//go:build integration

package dbmigrate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// mongoURLEnvVar names a MongoDB the reader is exercised against. The
// user must be able to create users; each test works in its own
// database and drops it. Example:
//
//	TOUCHGRASS_TEST_MONGODB_URL=mongodb://root:pass@127.0.0.1:27017/admin \
//	  go test -tags=integration ./internal/dbmigrate/
const mongoURLEnvVar = "TOUCHGRASS_TEST_MONGODB_URL"

// mongoFixture is a throwaway database with a user that can only read
// it, which is the user a migration setup should be given.
type mongoFixture struct {
	admin *mongo.Database
	// readerURL connects as the read-only user.
	readerURL string
}

// integrationDatabase creates an empty database and its read-only user.
func integrationDatabase(t *testing.T) mongoFixture {
	t.Helper()

	base := os.Getenv(mongoURLEnvVar)
	if base == "" {
		t.Skipf("%s is not set", mongoURLEnvVar)
	}

	target, err := parseMongoURL(base)
	if err != nil {
		t.Fatalf("parsing %s: %v", mongoURLEnvVar, err)
	}

	ctx := context.Background()

	client, err := mongo.Connect(options.Client().ApplyURI(base))
	if err != nil {
		t.Fatalf("connecting to the test server: %v", err)
	}

	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("generating a password: %v", err)
	}

	name := fmt.Sprintf("tg_it_%d", time.Now().UnixNano())
	password := hex.EncodeToString(secret)
	database := client.Database(name)

	err = database.RunCommand(ctx, bson.D{
		{Key: "createUser", Value: "reader"},
		{Key: "pwd", Value: password},
		{Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: "read"}, {Key: "db", Value: name}}}},
	}).Err()
	if err != nil {
		t.Fatalf("creating the read-only user: %v", err)
	}

	t.Cleanup(func() {
		if err := database.RunCommand(ctx, bson.D{{Key: "dropAllUsersFromDatabase", Value: 1}}).Err(); err != nil {
			t.Errorf("dropping the test users: %v", err)
		}

		if err := database.Drop(ctx); err != nil {
			t.Errorf("dropping the test database: %v", err)
		}

		if err := client.Disconnect(ctx); err != nil {
			t.Errorf("closing the test connection: %v", err)
		}
	})

	// Write-side options ride along, as they do in an application URL.
	readerURL := fmt.Sprintf("mongodb://reader:%s@%s/%s?retryWrites=true&w=majority",
		password, strings.Join(target.hosts, ","), name)

	return mongoFixture{admin: database, readerURL: readerURL}
}

// changelogDocument is a changelog entry exactly as migrate-mongo 14
// writes it: the hash only when useFileHash is on.
func changelogDocument(fileName, fileHash string, minutes int) bson.D {
	document := bson.D{{Key: "fileName", Value: fileName}}

	if fileHash != "" {
		document = append(document, bson.E{Key: "fileHash", Value: fileHash})
	}

	return append(document,
		bson.E{Key: "appliedAt", Value: at(minutes)},
		bson.E{Key: "migrationBlock", Value: int64(minutes)})
}

func TestMigrateMongoHistoryOfADatabaseItNeverTouched(t *testing.T) {
	t.Parallel()

	fixture := integrationDatabase(t)

	records, err := NewMigrateMongo().History(context.Background(), fixture.readerURL)
	if err != nil {
		t.Fatalf("History() error = %v, want nil", err)
	}

	if len(records) != 0 {
		t.Errorf("History() = %+v, want an empty history", records)
	}
}

func TestMigrateMongoHistoryReadsTheChangelogInAppliedOrder(t *testing.T) {
	t.Parallel()

	fixture := integrationDatabase(t)
	ctx := context.Background()

	// Documents exactly as migrate-mongo 14 writes them: without a hash,
	// with one, and a second row for a file it re-ran after an edit.
	_, err := fixture.admin.Collection(migrateMongoChangelog).InsertMany(ctx, []any{
		changelogDocument(testMongoColour, "edited", 2),
		changelogDocument(testMongoInit, "", 0),
		changelogDocument(testMongoColour, testSumA, 1),
	})
	if err != nil {
		t.Fatalf("writing the changelog: %v", err)
	}

	records, err := NewMigrateMongo().History(ctx, fixture.readerURL)
	if err != nil {
		t.Fatalf("History() error = %v, want nil", err)
	}

	want := []Record{
		{Name: testMongoInit, StartedAt: at(0)},
		{Name: testMongoColour, Checksum: testSumA, StartedAt: at(1)},
		{Name: testMongoColour, Checksum: "edited", StartedAt: at(2)},
	}

	if len(records) != len(want) {
		t.Fatalf("History() = %d records, want %d", len(records), len(want))
	}

	for index, record := range records {
		expected := want[index]

		if record.Name != expected.Name || record.Checksum != expected.Checksum || !record.StartedAt.Equal(expected.StartedAt) {
			t.Errorf("History()[%d] = %+v, want %+v", index, record, expected)
		}

		if record.FinishedAt == nil || !record.FinishedAt.Equal(record.StartedAt) || record.RolledBackAt != nil {
			t.Errorf("History()[%d] = %+v, want applied at one instant", index, record)
		}
	}
}

func TestMigrateMongoHistoryReadsANamedChangelog(t *testing.T) {
	t.Parallel()

	fixture := integrationDatabase(t)
	ctx := context.Background()

	if _, err := fixture.admin.Collection("schema_log").InsertOne(ctx, changelogDocument("elsewhere.js", "", 5)); err != nil {
		t.Fatalf("writing the named changelog: %v", err)
	}

	named, err := NewMigrateMongo().History(ctx, fixture.readerURL+"&changelogCollection=schema_log")
	if err != nil || len(named) != 1 || named[0].Name != "elsewhere.js" {
		t.Errorf("History(named collection) = %+v, %v, want the one row of schema_log", named, err)
	}

	// The default collection of the same database stays empty.
	if records, err := NewMigrateMongo().History(ctx, fixture.readerURL); err != nil || len(records) != 0 {
		t.Errorf("History(default collection) = %+v, %v, want empty", records, err)
	}
}

// The reader only issues a find, so the setup works with a user that
// can do nothing else. This proves the user it ran as cannot write.
func TestMigrateMongoReaderNeedsOnlyTheReadRole(t *testing.T) {
	t.Parallel()

	fixture := integrationDatabase(t)
	ctx := context.Background()

	if _, err := NewMigrateMongo().History(ctx, fixture.readerURL); err != nil {
		t.Fatalf("History() as the read-only user error = %v, want nil", err)
	}

	target, err := parseMongoURL(fixture.readerURL)
	if err != nil {
		t.Fatalf("parsing the reader url: %v", err)
	}

	reader, err := mongo.Connect(options.Client().ApplyURI(target.dsn))
	if err != nil {
		t.Fatalf("connecting as the read-only user: %v", err)
	}

	t.Cleanup(func() {
		if err := reader.Disconnect(ctx); err != nil {
			t.Errorf("closing the reader connection: %v", err)
		}
	})

	_, err = reader.Database(target.database).Collection(migrateMongoChangelog).InsertOne(ctx,
		changelogDocument("forged.js", "", 0))
	if err == nil {
		t.Fatal("InsertOne() as the read-only user succeeded, want it refused")
	}

	records, err := NewMigrateMongo().History(ctx, fixture.readerURL)
	if err != nil || len(records) != 0 {
		t.Errorf("History() after the refused write = %+v, %v, want empty", records, err)
	}
}
