package dbstats

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type stubAnswer struct {
	output string
	err    error
}

type runnerCall struct {
	name string
	args []string
}

type stubRunner struct {
	mu      sync.Mutex
	answers map[string]stubAnswer
	calls   []runnerCall
}

func (s *stubRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, runnerCall{name: name, args: append([]string{}, args...)})
	key := args[len(args)-1]
	for candidate := range s.answers {
		if strings.Contains(key, "touchgrass:"+candidate) {
			key = candidate
			break
		}
	}
	answer := s.answers[key]
	return []byte(answer.output), answer.err
}

func TestRedisCollectsAllowlistedAggregateStatistics(t *testing.T) {
	t.Parallel()

	runner := &stubRunner{answers: map[string]stubAnswer{
		"clients":      {output: "# Clients\r\nconnected_clients:8\r\nblocked_clients:1\r\nclient_recent_max_input_buffer:secret\r\n"},
		"memory":       {output: "used_memory:1024\r\nused_memory_rss:2048\r\nmaxmemory:4096\r\n"},
		"stats":        {output: "total_connections_received:50\r\ntotal_commands_processed:500\r\ninstantaneous_ops_per_sec:9\r\nkeyspace_hits:400\r\nkeyspace_misses:20\r\nevicted_keys:3\r\nexpired_keys:5\r\nrejected_connections:1\r\n"},
		"persistence":  {output: "rdb_changes_since_last_save:4\r\nrdb_last_bgsave_status:ok\r\naof_enabled:0\r\n"},
		"cpu":          {output: "used_cpu_sys:1.25\r\nused_cpu_user:2.5\r\n"},
		"commandstats": {output: "cmdstat_get:calls=10,usec=20,usec_per_call=2\r\ncmdstat_evalsha:calls=3,usec=30,usec_per_call=10\r\ncmdstat_customsecret:calls=2,usec=7,usec_per_call=3.5\r\n"},
		"keyspace":     {output: "db0:keys=12,expires=4,avg_ttl=60000\r\n"},
		"LEN":          {output: "6\n"},
	}}
	snapshot, err := NewRedis(RedisConfig{Runner: runner, Container: "redis-prod"}).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Metrics["connected_clients"].Value != 8 || snapshot.Metrics["used_memory"].Value != 1024 {
		t.Fatalf("metrics = %+v", snapshot.Metrics)
	}
	if snapshot.Commands["read"].Calls != 10 || snapshot.Commands["script"].Calls != 3 || snapshot.Commands["other"].Calls != 2 {
		t.Fatalf("commands = %+v", snapshot.Commands)
	}
	if snapshot.Metrics["keyspace_keys"].Value != 12 || snapshot.Metrics["slowlog_length"].Value != 6 {
		t.Fatalf("keyspace/slowlog metrics = %+v", snapshot.Metrics)
	}
	if _, ok := snapshot.Metrics["client_recent_max_input_buffer"]; ok {
		t.Fatal("non-allowlisted client detail was retained")
	}
	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		if strings.Contains(joined, "KEYS") || strings.Contains(joined, "MONITOR") || strings.Contains(joined, "CLIENT LIST") {
			t.Fatalf("unsafe redis command: %q", joined)
		}
		if call.name != "docker" || !reflect.DeepEqual(call.args[:3], []string{"exec", "redis-prod", "redis-cli"}) {
			t.Fatalf("unexpected command: %s %v", call.name, call.args)
		}
	}
}

func TestRedisUnknownCommandsAreBoundedAndDoNotExposeNames(t *testing.T) {
	t.Parallel()

	commands, err := parseRedisCommands("cmdstat_customer_email_lookup:calls=4,usec=8,usec_per_call=2\n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(commands, map[string]RedisCommandSample{"other": {Calls: 4, Microseconds: 8}}) {
		t.Fatalf("commands = %+v", commands)
	}
	if _, exists := commands["customer_email_lookup"]; exists {
		t.Fatal("raw command name was retained")
	}
}
