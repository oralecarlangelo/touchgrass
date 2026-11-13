package docker

import (
	"archive/tar"
	"bytes"
	"errors"
	"testing"
)

// tarEntry is one entry of a test archive.
type tarEntry struct {
	name     string
	typeflag byte
	content  string
}

// testManifest is a single file copied on its own.
const testManifest = "package.json"

// buildTar writes entries the way the engine's archive API does.
func buildTar(t *testing.T, entries []tarEntry) *bytes.Buffer {
	t.Helper()

	var buffer bytes.Buffer

	writer := tar.NewWriter(&buffer)

	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Typeflag: entry.typeflag, Mode: 0o644, Size: int64(len(entry.content))}

		if err := writer.WriteHeader(header); err != nil {
			t.Fatalf("WriteHeader(%s) error = %v, want nil", entry.name, err)
		}

		if _, err := writer.Write([]byte(entry.content)); err != nil {
			t.Fatalf("Write(%s) error = %v, want nil", entry.name, err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}

	return &buffer
}

func TestReadTarFilesKeysByPathUnderTheCopiedDirectory(t *testing.T) {
	t.Parallel()

	archive := buildTar(t, []tarEntry{
		{name: "migrations/", typeflag: tar.TypeDir},
		{name: "migrations/001_init/", typeflag: tar.TypeDir},
		{name: "migrations/001_init/migration.sql", typeflag: tar.TypeReg, content: "CREATE TABLE a ();"},
		{name: "migrations/migration_lock.toml", typeflag: tar.TypeReg, content: "provider"},
		{name: "migrations/latest", typeflag: tar.TypeSymlink},
	})

	files, err := readTarFiles(archive, "migrations")
	if err != nil {
		t.Fatalf("readTarFiles() error = %v, want nil", err)
	}

	if len(files) != 2 {
		t.Fatalf("readTarFiles() returned %d files, want the 2 regular ones", len(files))
	}

	if got := string(files["001_init/migration.sql"]); got != "CREATE TABLE a ();" {
		t.Errorf("001_init/migration.sql = %q, want its content", got)
	}

	if _, ok := files["migration_lock.toml"]; !ok {
		t.Errorf("files = %v, want migration_lock.toml keyed without the directory", files)
	}
}

func TestReadTarFilesKeysASingleFileByItsName(t *testing.T) {
	t.Parallel()

	archive := buildTar(t, []tarEntry{{name: testManifest, typeflag: tar.TypeReg, content: "{}"}})

	files, err := readTarFiles(archive, testManifest)
	if err != nil {
		t.Fatalf("readTarFiles() error = %v, want nil", err)
	}

	if got := string(files[testManifest]); got != "{}" {
		t.Errorf("package.json = %q, want {}", got)
	}
}

// A tree over the limit is an error: a shortened result would report
// migrations as missing that are really there.
func TestReadTarFilesRefusesAnOversizedFile(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer

	writer := tar.NewWriter(&buffer)

	if err := writer.WriteHeader(&tar.Header{
		Name: "migrations/huge/migration.sql", Typeflag: tar.TypeReg, Mode: 0o644, Size: readFilesMaxFileBytes + 1,
	}); err != nil {
		t.Fatalf("WriteHeader() error = %v, want nil", err)
	}

	// The header alone is enough: the size is refused before any read.
	if _, err := readTarFiles(&buffer, "migrations"); !errors.Is(err, ErrTreeTooLarge) {
		t.Errorf("readTarFiles() error = %v, want ErrTreeTooLarge", err)
	}
}

func TestReadTarFilesReportsACorruptArchive(t *testing.T) {
	t.Parallel()

	if _, err := readTarFiles(bytes.NewReader([]byte("not a tar archive at all, just text padding....")), "x"); err == nil {
		t.Error("readTarFiles() error = nil, want a read error")
	}
}

func TestRelativeEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, entry, base, want string
	}{
		{name: "file in the directory", entry: "migrations/a/migration.sql", base: "migrations", want: "a/migration.sql"},
		{name: "dot prefix", entry: "./migrations/a.sql", base: "migrations", want: "a.sql"},
		{name: "single file", entry: testManifest, base: testManifest, want: testManifest},
		{name: "similar prefix is not stripped", entry: "migrations2/a.sql", base: "migrations", want: "migrations2/a.sql"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := relativeEntry(test.entry, test.base); got != test.want {
				t.Errorf("relativeEntry(%q, %q) = %q, want %q", test.entry, test.base, got, test.want)
			}
		})
	}
}
