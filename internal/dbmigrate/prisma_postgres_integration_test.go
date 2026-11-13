//go:build integration

package dbmigrate

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// postgresURLEnvVar names a PostgreSQL the reader is exercised against.
// The role needs to create a schema; the test works in its own and
// drops it. Example:
//
//	TOUCHGRASS_TEST_POSTGRES_URL=postgres://user:pass@127.0.0.1:5432/db \
//	  go test -tags=integration ./internal/dbmigrate/
const postgresURLEnvVar = "TOUCHGRASS_TEST_POSTGRES_URL"

// prismaHistoryDDL is the table exactly as Prisma Migrate creates it.
const prismaHistoryDDL = `CREATE TABLE %s (
	id                  VARCHAR(36) PRIMARY KEY NOT NULL,
	checksum            VARCHAR(64) NOT NULL,
	finished_at         TIMESTAMPTZ,
	migration_name      VARCHAR(255) NOT NULL,
	logs                TEXT,
	rolled_back_at      TIMESTAMPTZ,
	started_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
	applied_steps_count INTEGER NOT NULL DEFAULT 0
)`

// integrationSchema creates an empty schema for one test and returns
// its name with a Prisma-style URL that points at it.
func integrationSchema(t *testing.T) (schema, prismaURL string, admin *pgx.Conn) {
	t.Helper()

	base := os.Getenv(postgresURLEnvVar)
	if base == "" {
		t.Skipf("%s is not set", postgresURLEnvVar)
	}

	ctx := context.Background()

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connecting to the test database: %v", err)
	}

	schema = fmt.Sprintf("tg_it_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()

	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatalf("creating the test schema: %v", err)
	}

	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("dropping the test schema: %v", err)
		}

		if err := admin.Close(ctx); err != nil {
			t.Errorf("closing the test connection: %v", err)
		}
	})

	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parsing %s: %v", postgresURLEnvVar, err)
	}

	// Prisma's own parameters ride along, as they do in a real URL.
	query := parsed.Query()
	query.Set("schema", schema)
	query.Set("connection_limit", "3")
	query.Set("pgbouncer", "true")
	parsed.RawQuery = query.Encode()

	return schema, parsed.String(), admin
}

func TestPrismaHistoryOfADatabasePrismaNeverTouched(t *testing.T) {
	t.Parallel()

	_, prismaURL, _ := integrationSchema(t)

	records, err := NewPrisma().History(context.Background(), prismaURL)
	if err != nil {
		t.Fatalf("History() error = %v, want nil", err)
	}

	if len(records) != 0 {
		t.Errorf("History() = %+v, want an empty history", records)
	}
}

func TestPrismaHistoryReadsEveryAttemptInStartOrder(t *testing.T) {
	t.Parallel()

	schema, prismaURL, admin := integrationSchema(t)
	ctx := context.Background()
	table := pgx.Identifier{schema, prismaHistoryTable}.Sanitize()

	if _, err := admin.Exec(ctx, fmt.Sprintf(prismaHistoryDDL, table)); err != nil {
		t.Fatalf("creating the history table: %v", err)
	}

	insert := "INSERT INTO " + table +
		" (id, checksum, migration_name, started_at, finished_at, rolled_back_at, logs, applied_steps_count)" +
		" VALUES ($1, $2, $3, $4, $5, $6, $7, $8)"
	start := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	done := start.Add(time.Second)
	undone := start.Add(3 * time.Minute)

	rows := [][]any{
		{"a", "sum-init", "20261101100000_init", start, &done, nil, nil, 1},
		{"b", "sum-colour", "20261102090000_add_colour", start.Add(time.Minute), nil, &undone, "column exists", 0},
		{"c", "sum-colour", "20261102090000_add_colour", start.Add(4 * time.Minute), nil, nil, "still broken", 0},
	}

	for _, row := range rows {
		if _, err := admin.Exec(ctx, insert, row...); err != nil {
			t.Fatalf("inserting a history row: %v", err)
		}
	}

	records, err := NewPrisma().History(ctx, prismaURL)
	if err != nil {
		t.Fatalf("History() error = %v, want nil", err)
	}

	if len(records) != 3 {
		t.Fatalf("History() returned %d rows, want 3", len(records))
	}

	wantHistoryRows(t, records)

	migrations := Compare(records, nil, false)
	if len(migrations) != 2 || migrations[1].Attempts != 2 || migrations[1].State != "failed" {
		t.Errorf("Compare() = %+v, want init applied and add_colour failed after 2 attempts", migrations)
	}
}

// wantHistoryRows checks the three inserted attempts, in start order.
func wantHistoryRows(t *testing.T, records []Record) {
	t.Helper()

	applied, undoneRow, failed := records[0], records[1], records[2]

	if applied.Name != "20261101100000_init" || applied.Checksum != "sum-init" || applied.FinishedAt == nil || applied.Steps != 1 {
		t.Errorf("first row = %+v, want the applied init migration", applied)
	}

	if undoneRow.RolledBackAt == nil || undoneRow.Logs != "column exists" {
		t.Errorf("second row = %+v, want the rolled-back attempt with its logs", undoneRow)
	}

	if failed.FinishedAt != nil || failed.RolledBackAt != nil || failed.Logs != "still broken" {
		t.Errorf("third row = %+v, want the unresolved failed attempt", failed)
	}
}

// The reader's transaction is read-only: even a role that may write
// cannot write through it.
func TestPrismaReaderCannotWrite(t *testing.T) {
	t.Parallel()

	_, prismaURL, _ := integrationSchema(t)
	ctx := context.Background()

	target, err := parsePostgresURL(prismaURL)
	if err != nil {
		t.Fatalf("parsePostgresURL() error = %v, want nil", err)
	}

	conn, err := pgx.Connect(ctx, target.dsn)
	if err != nil {
		t.Fatalf("connecting with the reader's dsn: %v", err)
	}

	defer func() {
		if err := conn.Close(ctx); err != nil {
			t.Errorf("closing the connection: %v", err)
		}
	}()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}

	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Errorf("Rollback() error = %v, want nil", err)
		}
	}()

	_, err = tx.Exec(ctx, "CREATE TABLE "+pgx.Identifier{target.schema, "should_not_exist"}.Sanitize()+" (id INT)")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("writing in the reader's transaction error = %v, want a read-only refusal", err)
	}
}
