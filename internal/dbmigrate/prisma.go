package dbmigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// PrismaName is the stored identifier of the Prisma Migrate tool.
const PrismaName = "prisma"

// Prisma file layout, relative to the application's working directory.
const (
	prismaMigrationsDir = "prisma/migrations"
	prismaMigrationFile = "migration.sql"
	prismaPackageFile   = "package.json"
	prismaConfigFile    = "prisma.config.ts"
)

// prismaConfigSeedRe finds a seed command in prisma.config.ts. The
// file is TypeScript, so this is a best-effort read of the literal
// `seed: "..."` form; a computed value is reported as no seed.
var prismaConfigSeedRe = regexp.MustCompile("seed\\s*:\\s*(?:\"([^\"\\n]+)\"|'([^'\\n]+)'|`([^`\\n]+)`)")

// Prisma reads Prisma Migrate state. It supports the SQL history table
// Prisma keeps in PostgreSQL; other Prisma datasources are refused
// with ErrUnsupportedDatabase until they have a reader.
type Prisma struct {
	history historyReader
}

// historyReader reads the Prisma history table of one database.
type historyReader func(ctx context.Context, target postgresTarget) ([]Record, error)

// NewPrisma builds the Prisma tool.
func NewPrisma() *Prisma {
	return &Prisma{history: readPrismaPostgres}
}

// Name implements Tool.
func (*Prisma) Name() string {
	return PrismaName
}

// Layout implements Tool.
func (*Prisma) Layout() Layout {
	return Layout{
		MigrationsDir: prismaMigrationsDir,
		Manifests:     []string{prismaPackageFile, prismaConfigFile},
	}
}

// DefaultCommand implements Tool. --no-install keeps npx from fetching
// the CLI from the network when the image does not ship it.
func (*Prisma) DefaultCommand() []string {
	return []string{"npx", "--no-install", "prisma"}
}

// Describe implements Tool.
func (*Prisma) Describe(databaseURL string) (Target, error) {
	target, err := parsePostgresURL(databaseURL)
	if err != nil {
		return Target{}, err
	}

	return Target{Label: target.label()}, nil
}

// History implements Tool.
func (p *Prisma) History(ctx context.Context, databaseURL string) ([]Record, error) {
	target, err := parsePostgresURL(databaseURL)
	if err != nil {
		return nil, err
	}

	return p.history(ctx, target)
}

// Source implements Tool. A Prisma migration is a directory holding a
// migration.sql; its checksum is the SHA-256 of that file, which is
// what Prisma stores when it applies it.
func (*Prisma) Source(migrations, manifests Files) (Source, error) {
	shipped := []Shipped{}

	for filePath, content := range migrations {
		dir, file := path.Split(filePath)
		name := strings.TrimSuffix(dir, "/")

		if file != prismaMigrationFile || name == "" || strings.Contains(name, "/") {
			continue
		}

		sum := sha256.Sum256(content)

		shipped = append(shipped, Shipped{Name: name, Checksum: hex.EncodeToString(sum[:])})
	}

	slices.SortFunc(shipped, func(a, b Shipped) int {
		return strings.Compare(a.Name, b.Name)
	})

	seed, err := prismaSeed(manifests)
	if err != nil {
		return Source{}, err
	}

	return Source{Migrations: shipped, SeedCommand: seed}, nil
}

// prismaSeed finds the declared seed: prisma.config.ts first (it wins
// in Prisma when present), then the "prisma.seed" key of package.json.
func prismaSeed(manifests Files) (string, error) {
	if config, ok := manifests[prismaConfigFile]; ok {
		if match := prismaConfigSeedRe.FindSubmatch(config); match != nil {
			return string(slices.MaxFunc(match[1:], func(a, b []byte) int {
				return len(a) - len(b)
			})), nil
		}
	}

	raw, ok := manifests[prismaPackageFile]
	if !ok {
		return "", nil
	}

	var manifest struct {
		Prisma struct {
			Seed string `json:"seed"`
		} `json:"prisma"`
	}

	if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", fmt.Errorf("parsing %s: %w", prismaPackageFile, err)
	}

	return manifest.Prisma.Seed, nil
}

// Command implements Tool.
func (*Prisma) Command(action Action, name string) ([]string, error) {
	switch action {
	case ActionDeploy:
		return []string{"migrate", "deploy"}, nil
	case ActionSeed:
		return []string{"db", "seed"}, nil
	case ActionResolveApplied:
		return prismaResolve("--applied", name)
	case ActionResolveRolledBack:
		return prismaResolve("--rolled-back", name)
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAction, action)
	}
}

// prismaResolve builds a migrate resolve command for one migration.
func prismaResolve(flag, name string) ([]string, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidName, name)
	}

	return []string{"migrate", "resolve", flag, name}, nil
}
