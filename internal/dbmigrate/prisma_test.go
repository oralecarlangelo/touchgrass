package dbmigrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
)

// testPassword must never appear in a label or an error.
const testPassword = "s3cr3t-Passw0rd"

func TestPrismaSourceFindsMigrationsAndTheirChecksums(t *testing.T) {
	t.Parallel()

	initSQL := []byte("CREATE TABLE widget (id INT PRIMARY KEY);\n")
	sum := sha256.Sum256(initSQL)

	source, err := NewPrisma().Source(Files{
		testPrismaInit + "/migration.sql":           initSQL,
		"20261102090000_add_colour/migration.sql":   []byte("ALTER TABLE widget ADD COLUMN colour TEXT;\n"),
		"migration_lock.toml":                       []byte(`provider = "postgresql"`),
		"20261102090000_add_colour/README.md":       []byte("notes"),
		"20261103000000_nested/extra/migration.sql": []byte("SELECT 1;"),
	}, Files{})
	if err != nil {
		t.Fatalf("Source() error = %v, want nil", err)
	}

	names := make([]string, 0, len(source.Migrations))
	for _, migration := range source.Migrations {
		names = append(names, migration.Name)
	}

	if want := []string{testPrismaInit, "20261102090000_add_colour"}; !slices.Equal(names, want) {
		t.Errorf("Source() migrations = %v, want %v", names, want)
	}

	// Prisma stores the SHA-256 of migration.sql; a match is what makes
	// an applied migration read as unmodified.
	if got := source.Migrations[0].Checksum; got != hex.EncodeToString(sum[:]) {
		t.Errorf("Source() checksum = %s, want the SHA-256 of the file", got)
	}

	if source.SeedCommand != "" {
		t.Errorf("Source() seed = %q, want none", source.SeedCommand)
	}
}

func TestPrismaSourceFindsTheDeclaredSeed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		manifests Files
		want      string
		wantErr   bool
	}{
		{
			name:      "package.json prisma.seed",
			manifests: Files{prismaPackageFile: []byte(`{"name":"app","prisma":{"seed":"ts-node prisma/seed.ts"}}`)},
			want:      "ts-node prisma/seed.ts",
		},
		{
			name:      "package.json without a seed",
			manifests: Files{prismaPackageFile: []byte(`{"name":"app"}`)},
		},
		{
			name: "prisma.config.ts wins",
			manifests: Files{
				prismaPackageFile:  []byte(`{"prisma":{"seed":"old"}}`),
				"prisma.config.ts": []byte("export default defineConfig({ migrations: { seed: 'tsx prisma/seed.ts' } })"),
			},
			want: "tsx prisma/seed.ts",
		},
		{
			name: "prisma.config.ts with a computed seed falls back",
			manifests: Files{
				prismaPackageFile:  []byte(`{"prisma":{"seed":"node seed.js"}}`),
				"prisma.config.ts": []byte("export default defineConfig({ migrations: { seed: seedCommand() } })"),
			},
			want: "node seed.js",
		},
		{name: "no manifests", manifests: Files{}},
		{name: "broken package.json", manifests: Files{prismaPackageFile: []byte(`{`)}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source, err := NewPrisma().Source(Files{}, test.manifests)
			if (err != nil) != test.wantErr {
				t.Fatalf("Source() error = %v, wantErr %t", err, test.wantErr)
			}

			if source.SeedCommand != test.want {
				t.Errorf("Source() seed = %q, want %q", source.SeedCommand, test.want)
			}
		})
	}
}

func TestPrismaCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		action  Action
		arg     string
		want    []string
		wantErr error
	}{
		{name: "deploy", action: ActionDeploy, want: []string{"migrate", "deploy"}},
		{name: "seed", action: ActionSeed, want: []string{"db", "seed"}},
		{
			name: "resolve applied", action: ActionResolveApplied, arg: testPrismaInit,
			want: []string{"migrate", "resolve", "--applied", testPrismaInit},
		},
		{
			name: "resolve rolled back", action: ActionResolveRolledBack, arg: testPrismaInit,
			want: []string{"migrate", "resolve", "--rolled-back", testPrismaInit},
		},
		{name: "resolve needs a name", action: ActionResolveApplied, wantErr: ErrInvalidName},
		{name: "a flag is not a name", action: ActionResolveApplied, arg: "--schema=/etc/passwd", wantErr: ErrInvalidName},
		{name: "a space is not a name", action: ActionResolveRolledBack, arg: "a b", wantErr: ErrInvalidName},
		{name: "unknown action", action: Action("reset"), wantErr: ErrUnsupportedAction},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := NewPrisma().Command(test.action, test.arg)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Command() error = %v, want %v", err, test.wantErr)
			}

			if !slices.Equal(got, test.want) {
				t.Errorf("Command() = %v, want %v", got, test.want)
			}
		})
	}
}

// describeCase is one database URL and what a tool should say of it.
type describeCase struct {
	name    string
	url     string
	want    string
	wantErr error
}

// checkDescribe runs a tool's Describe over cases and checks that the
// password reaches neither the label nor the error.
func checkDescribe(t *testing.T, tool Tool, tests []describeCase) {
	t.Helper()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			target, err := tool.Describe(test.url)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Describe() error = %v, want %v", err, test.wantErr)
			}

			if target.Label != test.want {
				t.Errorf("Describe() label = %q, want %q", target.Label, test.want)
			}

			if strings.Contains(target.Label, testPassword) {
				t.Errorf("Describe() label %q holds the password", target.Label)
			}

			if err != nil && strings.Contains(err.Error(), testPassword) {
				t.Errorf("Describe() error %q holds the password", err)
			}
		})
	}
}

func TestPrismaDescribe(t *testing.T) {
	t.Parallel()

	tests := []describeCase{
		{
			name: "prisma url with its own parameters",
			url:  "postgresql://app:" + testPassword + "@db.internal:6543/shop?schema=sales&connection_limit=5&pgbouncer=true",
			want: "db.internal:6543/shop (schema sales)",
		},
		{
			name: "defaults",
			url:  "postgres://app:" + testPassword + "@localhost/shop",
			want: "localhost:5432/shop (schema public)",
		},
		{name: "mysql is not readable yet", url: "mysql://app:" + testPassword + "@db/shop", wantErr: ErrUnsupportedDatabase},
		{name: "mongodb is not readable yet", url: "mongodb://app:" + testPassword + "@db/shop", wantErr: ErrUnsupportedDatabase},
		{name: "no database name", url: "postgres://app:" + testPassword + "@db:5432", wantErr: ErrInvalidURL},
		{name: "no scheme", url: "db:5432/shop", wantErr: ErrUnsupportedDatabase},
		{name: "empty", url: "", wantErr: ErrInvalidURL},
	}

	checkDescribe(t, NewPrisma(), tests)
}

// Prisma's own URL parameters are unknown startup parameters to
// PostgreSQL; only driver parameters may reach the connection.
func TestParsePostgresURLDropsPrismaParameters(t *testing.T) {
	t.Parallel()

	target, err := parsePostgresURL(
		"postgresql://app:" + testPassword + "@db:5432/shop?schema=sales&connection_limit=5&sslmode=require&pool_timeout=3")
	if err != nil {
		t.Fatalf("parsePostgresURL() error = %v, want nil", err)
	}

	if want := "postgresql://app:" + testPassword + "@db:5432/shop?sslmode=require"; target.dsn != want {
		t.Errorf("dsn = %q, want %q", target.dsn, want)
	}

	if target.schema != "sales" {
		t.Errorf("schema = %q, want sales", target.schema)
	}
}

func TestPrismaHistoryUsesTheParsedTarget(t *testing.T) {
	t.Parallel()

	var seen postgresTarget

	tool := &Prisma{history: func(_ context.Context, target postgresTarget) ([]Record, error) {
		seen = target

		return []Record{{Name: testInit}}, nil
	}}

	records, err := tool.History(context.Background(), "postgres://app:"+testPassword+"@db/shop?schema=sales")
	if err != nil || len(records) != 1 {
		t.Fatalf("History() = %v, %v, want one record", records, err)
	}

	if seen.schema != "sales" || seen.database != "shop" || seen.host != "db" {
		t.Errorf("History() read %+v, want db/shop schema sales", seen)
	}

	if _, err := tool.History(context.Background(), "mongodb://db/shop"); !errors.Is(err, ErrUnsupportedDatabase) {
		t.Errorf("History(mongodb) error = %v, want ErrUnsupportedDatabase", err)
	}
}

func TestRegistry(t *testing.T) {
	t.Parallel()

	registry := NewRegistry(NewPrisma())

	if got := registry.Names(); !slices.Equal(got, []string{PrismaName}) {
		t.Errorf("Names() = %v, want [prisma]", got)
	}

	if tool, err := registry.Get(PrismaName); err != nil || tool.Name() != PrismaName {
		t.Errorf("Get(prisma) = %v, %v, want the prisma tool", tool, err)
	}

	if _, err := registry.Get("flyway"); !errors.Is(err, ErrUnknownTool) {
		t.Errorf("Get(flyway) error = %v, want ErrUnknownTool", err)
	}
}

func TestValidName(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		testPrismaInit:           true,
		"V2.1__add-users":        true,
		"":                       false,
		"-rf":                    false,
		"../escape":              false,
		"a b":                    false,
		"a;b":                    false,
		strings.Repeat("a", 256): false,
	} {
		if got := ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %t, want %t", name, got, want)
		}
	}
}
