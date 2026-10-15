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

// RedisConfig wires a fixed-command Redis collector.
type RedisConfig struct {
	Runner    Runner
	Container string
	Timeout   time.Duration
}

// Redis collects privacy-safe Redis aggregate statistics.
type Redis struct {
	runner    Runner
	container string
	timeout   time.Duration
}

var redisSections = []string{"clients", "memory", "stats", "persistence", "cpu", "commandstats", "keyspace"}

var redisMetricKinds = map[string]bool{
	"connected_clients": false, "blocked_clients": false, "used_memory": false,
	"used_memory_rss": false, "maxmemory": false, "instantaneous_ops_per_sec": false,
	"rdb_changes_since_last_save": false, "aof_enabled": false, "used_cpu_sys": true,
	"used_cpu_user": true, "total_connections_received": true, "total_commands_processed": true,
	"keyspace_hits": true, "keyspace_misses": true, "evicted_keys": true,
	"expired_keys": true, "rejected_connections": true,
}

// NewRedis builds a Redis collector.
func NewRedis(cfg RedisConfig) *Redis {
	runner := cfg.Runner
	if runner == nil {
		runner = NewCommandRunner()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultSourceTimeout
	}
	return &Redis{runner: runner, container: cfg.Container, timeout: timeout}
}

// Collect reads named INFO sections and SLOWLOG LEN only.
func (r *Redis) Collect(ctx context.Context) (model.RedisSnapshot, error) {
	snapshot := model.RedisSnapshot{SampledAt: time.Now().UTC(), Metrics: map[string]model.NumericSample{}, Commands: map[string]model.RedisCommandSnapshot{}}
	for _, section := range redisSections {
		output, err := r.run(ctx, "INFO", section)
		if err != nil {
			snapshot.Errors = append(snapshot.Errors, "redis_"+section+"_unavailable")
			continue
		}
		if section == "commandstats" {
			commands, parseErr := parseRedisCommands(string(output))
			if parseErr != nil {
				snapshot.Errors = append(snapshot.Errors, "redis_commandstats_invalid")
				continue
			}
			snapshot.Commands = commands
			continue
		}
		metrics, parseErr := parseRedisMetrics(section, string(output))
		if parseErr != nil {
			snapshot.Errors = append(snapshot.Errors, "redis_"+section+"_invalid")
			continue
		}
		for name, sample := range metrics {
			snapshot.Metrics[name] = sample
		}
	}

	output, err := r.run(ctx, "SLOWLOG", "LEN")
	if err != nil {
		snapshot.Errors = append(snapshot.Errors, "redis_slowlog_unavailable")
	} else if value, parseErr := strconv.ParseFloat(strings.TrimSpace(string(output)), 64); parseErr == nil {
		snapshot.Metrics["slowlog_length"] = model.NumericSample{Value: value}
	} else {
		snapshot.Errors = append(snapshot.Errors, "redis_slowlog_invalid")
	}
	if len(snapshot.Metrics) == 0 && len(snapshot.Commands) == 0 {
		return snapshot, errors.New("redis_unavailable")
	}
	return snapshot, nil
}

func (r *Redis) run(ctx context.Context, command ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	args := []string{"exec", r.container, "redis-cli", "--raw"}
	return r.runner.Run(ctx, "docker", append(args, command...)...)
}

func parseRedisMetrics(section, raw string) (map[string]model.NumericSample, error) {
	metrics := map[string]model.NumericSample{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if section == "keyspace" {
			parseRedisKeyspace(line, metrics)
			continue
		}
		name, rawValue, found := strings.Cut(line, ":")
		cumulative, allowed := redisMetricKinds[name]
		if !found || !allowed {
			continue
		}
		value, err := strconv.ParseFloat(rawValue, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid redis numeric metric")
		}
		metrics[name] = model.NumericSample{Value: value, Cumulative: cumulative}
	}
	return metrics, nil
}

func parseRedisKeyspace(line string, metrics map[string]model.NumericSample) {
	_, values, found := strings.Cut(line, ":")
	if !found {
		return
	}
	for _, field := range strings.Split(values, ",") {
		name, rawValue, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(rawValue, 64)
		if err != nil {
			continue
		}
		switch name {
		case "keys":
			current := metrics["keyspace_keys"]
			current.Value += value
			metrics["keyspace_keys"] = current
		case "expires":
			current := metrics["keyspace_expires"]
			current.Value += value
			metrics["keyspace_expires"] = current
		}
	}
}

// RedisCommandSample is a bounded intermediate used by parser tests.
type RedisCommandSample = model.RedisCommandSnapshot

func parseRedisCommands(raw string) (map[string]model.RedisCommandSnapshot, error) {
	commands := map[string]model.RedisCommandSnapshot{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, fields, found := strings.Cut(line, ":")
		if !found || !strings.HasPrefix(name, "cmdstat_") {
			continue
		}
		family := redisCommandFamily(strings.TrimPrefix(name, "cmdstat_"))
		values := map[string]float64{}
		for _, field := range strings.Split(fields, ",") {
			key, rawValue, ok := strings.Cut(field, "=")
			if !ok || (key != "calls" && key != "usec") {
				continue
			}
			value, err := strconv.ParseFloat(rawValue, 64)
			if err != nil {
				return nil, errors.New("invalid redis command numeric value")
			}
			values[key] = value
		}
		current := commands[family]
		current.Calls += values["calls"]
		current.Microseconds += values["usec"]
		commands[family] = current
	}
	return commands, nil
}

func redisCommandFamily(command string) string {
	switch command {
	case "get", "mget", "exists", "ttl", "pttl", "scan", "hget", "hmget", "hgetall", "lrange", "smembers", "zrange":
		return "read"
	case "set", "mset", "del", "unlink", "expire", "pexpire", "incr", "decr", "hset", "lpush", "rpush", "sadd", "zadd":
		return "write"
	case "eval", "evalsha", "fcall":
		return "script"
	case "publish", "subscribe", "psubscribe":
		return "pubsub"
	case "multi", "exec", "discard", "watch":
		return "transaction"
	default:
		return "other"
	}
}
