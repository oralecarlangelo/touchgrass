package docker

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// Limits on one ReadFiles call. They bound memory, not correctness: a
// tree over a limit is an error, never a silently shortened result.
const (
	readFilesMaxEntries   = 20000
	readFilesMaxFileBytes = 8 << 20
	readFilesMaxBytes     = 64 << 20
)

// Errors reported by ReadFiles.
var (
	// ErrPathNotFound reports a path that does not exist in the container.
	ErrPathNotFound = errors.New("docker: path not found in container")
	// ErrTreeTooLarge reports a tree over the ReadFiles limits.
	ErrTreeTooLarge = errors.New("docker: tree is too large to read")
)

// ExecSpec is one command to run inside a running container. Cmd is an
// argument vector; no shell is involved.
type ExecSpec struct {
	Cmd        []string
	WorkingDir string
}

// Workdir returns the working directory a container was started with,
// or "/" when the image sets none.
func (c *Client) Workdir(ctx context.Context, id string) (string, error) {
	response, err := guardedCall(ctx, c.guard, defaultDockerCallTimeout,
		func(callCtx context.Context) (container.InspectResponse, error) {
			return c.cli.ContainerInspect(callCtx, id)
		})
	if err != nil {
		return "", fmt.Errorf("inspecting container %q: %w", id, err)
	}

	if response.Config == nil || response.Config.WorkingDir == "" {
		return "/", nil
	}

	return response.Config.WorkingDir, nil
}

// answered is the result of an engine call that the engine answered.
// A refusal such as "no such path" is an answer, not a daemon failure,
// so it travels in err and never counts against the request guard.
type answered[T any] struct {
	value T
	err   error
}

// ReadFiles returns the regular files under an absolute path of a
// container, keyed by slash path relative to it (a single file is
// keyed by its base name). It reads through the engine's archive API,
// so the image needs no shell or tools and the container need not be
// running.
func (c *Client) ReadFiles(ctx context.Context, id, containerPath string) (map[string][]byte, error) {
	result, err := guardedCall(ctx, c.guard, defaultDockerDiffTimeout,
		func(callCtx context.Context) (answered[map[string][]byte], error) {
			archive, _, err := c.cli.CopyFromContainer(callCtx, id, containerPath)
			if err != nil {
				if cerrdefs.IsNotFound(err) {
					return answered[map[string][]byte]{err: fmt.Errorf("%w: %s", ErrPathNotFound, containerPath)}, nil
				}

				return answered[map[string][]byte]{}, fmt.Errorf("reading %s from container: %w", containerPath, err)
			}

			defer func() {
				_ = archive.Close()
			}()

			// An oversized or unreadable tree is the container's
			// content, not the daemon's health.
			files, err := readTarFiles(archive, path.Base(containerPath))

			return answered[map[string][]byte]{value: files, err: err}, nil
		})
	if err != nil {
		return nil, err
	}

	return result.value, result.err
}

// readTarFiles collects the regular files of an engine archive. Entry
// names start with the copied path's base name, which is stripped.
func readTarFiles(archive io.Reader, base string) (map[string][]byte, error) {
	files := map[string][]byte{}
	reader := tar.NewReader(archive)

	var total int64

	for entries := 0; ; entries++ {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}

		if err != nil {
			return nil, fmt.Errorf("reading container archive: %w", err)
		}

		if entries >= readFilesMaxEntries {
			return nil, fmt.Errorf("%w: more than %d entries", ErrTreeTooLarge, readFilesMaxEntries)
		}

		if header.Typeflag != tar.TypeReg {
			continue
		}

		total += header.Size
		if header.Size > readFilesMaxFileBytes || total > readFilesMaxBytes {
			return nil, fmt.Errorf("%w: at %s", ErrTreeTooLarge, header.Name)
		}

		content, err := io.ReadAll(io.LimitReader(reader, readFilesMaxFileBytes))
		if err != nil {
			return nil, fmt.Errorf("reading %s from container archive: %w", header.Name, err)
		}

		files[relativeEntry(header.Name, base)] = content
	}
}

// relativeEntry strips the archive's leading base directory. A single
// copied file has no directory and keeps its name.
func relativeEntry(name, base string) string {
	clean := path.Clean(strings.TrimPrefix(name, "./"))
	if rest, ok := strings.CutPrefix(clean, base+"/"); ok {
		return rest
	}

	return clean
}

// Exec runs a command in a running container, copying its stdout and
// stderr to output as they arrive, and returns its exit code. It holds
// no engine permit while the command runs, so a long migration never
// starves the collectors; ctx bounds the whole run.
func (c *Client) Exec(ctx context.Context, id string, spec ExecSpec, output io.Writer) (int, error) {
	created, err := guardedCall(ctx, c.guard, defaultDockerCallTimeout,
		func(callCtx context.Context) (answered[container.ExecCreateResponse], error) {
			response, err := c.cli.ContainerExecCreate(callCtx, id, container.ExecOptions{
				Cmd:          spec.Cmd,
				WorkingDir:   spec.WorkingDir,
				AttachStdout: true,
				AttachStderr: true,
			})
			// A container that is gone or not running is an answer.
			if err != nil && (cerrdefs.IsNotFound(err) || cerrdefs.IsConflict(err)) {
				return answered[container.ExecCreateResponse]{err: err}, nil
			}

			return answered[container.ExecCreateResponse]{value: response}, err
		})
	if err == nil {
		err = created.err
	}

	if err != nil {
		return 0, fmt.Errorf("creating exec in container %q: %w", id, err)
	}

	attached, err := c.cli.ContainerExecAttach(ctx, created.value.ID, container.ExecAttachOptions{})
	if err != nil {
		return 0, fmt.Errorf("attaching to exec: %w", err)
	}

	defer attached.Close()

	// Closing the stream is what unblocks the copy when ctx ends.
	stop := context.AfterFunc(ctx, attached.Close)
	defer stop()

	if _, err := stdcopy.StdCopy(output, output, attached.Reader); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, fmt.Errorf("exec interrupted: %w", ctxErr)
		}

		return 0, fmt.Errorf("reading exec output: %w", err)
	}

	inspected, err := guardedCall(context.WithoutCancel(ctx), c.guard, defaultDockerCallTimeout,
		func(callCtx context.Context) (container.ExecInspect, error) {
			return c.cli.ContainerExecInspect(callCtx, created.value.ID)
		})
	if err != nil {
		return 0, fmt.Errorf("inspecting exec: %w", err)
	}

	return inspected.ExitCode, nil
}
