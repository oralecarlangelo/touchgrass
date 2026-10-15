#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
timing_conf="$repo_dir/deploy/nginx/timing-log.conf"
route_map="$repo_dir/deploy/nginx/route-families.map"
binary="$repo_dir/bin/touchgrass"
port=${TOUCHGRASS_SOAK_PORT:-18089}
base_url="http://127.0.0.1:$port"
email_fixture="privacy-soak-alice@example.invalid"
token_fixture="tg-secret-soak-token-9482"
query_fixture="private-query-soak-7319"
max_cpu=${TOUCHGRASS_SOAK_MAX_CPU_PERCENT:-5}
max_growth=${TOUCHGRASS_SOAK_MAX_DB_GROWTH_BYTES:-8388608}
max_api_ms=${TOUCHGRASS_SOAK_MAX_API_P95_MS:-500}
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/touchgrass-performance-soak.XXXXXX")
db_path="$work_dir/touchgrass.db"
timing_log="$work_dir/requests.jsonl"
server_log="$work_dir/touchgrass.log"
cookie_jar="$work_dir/cookies"
server_pid=''

cleanup() {
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

fail() {
  echo "performance soak: FAIL: $*" >&2
  exit 1
}

file_size() {
  stat -f '%z' "$1" 2>/dev/null || stat -c '%s' "$1"
}

stop_server() {
  if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
    kill "$server_pid"
    wait "$server_pid" 2>/dev/null || true
  fi
  server_pid=''
}

wait_for_health() {
  attempts=0
  until curl -fsS "$base_url/api/health" >/dev/null 2>&1; do
    attempts=$((attempts + 1))
    if [ "$attempts" -ge 100 ]; then
      tail -40 "$server_log" >&2 || true
      fail "server did not become healthy"
    fi
    sleep 0.1
  done
}

start_server() {
  mode=$1
  : >"$server_log"
  if [ "$mode" = performance ]; then
    TOUCHGRASS_NGINX_TIMING_LOG="$timing_log" \
      TOUCHGRASS_PERFORMANCE_SERVICE_ID="soak-api" \
      TOUCHGRASS_PERFORMANCE_INTERVAL="100ms" \
      TOUCHGRASS_ADDR="127.0.0.1:$port" \
      TOUCHGRASS_DB="$db_path" \
      TOUCHGRASS_ADMIN_PASSWORD="performance-soak-password" \
      "$binary" serve >"$server_log" 2>&1 &
  else
    TOUCHGRASS_ADDR="127.0.0.1:$port" \
      TOUCHGRASS_DB="$db_path" \
      TOUCHGRASS_ADMIN_PASSWORD="performance-soak-password" \
      "$binary" serve >"$server_log" 2>&1 &
  fi
  server_pid=$!
  wait_for_health
}

sample_cpu() {
  pid=$1
  samples="$work_dir/cpu"
  : >"$samples"
  count=0
  while [ "$count" -lt 8 ]; do
    ps -p "$pid" -o %cpu= | awk '{$1=$1; print}' >>"$samples"
    count=$((count + 1))
    sleep 0.25
  done
  awk '{sum += $1; count += 1} END {if (count == 0) print "0"; else printf "%.2f", sum/count}' "$samples"
}

[ -f "$timing_conf" ] || fail "missing deploy/nginx/timing-log.conf"
[ -f "$route_map" ] || fail "missing deploy/nginx/route-families.map"
[ -x "$binary" ] || fail "missing bin/touchgrass (run make build first)"
command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v sqlite3 >/dev/null 2>&1 || fail "sqlite3 is required"

for forbidden in '\$request_uri' '\$args' '\$http_' '\$remote_addr' '\$remote_user' '\$cookie_' '\$request[ ;,}]'; do
  if grep -En "$forbidden" "$timing_conf" "$route_map" >/dev/null; then
    fail "privacy-forbidden Nginx variable matched: $forbidden"
  fi
done

start_server baseline
baseline_cpu=$(sample_cpu "$server_pid")
stop_server

sqlite3 "$db_path" <<'SQL'
INSERT INTO services (id, name, strategy, compose_project, compose_dir, config)
VALUES ('soak-api', 'Performance soak API', 'recreate', 'soak', '/tmp', '{"container":"soak-api"}');
SQL

timestamp=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
: >"$timing_log"
index=0
while [ "$index" -lt 500 ]; do
  printf '%s\n' "{\"timestamp\":\"$timestamp\",\"request_id\":\"soak-$index\",\"method\":\"GET\",\"status\":\"200\",\"route_family\":\"public_events\",\"request_time\":\"0.025\",\"upstream_connect_time\":\"0.001\",\"upstream_header_time\":\"0.010\",\"upstream_response_time\":\"0.024\",\"upstream_status\":\"200\",\"upstream_addr\":\"api:3000\",\"bytes_sent\":\"512\",\"request_length\":\"120\",\"connection_requests\":\"1\"}" >>"$timing_log"
  index=$((index + 1))
done

size_before=$(file_size "$db_path")
start_server performance
curl -fsS -c "$cookie_jar" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"performance-soak-password"}' \
  "$base_url/api/auth/login" >/dev/null

curl -sS "$base_url/privacy/$email_fixture?token=$token_fixture&search=$query_fixture" >/dev/null
sleep 1
performance_cpu=$(sample_cpu "$server_pid")
request_total=$(sqlite3 "$db_path" "SELECT COALESCE(SUM(request_count), 0) FROM http_metric_buckets WHERE service_id = 'soak-api';")
[ "$request_total" -ge 500 ] || fail "collector stored $request_total of 500 fixture requests"

api_times="$work_dir/api-times"
: >"$api_times"
index=0
while [ "$index" -lt 20 ]; do
  curl -fsS -b "$cookie_jar" -o /dev/null -w '%{time_total}\n' \
    "$base_url/api/services/soak-api/performance/summary" >>"$api_times"
  index=$((index + 1))
done
api_p95_ms=$(sort -n "$api_times" | awk 'NR == 19 {printf "%.2f", $1 * 1000}')

stop_server
size_after=$(file_size "$db_path")
growth=$((size_after - size_before))
cpu_overhead=$(awk -v baseline="$baseline_cpu" -v enabled="$performance_cpu" 'BEGIN {value=enabled-baseline; if (value < 0) value=0; printf "%.2f", value}')

for fixture in "$email_fixture" "$token_fixture" "$query_fixture"; do
  if grep -aF "$fixture" "$db_path" "$server_log" >/dev/null 2>&1; then
    fail "sensitive fixture persisted: $fixture"
  fi
done

awk -v value="$cpu_overhead" -v limit="$max_cpu" 'BEGIN {exit !(value < limit)}' || \
  fail "collector CPU overhead ${cpu_overhead}% is not under ${max_cpu}%"
[ "$growth" -le "$max_growth" ] || fail "SQLite growth $growth exceeds $max_growth bytes"
awk -v value="$api_p95_ms" -v limit="$max_api_ms" 'BEGIN {exit !(value <= limit)}' || \
  fail "performance API p95 ${api_p95_ms}ms exceeds ${max_api_ms}ms"

echo "performance soak: PASS"
echo "  collector CPU overhead: ${cpu_overhead}% (< ${max_cpu}%)"
echo "  SQLite growth: ${growth} bytes (<= ${max_growth})"
echo "  performance API p95: ${api_p95_ms}ms (<= ${max_api_ms}ms)"
echo "  timing observations stored: ${request_total}"
echo "  sensitive fixture matches: 0"
