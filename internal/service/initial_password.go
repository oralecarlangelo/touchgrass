package service

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// InitialPasswordFileName is the file, kept beside the database, that
// holds a generated one-time admin password until it is replaced.
const InitialPasswordFileName = "initial-admin-password"

// initialPasswordMode keeps the one-time password readable by the
// service user only.
const initialPasswordMode fs.FileMode = 0o600

// generatePassword returns a random one-time password: 26 base32
// characters carrying 130 bits from the system CSPRNG, well above the
// managed-account minimum and free of shell or env-file metacharacters.
func generatePassword() string {
	return rand.Text()
}

// writeInitialPassword stores password at path with mode 0600. It
// writes a sibling temp file and renames it, so a reader never sees a
// partial password and a stale file from an earlier boot is replaced.
func writeInitialPassword(path, password string) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("creating initial password file: %w", err)
	}

	tempName := temp.Name()

	if err := writeAndClose(temp, password+"\n"); err != nil {
		return errors.Join(err, removeIfPresent(tempName))
	}

	if err := os.Rename(tempName, path); err != nil {
		return errors.Join(fmt.Errorf("installing initial password file: %w", err), removeIfPresent(tempName))
	}

	return nil
}

// writeAndClose writes content to an open 0600 file and closes it.
func writeAndClose(file *os.File, content string) error {
	if err := file.Chmod(initialPasswordMode); err != nil {
		return errors.Join(fmt.Errorf("restricting initial password file: %w", err), file.Close())
	}

	if _, err := file.WriteString(content); err != nil {
		return errors.Join(fmt.Errorf("writing initial password file: %w", err), file.Close())
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("closing initial password file: %w", err)
	}

	return nil
}

// removeIfPresent deletes path; a missing file is not an error.
func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing %s: %w", filepath.Base(path), err)
	}

	return nil
}

// fileExists reports whether path names an existing file.
func fileExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}
