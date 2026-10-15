package dbstats

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPostgresCollectsBoundedNumericStatistics(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{answers: map[string]stubAnswer{
		"database":   {output: "xact_commit\t120\nxact_rollback\t3\nblks_read\t40\nblks_hit\t900\nstats_reset_epoch\t1700000000\n"},
		"activity":   {output: "connections_total\t12\nconnections_active\t3\nconnections_waiting\t1\nmax_connections\t100\n"},
		"tables":     {output: "table_seq_scan\t5\ntable_idx_scan\t60\ndead_tuples\t7\n"},
		"indexes":    {output: "index_scans\t60\nindex_tuples_read\t240\n"},
		"bgwriter":   {output: "checkpoints_timed\t9\nbuffers_checkpoint\t22\n"},
		"wal":        {output: "wal_records\t1000\nwal_fpi\t10\nwal_bytes\t4096\n"},
		"statements": {output: "42\t10\t125.5\t20\t80\t4\t0\t1\n"},
	}}
	collector := NewPostgres(PostgresConfig{
		Runner: runner, Container: "postgres-prod", User: "postgres", Database: "ticketnation",
		Timeout: time.Second,
	})

	snapshot, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Metrics["xact_commit"].Value != 120 || !snapshot.Metrics["xact_commit"].Cumulative {
		t.Fatalf("xact_commit = %+v", snapshot.Metrics["xact_commit"])
	}
	if snapshot.Metrics["connections_active"].Value != 3 || snapshot.Metrics["connections_active"].Cumulative {
		t.Fatalf("connections_active = %+v", snapshot.Metrics["connections_active"])
	}
	if got := snapshot.Statements["42"]; got.Calls != 10 || got.TotalExecMS != 125.5 || got.TempBlocksWritten != 1 {
		t.Fatalf("statement = %+v", got)
	}
	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		if strings.Contains(strings.ToLower(joined), "query ") || strings.Contains(joined, "SELECT query,") {
			t.Fatalf("command exposes raw query text: %q", joined)
		}
		if call.name != "docker" || len(call.args) < 8 || !reflect.DeepEqual(call.args[:3], []string{"exec", "postgres-prod", "psql"}) {
			t.Fatalf("unexpected command: %s %v", call.name, call.args)
		}
	}
}

func TestPostgresOptionalViewsDegradeIndependently(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{answers: map[string]stubAnswer{
		"database": {output: "xact_commit\t12\n"},
		"activity": {output: "connections_total\t2\n"},
		"tables":   {output: "table_seq_scan\t1\n"},
		"indexes":  {output: "index_scans\t1\n"},
		"bgwriter": {output: "checkpoints_timed\t1\n"},
		"wal":      {err: errors.New("relation pg_stat_wal does not exist")},
		"statements": {err: errors.New(
			"relation pg_stat_statements does not exist; SELECT password FROM users",
		)},
	}}
	snapshot, err := NewPostgres(PostgresConfig{
		Runner: runner, Container: "postgres-prod", User: "postgres", Database: "ticketnation",
	}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot.Errors, []string{"postgres_wal_unavailable", "postgres_statements_unavailable"}) {
		t.Fatalf("errors = %v", snapshot.Errors)
	}
	for _, category := range snapshot.Errors {
		if strings.Contains(category, "SELECT") || strings.Contains(category, "password") {
			t.Fatalf("unsafe error category: %q", category)
		}
	}
}

func TestCounterDeltaStartsNewBaselineAfterReset(t *testing.T) {
	t.Parallel()

	if delta, reset, valid := CounterDelta(120, 100, true); delta != 20 || reset || !valid {
		t.Fatalf("normal delta = %v, %v, %v", delta, reset, valid)
	}
	if delta, reset, valid := CounterDelta(5, 120, true); delta != 0 || !reset || valid {
		t.Fatalf("reset delta = %v, %v, %v", delta, reset, valid)
	}
	if delta, reset, valid := CounterDelta(5, 0, false); delta != 0 || reset || valid {
		t.Fatalf("initial delta = %v, %v, %v", delta, reset, valid)
	}
}
