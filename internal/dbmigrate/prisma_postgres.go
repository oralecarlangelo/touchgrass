package dbmigrate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// prismaHistoryTable is the table Prisma Migrate keeps its record in.
	prismaHistoryTable = "_prisma_migrations"
	// postgresDefaultSchema is Prisma's schema when the URL names none.
	postgresDefaultSchema = "public"
	// postgresDefaultPort is used when the URL names no port.
	postgresDefaultPort = "5432"
	// postgresUndefinedTable is the SQLSTATE for a missing relation.
	postgresUndefinedTable = "42P01"
	// postgresApplicationName tags the connection in pg_stat_activity.
	postgresApplicationName = "touchgrass"
	// prismaHistoryLimit bounds one read of the history table.
	prismaHistoryLimit = 5000
)

// postgresParams are the URL parameters passed on to the driver. Every
// other parameter is dropped: Prisma URLs carry its own settings
// (schema, connection_limit, pgbouncer, ...) that PostgreSQL rejects
// as unknown startup parameters.
var postgresParams = []string{"sslmode", "sslrootcert", "sslcert", "sslkey", "connect_timeout"}

// postgresTarget is a PostgreSQL URL reduced to what the reader needs.
type postgresTarget struct {
	// dsn is the URL with only driver parameters left.
	dsn      string
	host     string
	port     string
	database string
	schema   string
}

// label is the credential-free description of the target.
func (t postgresTarget) label() string {
	return fmt.Sprintf("%s/%s (schema %s)", net.JoinHostPort(t.host, t.port), t.database, t.schema)
}

// parsePostgresURL validates a Prisma-style PostgreSQL URL. The error
// never echoes the URL, which holds a password.
func parsePostgresURL(databaseURL string) (postgresTarget, error) {
	parsed, err := url.Parse(strings.TrimSpace(databaseURL))
	if err != nil {
		return postgresTarget{}, fmt.Errorf("%w: it does not parse as a url", ErrInvalidURL)
	}

	switch parsed.Scheme {
	case "postgres", "postgresql":
	case "":
		return postgresTarget{}, fmt.Errorf("%w: it has no scheme", ErrInvalidURL)
	default:
		return postgresTarget{}, fmt.Errorf("%w: scheme %q", ErrUnsupportedDatabase, parsed.Scheme)
	}

	database := strings.TrimPrefix(parsed.Path, "/")
	if parsed.Hostname() == "" || database == "" {
		return postgresTarget{}, fmt.Errorf("%w: it needs a host and a database name", ErrInvalidURL)
	}

	query := parsed.Query()

	schema := query.Get("schema")
	if schema == "" {
		schema = postgresDefaultSchema
	}

	port := parsed.Port()
	if port == "" {
		port = postgresDefaultPort
	}

	kept := url.Values{}

	for _, param := range postgresParams {
		if value := query.Get(param); value != "" {
			kept.Set(param, value)
		}
	}

	clean := *parsed
	clean.RawQuery = kept.Encode()
	clean.Fragment = ""

	return postgresTarget{
		dsn:      clean.String(),
		host:     parsed.Hostname(),
		port:     port,
		database: database,
		schema:   schema,
	}, nil
}

// readPrismaPostgres reads the Prisma history table over one
// short-lived, read-only connection. A database Prisma has never
// migrated has no table, which reads as an empty history.
func readPrismaPostgres(ctx context.Context, target postgresTarget) (records []Record, err error) {
	config, err := pgx.ParseConfig(target.dsn)
	if err != nil {
		// The driver's message can quote the URL; keep it out.
		return nil, fmt.Errorf("%w: the driver rejected it", ErrInvalidURL)
	}

	// The simple protocol sends no prepared statement, so the read
	// also works through a transaction-pooling proxy.
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	config.RuntimeParams["application_name"] = postgresApplicationName

	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}

	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()

		err = errors.Join(err, conn.Close(closeCtx))
	}()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("starting read-only transaction: %w", err)
	}

	defer func() {
		// A read-only transaction has nothing to keep; roll back always.
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, fmt.Errorf("ending read-only transaction: %w", rollbackErr))
		}
	}()

	table := pgx.Identifier{target.schema, prismaHistoryTable}.Sanitize()

	rows, err := tx.Query(ctx, fmt.Sprintf(
		`SELECT migration_name, checksum, started_at, finished_at, rolled_back_at,
		        applied_steps_count, COALESCE(logs, '')
		   FROM %s
		  ORDER BY started_at, migration_name
		  LIMIT %d`, table, prismaHistoryLimit))
	if err != nil {
		return nil, missingTableOr(err)
	}

	records, err = pgx.CollectRows(rows, scanPrismaRecord)
	if err != nil {
		return nil, missingTableOr(err)
	}

	return records, nil
}

// scanPrismaRecord maps one history row.
func scanPrismaRecord(row pgx.CollectableRow) (Record, error) {
	var record Record

	err := row.Scan(
		&record.Name,
		&record.Checksum,
		&record.StartedAt,
		&record.FinishedAt,
		&record.RolledBackAt,
		&record.Steps,
		&record.Logs,
	)
	if err != nil {
		return Record{}, fmt.Errorf("scanning history row: %w", err)
	}

	return record, nil
}

// missingTableOr turns "relation does not exist" into an empty
// history and passes every other error on.
func missingTableOr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == postgresUndefinedTable {
		return nil
	}

	return fmt.Errorf("reading prisma history: %w", err)
}
