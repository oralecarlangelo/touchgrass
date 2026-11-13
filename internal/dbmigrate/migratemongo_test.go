package dbmigrate

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// Migration files the migrate-mongo tests share.
const (
	testMongoInit   = "20261101100000-create-widgets.js"
	testMongoColour = "20261102090000-add-colour.js"
	// testMongoPlainJS is the .js file among mixed extensions.
	testMongoPlainJS = "20261101100000-init.js"
)

// testMongoInitFile is a migration and testMongoInitHash the hash
// migrate-mongo 14.0.7 itself reported for it with useFileHash on
// (`migrate-mongo status`), so a match here is a match with the tool.
const (
	testMongoInitFile = `module.exports = {
  async up(db) {
    await db.collection("widgets").createIndex({ sku: 1 }, { unique: true });
  },
  async down(db) {
    await db.collection("widgets").dropIndex("sku_1");
  },
};
`
	testMongoInitHash = "f3f43321ddb9cc251f84fd58dde7295eba9f9a350f1737d65e256eb32114cd33"
)

// shippedNames lists the names of a source's migrations.
func shippedNames(source Source) []string {
	names := make([]string, 0, len(source.Migrations))

	for _, migration := range source.Migrations {
		names = append(names, migration.Name)
	}

	return names
}

func TestMigrateMongoSourceFindsMigrationsAndTheirChecksums(t *testing.T) {
	t.Parallel()

	source, err := NewMigrateMongo().Source(Files{
		testMongoColour:              []byte("module.exports = { async up() {}, async down() {} };\n"),
		testMongoInit:                []byte(testMongoInitFile),
		"sample-migration.js":        []byte("module.exports = {};\n"),
		"README.md":                  []byte("notes"),
		"20261103000000-later.ts":    []byte("export const up = async () => {};\n"),
		"helpers/20261104-nested.js": []byte("module.exports = {};\n"),
	}, Files{})
	if err != nil {
		t.Fatalf("Source() error = %v, want nil", err)
	}

	if got, want := shippedNames(source), []string{testMongoInit, testMongoColour}; !slices.Equal(got, want) {
		t.Errorf("Source() migrations = %v, want %v", got, want)
	}

	if got := source.Migrations[0].Checksum; got != testMongoInitHash {
		t.Errorf("Source() checksum = %s, want %s as migrate-mongo records it", got, testMongoInitHash)
	}

	if source.SeedCommand != "" {
		t.Errorf("Source() seed = %q, want none: migrate-mongo has no seed", source.SeedCommand)
	}
}

func TestMigrateMongoSourceUsesTheConfiguredExtension(t *testing.T) {
	t.Parallel()

	files := Files{
		"20261101100000-init.ts":  []byte("export const up = async () => {};\n"),
		testMongoPlainJS:          []byte("module.exports = {};\n"),
		"20261102090000-next.cjs": []byte("module.exports = {};\n"),
		"sample-migration.ts":     []byte("export {};\n"),
		"sample-migration.js":     []byte("module.exports = {};\n"),
	}

	tests := []struct {
		name   string
		config string
		want   []string
	}{
		{name: "no config file", want: []string{testMongoPlainJS}},
		{name: "config without the key", config: `module.exports = { migrationsDir: "migrations" };`, want: []string{testMongoPlainJS}},
		{name: "double quotes", config: `module.exports = { migrationFileExtension: ".ts" };`, want: []string{"20261101100000-init.ts"}},
		{name: "single quotes", config: `const config = { migrationFileExtension : '.cjs' };`, want: []string{"20261102090000-next.cjs"}},
		{name: "computed value falls back", config: `module.exports = { migrationFileExtension: process.env.EXT };`, want: []string{testMongoPlainJS}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			manifests := Files{}
			if test.config != "" {
				manifests[migrateMongoConfigFile] = []byte(test.config)
			}

			source, err := NewMigrateMongo().Source(files, manifests)
			if err != nil {
				t.Fatalf("Source() error = %v, want nil", err)
			}

			if got := shippedNames(source); !slices.Equal(got, test.want) {
				t.Errorf("Source() migrations = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMigrateMongoCommand(t *testing.T) {
	t.Parallel()

	tool := NewMigrateMongo()

	got, err := tool.Command(ActionDeploy, "")
	if err != nil || !slices.Equal(got, []string{"up"}) {
		t.Errorf("Command(deploy) = %v, %v, want [up]", got, err)
	}

	if prefix := tool.DefaultCommand(); !slices.Equal(prefix, []string{"npx", "--no-install", "migrate-mongo"}) {
		t.Errorf("DefaultCommand() = %v, want npx --no-install migrate-mongo", prefix)
	}

	// The changelog has no failed or rolled-back state to resolve, and
	// the tool has no seed.
	for _, action := range []Action{ActionResolveApplied, ActionResolveRolledBack, ActionSeed, Action("down")} {
		if _, err := tool.Command(action, testMongoInit); !errors.Is(err, ErrUnsupportedAction) {
			t.Errorf("Command(%s) error = %v, want ErrUnsupportedAction", action, err)
		}
	}
}

func TestMigrateMongoDescribe(t *testing.T) {
	t.Parallel()

	creds := "app:" + testPassword + "@"

	tests := []describeCase{
		{name: "defaults", url: "mongodb://" + creds + "db.internal/shop", want: "db.internal:27017/shop (collection changelog)"},
		{
			name: "replica set with ports and options",
			url:  "mongodb://" + creds + "db1:27018,db2,db3:27020/shop?replicaSet=rs0&authSource=admin&w=majority",
			want: "db1:27018,db2:27017,db3:27020/shop (collection changelog)",
		},
		{
			name: "dns seed list",
			url:  "mongodb+srv://" + creds + "cluster0.example.net/shop?retryWrites=true",
			want: "cluster0.example.net/shop (collection changelog)",
		},
		{
			name: "named changelog collection",
			url:  "mongodb://" + creds + "db/shop?changelogCollection=schema_log",
			want: "db:27017/shop (collection schema_log)",
		},
		{name: "ipv6 host", url: "mongodb://" + creds + "[::1]:27018/shop", want: "[::1]:27018/shop (collection changelog)"},
		{name: "no credentials", url: "mongodb://localhost/shop", want: "localhost:27017/shop (collection changelog)"},
		{name: "password holding an at sign", url: "mongodb://app:p@ss" + testPassword + "@db/shop", want: "db:27017/shop (collection changelog)"},
		{name: "postgres is another tool's", url: "postgres://" + creds + "db/shop", wantErr: ErrUnsupportedDatabase},
		{name: "mysql", url: "mysql://" + creds + "db/shop", wantErr: ErrUnsupportedDatabase},
		{name: "no database name", url: "mongodb://" + creds + "db:27017", wantErr: ErrInvalidURL},
		{name: "empty database name", url: "mongodb://" + creds + "db:27017/?replicaSet=rs0", wantErr: ErrInvalidURL},
		{name: "database name with a dot", url: "mongodb://" + creds + "db/shop.widgets", wantErr: ErrInvalidURL},
		{name: "no host", url: "mongodb://" + creds + "/shop", wantErr: ErrInvalidURL},
		{name: "empty host in the list", url: "mongodb://" + creds + "db1,,db3/shop", wantErr: ErrInvalidURL},
		{name: "dns seed list with a port", url: "mongodb+srv://" + creds + "cluster0.example.net:27017/shop", wantErr: ErrInvalidURL},
		{name: "dns seed list with two hosts", url: "mongodb+srv://" + creds + "a.example.net,b.example.net/shop", wantErr: ErrInvalidURL},
		{name: "system collection", url: "mongodb://" + creds + "db/shop?changelogCollection=system.users", wantErr: ErrInvalidURL},
		{name: "collection with a dollar", url: "mongodb://" + creds + "db/shop?changelogCollection=a$b", wantErr: ErrInvalidURL},
		{name: "no scheme", url: creds + "db/shop", wantErr: ErrInvalidURL},
		{name: "empty", url: "", wantErr: ErrInvalidURL},
	}

	checkDescribe(t, NewMigrateMongo(), tests)
}

func TestParseMongoURLKeepsOnlyDriverOptions(t *testing.T) {
	t.Parallel()

	target, err := parseMongoURL("mongodb://app:" + testPassword + "@db1:27018,db2/shop" +
		"?replicaset=rs0&authSource=admin&w=majority&retryWrites=true&changelogCollection=schema_log&tls=true#frag")
	if err != nil {
		t.Fatalf("parseMongoURL() error = %v, want nil", err)
	}

	// Options match without regard to case, as MongoDB reads them; the
	// tool's own parameter and the write-side options are gone.
	want := "mongodb://app:" + testPassword + "@db1:27018,db2/shop?authSource=admin&replicaset=rs0&tls=true"
	if target.dsn != want {
		t.Errorf("parseMongoURL() dsn = %q, want %q", target.dsn, want)
	}

	if target.database != "shop" || target.collection != "schema_log" {
		t.Errorf("parseMongoURL() = %s.%s, want shop.schema_log", target.database, target.collection)
	}
}

func TestMigrateMongoHistoryUsesTheParsedTarget(t *testing.T) {
	t.Parallel()

	var seen mongoTarget

	tool := &MigrateMongo{history: func(_ context.Context, target mongoTarget) ([]Record, error) {
		seen = target

		return []Record{{Name: testMongoInit}}, nil
	}}

	records, err := tool.History(context.Background(), "mongodb://app:"+testPassword+"@db/shop?changelogCollection=schema_log")
	if err != nil || len(records) != 1 {
		t.Fatalf("History() = %v, %v, want one record", records, err)
	}

	if seen.database != "shop" || seen.collection != "schema_log" || !slices.Equal(seen.hosts, []string{"db:27017"}) {
		t.Errorf("History() read %+v, want db:27017 shop.schema_log", seen)
	}

	if _, err := tool.History(context.Background(), "postgres://db/shop"); !errors.Is(err, ErrUnsupportedDatabase) {
		t.Errorf("History(postgres) error = %v, want ErrUnsupportedDatabase", err)
	}
}

func TestChangelogRecordsMapEveryRowAsApplied(t *testing.T) {
	t.Parallel()

	records := changelogRecords([]changelogRow{
		{FileName: testMongoInit, AppliedAt: at(0)},
		{FileName: testMongoColour, FileHash: "abc", AppliedAt: at(1)},
		{AppliedAt: at(2)},
	})

	if len(records) != 2 {
		t.Fatalf("changelogRecords() = %d records, want 2 (a row without a file name is skipped)", len(records))
	}

	for _, record := range records {
		if record.FinishedAt == nil || !record.FinishedAt.Equal(record.StartedAt) || record.RolledBackAt != nil {
			t.Errorf("changelogRecords() %s = %+v, want applied at one instant", record.Name, record)
		}
	}

	if records[0].Checksum != "" || records[1].Checksum != "abc" {
		t.Errorf("changelogRecords() checksums = %q, %q, want none and abc", records[0].Checksum, records[1].Checksum)
	}
}
