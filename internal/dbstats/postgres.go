package dbstats

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const defaultSourceTimeout = 5 * time.Second

// PostgresConfig wires a fixed-query PostgreSQL collector.
type PostgresConfig struct {
	Runner    Runner
	Container string
	User      string
	Database  string
	Timeout   time.Duration
}

// Postgres collects privacy-safe PostgreSQL aggregate statistics.
type Postgres struct {
	runner  Runner
	args    []string
	timeout time.Duration
}

type postgresQuery struct {
	category string
	query    string
	required bool
}

var postgresMetricQueries = []postgresQuery{
	{category: "database", required: true, query: `/* touchgrass:database */ SELECT metric, value FROM (SELECT 'xact_commit' metric, xact_commit::numeric value FROM pg_stat_database WHERE datname=current_database() UNION ALL SELECT 'xact_rollback', xact_rollback FROM pg_stat_database WHERE datname=current_database() UNION ALL SELECT 'blks_read', blks_read FROM pg_stat_database WHERE datname=current_database() UNION ALL SELECT 'blks_hit', blks_hit FROM pg_stat_database WHERE datname=current_database() UNION ALL SELECT 'temp_files', temp_files FROM pg_stat_database WHERE datname=current_database() UNION ALL SELECT 'temp_bytes', temp_bytes FROM pg_stat_database WHERE datname=current_database() UNION ALL SELECT 'deadlocks', deadlocks FROM pg_stat_database WHERE datname=current_database() UNION ALL SELECT 'stats_reset_epoch', COALESCE(EXTRACT(EPOCH FROM stats_reset),0) FROM pg_stat_database WHERE datname=current_database()) bounded`},
	{category: "activity", query: `/* touchgrass:activity */ SELECT metric, value FROM (SELECT 'connections_total' metric, count(*)::numeric value FROM pg_stat_activity UNION ALL SELECT 'connections_active', count(*) FROM pg_stat_activity WHERE state='active' UNION ALL SELECT 'connections_waiting', count(*) FROM pg_stat_activity WHERE wait_event IS NOT NULL UNION ALL SELECT 'connections_idle_in_transaction', count(*) FROM pg_stat_activity WHERE state='idle in transaction' UNION ALL SELECT 'max_connections', setting::numeric FROM pg_settings WHERE name='max_connections') bounded`},
	{category: "tables", query: `/* touchgrass:tables */ SELECT metric, value FROM (SELECT 'table_seq_scan' metric, COALESCE(sum(seq_scan),0)::numeric value FROM pg_stat_user_tables UNION ALL SELECT 'table_idx_scan', COALESCE(sum(idx_scan),0) FROM pg_stat_user_tables UNION ALL SELECT 'live_tuples', COALESCE(sum(n_live_tup),0) FROM pg_stat_user_tables UNION ALL SELECT 'dead_tuples', COALESCE(sum(n_dead_tup),0) FROM pg_stat_user_tables) bounded`},
	{category: "indexes", query: `/* touchgrass:indexes */ SELECT metric, value FROM (SELECT 'index_scans' metric, COALESCE(sum(idx_scan),0)::numeric value FROM pg_stat_user_indexes UNION ALL SELECT 'index_tuples_read', COALESCE(sum(idx_tup_read),0) FROM pg_stat_user_indexes UNION ALL SELECT 'index_tuples_fetched', COALESCE(sum(idx_tup_fetch),0) FROM pg_stat_user_indexes) bounded`},
	{category: "bgwriter", query: `/* touchgrass:bgwriter */ SELECT metric, value FROM (SELECT 'checkpoints_timed' metric, checkpoints_timed::numeric value FROM pg_stat_bgwriter UNION ALL SELECT 'checkpoints_requested', checkpoints_req FROM pg_stat_bgwriter UNION ALL SELECT 'buffers_checkpoint', buffers_checkpoint FROM pg_stat_bgwriter UNION ALL SELECT 'buffers_clean', buffers_clean FROM pg_stat_bgwriter UNION ALL SELECT 'buffers_backend', buffers_backend FROM pg_stat_bgwriter) bounded`},
	{category: "wal", query: `/* touchgrass:wal */ SELECT metric, value FROM (SELECT 'wal_records' metric, wal_records::numeric value FROM pg_stat_wal UNION ALL SELECT 'wal_fpi', wal_fpi FROM pg_stat_wal UNION ALL SELECT 'wal_bytes', wal_bytes FROM pg_stat_wal) bounded`},
}

const postgresStatementsQuery = `/* touchgrass:statements */ SELECT queryid, calls, total_exec_time, rows, shared_blks_hit, shared_blks_read, temp_blks_read, temp_blks_written FROM pg_stat_statements ORDER BY total_exec_time DESC LIMIT 50`

var postgresCumulative = map[string]bool{
	"xact_commit": true, "xact_rollback": true, "blks_read": true, "blks_hit": true,
	"temp_files": true, "temp_bytes": true, "deadlocks": true, "table_seq_scan": true,
	"table_idx_scan": true, "index_scans": true, "index_tuples_read": true,
	"index_tuples_fetched": true, "checkpoints_timed": true, "checkpoints_requested": true,
	"buffers_checkpoint": true, "buffers_clean": true, "buffers_backend": true,
	"wal_records": true, "wal_fpi": true, "wal_bytes": true,
}

// NewPostgres builds a PostgreSQL collector. Configuration values are passed
// as individual docker arguments and never through a shell.
func NewPostgres(cfg PostgresConfig) *Postgres {
	runner := cfg.Runner
	if runner == nil {
		runner = NewCommandRunner()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultSourceTimeout
	}
	return &Postgres{runner: runner, timeout: timeout, args: []string{
		"exec", cfg.Container, "psql", "-X", "-A", "-t", "-F", "\t", "-U", cfg.User, "-d", cfg.Database, "-c",
	}}
}

// Collect reads all supported aggregate views. Optional view failures are
// reduced to safe categories and do not discard healthy metrics.
func (p *Postgres) Collect(ctx context.Context) (model.PostgresSnapshot, error) {
	snapshot := model.PostgresSnapshot{SampledAt: time.Now().UTC(), Metrics: map[string]model.NumericSample{}, Statements: map[string]model.PostgresStatementSnapshot{}}
	for _, query := range postgresMetricQueries {
		output, err := p.query(ctx, query.query)
		if err != nil {
			if query.required {
				return snapshot, fmt.Errorf("postgres_%s_unavailable", query.category)
			}
			snapshot.Errors = append(snapshot.Errors, "postgres_"+query.category+"_unavailable")
			continue
		}
		metrics, err := parsePostgresMetrics(string(output))
		if err != nil {
			if query.required {
				return snapshot, fmt.Errorf("postgres_%s_invalid", query.category)
			}
			snapshot.Errors = append(snapshot.Errors, "postgres_"+query.category+"_invalid")
			continue
		}
		for name, value := range metrics {
			snapshot.Metrics[name] = model.NumericSample{Value: value, Cumulative: postgresCumulative[name]}
		}
	}

	output, err := p.query(ctx, postgresStatementsQuery)
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "postgres_statements_unavailable")
		return snapshot, nil
	}
	statements, err := parsePostgresStatements(string(output))
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "postgres_statements_invalid")
		return snapshot, nil
	}
	snapshot.Statements = statements
	return snapshot, nil
}

func (p *Postgres) query(ctx context.Context, query string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.runner.Run(ctx, "docker", append(append([]string{}, p.args...), query)...)
}

func parsePostgresMetrics(raw string) (map[string]float64, error) {
	metrics := map[string]float64{}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 2 || (!postgresCumulative[parts[0]] && !postgresGauge(parts[0])) {
			return nil, errors.New("invalid bounded postgres metric")
		}
		value, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return nil, errors.New("invalid postgres numeric value")
		}
		metrics[parts[0]] = value
	}
	return metrics, nil
}

func postgresGauge(name string) bool {
	switch name {
	case "stats_reset_epoch", "connections_total", "connections_active", "connections_waiting", "connections_idle_in_transaction", "max_connections", "live_tuples", "dead_tuples":
		return true
	default:
		return false
	}
}

func parsePostgresStatements(raw string) (map[string]model.PostgresStatementSnapshot, error) {
	statements := map[string]model.PostgresStatementSnapshot{}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 8 {
			return nil, errors.New("invalid bounded postgres statement")
		}
		values := make([]float64, 7)
		for index := range values {
			value, err := strconv.ParseFloat(parts[index+1], 64)
			if err != nil {
				return nil, errors.New("invalid postgres statement numeric value")
			}
			values[index] = value
		}
		statements[parts[0]] = model.PostgresStatementSnapshot{Calls: values[0], TotalExecMS: values[1], Rows: values[2], SharedBlocksHit: values[3], SharedBlocksRead: values[4], TempBlocksRead: values[5], TempBlocksWritten: values[6]}
	}
	return statements, nil
}
