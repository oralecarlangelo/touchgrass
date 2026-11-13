package dbmigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// MigrateMongoName is the stored identifier of the migrate-mongo tool.
const MigrateMongoName = "migrate-mongo"

// migrate-mongo file layout, relative to the application's working
// directory, and the defaults of its config file.
const (
	migrateMongoMigrationsDir = "migrations"
	migrateMongoConfigFile    = "migrate-mongo-config.js"
	migrateMongoDefaultExt    = ".js"
	// migrateMongoSample is the template migrate-mongo itself skips.
	migrateMongoSample = "sample-migration"
)

// migrateMongoExtRe finds the migration file extension in the config
// file. The file is JavaScript, so this is a best-effort read of the
// literal `migrationFileExtension: ".ts"` form; a computed value falls
// back to the default.
var migrateMongoExtRe = regexp.MustCompile("migrationFileExtension\\s*:\\s*[\"'`](\\.[A-Za-z0-9.]+)[\"'`]")

// MigrateMongo reads migrate-mongo state: the changelog collection it
// keeps in MongoDB and the migration scripts a build ships.
//
// migrate-mongo records a migration only after it succeeds and removes
// the record when it is rolled back, so a migration is never failed or
// rolled back here, and there is nothing to resolve. It has no seed.
type MigrateMongo struct {
	history changelogReader
}

// changelogReader reads the migrate-mongo changelog of one database.
type changelogReader func(ctx context.Context, target mongoTarget) ([]Record, error)

// NewMigrateMongo builds the migrate-mongo tool.
func NewMigrateMongo() *MigrateMongo {
	return &MigrateMongo{history: readMigrateMongo}
}

// Name implements Tool.
func (*MigrateMongo) Name() string {
	return MigrateMongoName
}

// Layout implements Tool.
func (*MigrateMongo) Layout() Layout {
	return Layout{
		MigrationsDir: migrateMongoMigrationsDir,
		Manifests:     []string{migrateMongoConfigFile},
	}
}

// DefaultCommand implements Tool. --no-install keeps npx from fetching
// the CLI from the network when the image does not ship it.
func (*MigrateMongo) DefaultCommand() []string {
	return []string{"npx", "--no-install", "migrate-mongo"}
}

// Describe implements Tool.
func (*MigrateMongo) Describe(databaseURL string) (Target, error) {
	target, err := parseMongoURL(databaseURL)
	if err != nil {
		return Target{}, err
	}

	return Target{Label: target.label()}, nil
}

// History implements Tool.
func (m *MigrateMongo) History(ctx context.Context, databaseURL string) ([]Record, error) {
	target, err := parseMongoURL(databaseURL)
	if err != nil {
		return nil, err
	}

	return m.history(ctx, target)
}

// Source implements Tool. A migration is a file directly in the
// migrations directory with the configured extension, named as the
// changelog records it. Its checksum is the SHA-256 of the file, which
// is what migrate-mongo stores when useFileHash is on.
func (*MigrateMongo) Source(migrations, manifests Files) (Source, error) {
	ext := migrateMongoExt(manifests)
	shipped := []Shipped{}

	for name, content := range migrations {
		if strings.Contains(name, "/") || path.Ext(name) != ext || name == migrateMongoSample+ext {
			continue
		}

		sum := sha256.Sum256(content)

		shipped = append(shipped, Shipped{Name: name, Checksum: hex.EncodeToString(sum[:])})
	}

	slices.SortFunc(shipped, func(a, b Shipped) int {
		return strings.Compare(a.Name, b.Name)
	})

	return Source{Migrations: shipped}, nil
}

// migrateMongoExt returns the migration file extension the build's
// config declares, or the tool's default.
func migrateMongoExt(manifests Files) string {
	if match := migrateMongoExtRe.FindSubmatch(manifests[migrateMongoConfigFile]); match != nil {
		return string(match[1])
	}

	return migrateMongoDefaultExt
}

// Command implements Tool. Only "up" is offered: "down" undoes one
// migration at a time and is a decision for a person at a terminal.
func (*MigrateMongo) Command(action Action, _ string) ([]string, error) {
	if action == ActionDeploy {
		return []string{"up"}, nil
	}

	return nil, fmt.Errorf("%w: %q", ErrUnsupportedAction, action)
}
