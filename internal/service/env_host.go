package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// backupSuffix names the single previous-file copy kept beside a target.
const backupSuffix = ".bak"

// defaultEnvFileMode applies when a target declares no mode.
const defaultEnvFileMode = os.FileMode(0o600)

// errEnvCommandEmpty reports a configured command with no words.
var errEnvCommandEmpty = errors.New("command is empty")

// EnvHost performs the host-side file and command work of an env apply.
// Implementations must honour sudo by escalating with `sudo -n`; the
// local implementation does so, tests substitute fakes.
type EnvHost interface {
	// ReadFile returns the target file contents.
	ReadFile(ctx context.Context, path string, sudo bool) ([]byte, error)
	// WriteFile creates or replaces path with data, mode, and owner.
	WriteFile(ctx context.Context, path string, data []byte, mode, owner string, sudo bool) error
	// Replace moves temp over target, keeping one .bak of the previous
	// target. It reports whether a previous file existed.
	Replace(ctx context.Context, temp, target string, sudo bool) (bool, error)
	// Restore puts the .bak back over target, or removes target when no
	// previous file existed.
	Restore(ctx context.Context, target string, hadPrevious, sudo bool) error
	// Remove deletes path, ignoring a missing file.
	Remove(ctx context.Context, path string, sudo bool) error
	// Run executes a whitespace-split command with extra environment and
	// optional stdin, streaming merged output lines to emit.
	Run(ctx context.Context, command string, input []byte, env []string, sudo bool, emit func(string)) error
	// Output executes a whitespace-split command and returns its stdout.
	Output(ctx context.Context, command string, env []string, sudo bool) ([]byte, error)
}

// localEnvHost is the production EnvHost: direct os calls without sudo,
// `sudo -n` subprocesses with it.
type localEnvHost struct{}

// NewLocalEnvHost returns the production EnvHost.
func NewLocalEnvHost() EnvHost {
	return localEnvHost{}
}

// ReadFile implements EnvHost.
func (localEnvHost) ReadFile(ctx context.Context, path string, sudo bool) ([]byte, error) {
	if !sudo {
		data, err := os.ReadFile(path) //nolint:gosec // admin-configured target path.
		if err != nil {
			return nil, fmt.Errorf("reading env file: %w", err)
		}

		return data, nil
	}

	var out bytes.Buffer

	//nolint:gosec // sudo arguments are admin-configured paths and commands from service config, never request input.
	cmd := exec.CommandContext(ctx, "sudo", "-n", "cat", path)
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("reading env file with sudo: %w", err)
	}

	return out.Bytes(), nil
}

// WriteFile implements EnvHost.
func (localEnvHost) WriteFile(
	ctx context.Context,
	path string,
	data []byte,
	mode, owner string,
	sudo bool,
) error {
	perm, err := parseFileMode(mode)
	if err != nil {
		return err
	}

	if sudo {
		return sudoWrite(ctx, path, data, perm, owner)
	}

	if err := os.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("writing env file: %w", err)
	}

	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("setting env file mode: %w", err)
	}

	return chownLocal(path, owner)
}

// Replace implements EnvHost.
func (localEnvHost) Replace(ctx context.Context, temp, target string, sudo bool) (bool, error) {
	if sudo {
		return sudoReplace(ctx, temp, target)
	}

	hadPrevious := true

	if err := os.Rename(target, target+backupSuffix); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("backing up env file: %w", err)
		}

		hadPrevious = false
	}

	if err := os.Rename(temp, target); err != nil {
		return hadPrevious, fmt.Errorf("replacing env file: %w", err)
	}

	return hadPrevious, nil
}

// Restore implements EnvHost.
func (localEnvHost) Restore(ctx context.Context, target string, hadPrevious, sudo bool) error {
	if !hadPrevious {
		return localEnvHost{}.Remove(ctx, target, sudo)
	}

	if sudo {
		return sudoRun(ctx, "mv", "-f", target+backupSuffix, target)
	}

	if err := os.Rename(target+backupSuffix, target); err != nil {
		return fmt.Errorf("restoring env file: %w", err)
	}

	return nil
}

// Remove implements EnvHost.
func (localEnvHost) Remove(ctx context.Context, path string, sudo bool) error {
	if sudo {
		return sudoRun(ctx, "rm", "-f", path)
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing env file: %w", err)
	}

	return nil
}

// Run implements EnvHost.
func (localEnvHost) Run(
	ctx context.Context,
	command string,
	input []byte,
	env []string,
	sudo bool,
	emit func(string),
) error {
	cmd, err := buildCommand(ctx, command, env, sudo)
	if err != nil {
		return err
	}

	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}

	writer := &lineWriter{emit: emit}
	cmd.Stdout = writer
	cmd.Stderr = writer

	err = cmd.Run()

	writer.flush()

	if err != nil {
		return fmt.Errorf("command failed: %w", err)
	}

	return nil
}

// Output implements EnvHost.
func (localEnvHost) Output(ctx context.Context, command string, env []string, sudo bool) ([]byte, error) {
	cmd, err := buildCommand(ctx, command, env, sudo)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer

	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("command failed: %w", err)
	}

	return out.Bytes(), nil
}

// buildCommand splits an admin-configured command on whitespace and
// escalates it with `sudo -n env` when sudo is set.
func buildCommand(ctx context.Context, command string, env []string, sudo bool) (*exec.Cmd, error) {
	words := strings.Fields(command)
	if len(words) == 0 {
		return nil, errEnvCommandEmpty
	}

	if sudo {
		args := append([]string{"-n", "env"}, env...)
		args = append(args, words...)

		//nolint:gosec // sudo arguments are admin-configured paths and commands from service config, never request input.
		return exec.CommandContext(ctx, "sudo", args...), nil
	}

	//nolint:gosec // command is admin-configured service config, split on whitespace, never request input.
	cmd := exec.CommandContext(ctx, words[0], words[1:]...)

	cmd.Env = append(os.Environ(), env...)

	return cmd, nil
}

// sudoWrite streams data through a privileged shell that applies the
// mode and owner before anything else can read the file.
func sudoWrite(ctx context.Context, path string, data []byte, perm os.FileMode, owner string) error {
	script := `umask 077 && cat > "$1" && chmod "$2" "$1" && { [ -z "$3" ] || chown "$3" "$1"; }`
	//nolint:gosec // sudo arguments are admin-configured paths and commands from service config, never request input.
	cmd := exec.CommandContext(ctx, "sudo", "-n", "sh", "-c", script, "sh", path, fmt.Sprintf("%04o", perm), owner)
	cmd.Stdin = bytes.NewReader(data)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("writing env file with sudo: %w", err)
	}

	return nil
}

// sudoReplace backs up and replaces the target under sudo.
func sudoReplace(ctx context.Context, temp, target string) (bool, error) {
	hadPrevious := true

	//nolint:gosec // sudo arguments are admin-configured paths and commands from service config, never request input.
	if err := exec.CommandContext(ctx, "sudo", "-n", "test", "-e", target).Run(); err != nil {
		hadPrevious = false
	}

	if hadPrevious {
		if err := sudoRun(ctx, "mv", "-f", target, target+backupSuffix); err != nil {
			return false, err
		}
	}

	if err := sudoRun(ctx, "mv", "-f", temp, target); err != nil {
		return hadPrevious, err
	}

	return hadPrevious, nil
}

// sudoRun executes one privileged command without output.
func sudoRun(ctx context.Context, args ...string) error {
	full := append([]string{"-n"}, args...)

	//nolint:gosec // sudo arguments are admin-configured paths and commands from service config, never request input.
	if err := exec.CommandContext(ctx, "sudo", full...).Run(); err != nil {
		return fmt.Errorf("sudo %s failed: %w", args[0], err)
	}

	return nil
}

// parseFileMode reads an octal mode such as "0600"; empty means 0600.
func parseFileMode(mode string) (os.FileMode, error) {
	if mode == "" {
		return defaultEnvFileMode, nil
	}

	parsed, err := strconv.ParseUint(mode, 8, 32)
	if err != nil || parsed > 0o777 {
		return 0, fmt.Errorf("%w: env target mode %q is not an octal permission", ErrInvalidInput, mode)
	}

	return os.FileMode(parsed), nil
}

// chownLocal sets the owner without sudo; an empty or current owner is a
// no-op so unprivileged installs keep working.
func chownLocal(path, owner string) error {
	if owner == "" {
		return nil
	}

	target, err := user.Lookup(owner)
	if err != nil {
		return fmt.Errorf("resolving env file owner: %w", err)
	}

	current, err := user.Current()
	if err == nil && current.Uid == target.Uid {
		return nil
	}

	uid, err := strconv.Atoi(target.Uid)
	if err != nil {
		return fmt.Errorf("parsing owner uid: %w", err)
	}

	gid, err := strconv.Atoi(target.Gid)
	if err != nil {
		return fmt.Errorf("parsing owner gid: %w", err)
	}

	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("setting env file owner: %w", err)
	}

	return nil
}

// envTempPath names the candidate file written beside the target.
func envTempPath(target, runID string) string {
	return filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+"."+runID+".tmp")
}
