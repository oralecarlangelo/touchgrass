#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
count=${TOUCHGRASS_SECURITY_SOAK_CONTAINERS:-100}
max_cpu=${TOUCHGRASS_SECURITY_SOAK_MAX_CPU_PERCENT:-2}
max_rss_growth_kib=${TOUCHGRASS_SECURITY_SOAK_MAX_RSS_GROWTH_KIB:-65536}
simulated_window_seconds=150
fixture_image=${TOUCHGRASS_SECURITY_SOAK_IMAGE:-busybox:1.36}
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/touchgrass-security-soak.XXXXXX")
test_binary="$work_dir/security-soak.test"
label_key="com.touchgrass.security-soak"
run_id="run-$$"
label="$label_key=$run_id"
docker_mode=0

fail() {
  echo "container security soak: FAIL: $*" >&2
  exit 1
}

cleanup() {
  if [ "$docker_mode" -eq 1 ]; then
    docker ps -aq --filter "label=$label" 2>/dev/null | while IFS= read -r container_id; do
      [ -n "$container_id" ] && docker rm -f "$container_id" >/dev/null 2>&1 || true
    done
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

case "$count" in
  ''|*[!0-9]*) fail "TOUCHGRASS_SECURITY_SOAK_CONTAINERS must be an integer" ;;
esac
[ "$count" -ge 1 ] && [ "$count" -le 100 ] || fail "container count must be between 1 and 100"
command -v go >/dev/null 2>&1 || fail "go is required"
command -v ps >/dev/null 2>&1 || fail "ps is required"

cd "$repo_dir"
go test -c -tags=integration -o "$test_binary" ./internal/service

if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1 && docker image inspect "$fixture_image" >/dev/null 2>&1; then
  docker_mode=1
  index=0
  while [ "$index" -lt "$count" ]; do
    docker run -d --label "$label" --name "touchgrass-security-soak-$run_id-$index" \
      "$fixture_image" sleep 300 >/dev/null
    index=$((index + 1))
  done
  source_label=$label
  source_mode="docker"
else
  source_label=''
  source_mode="deterministic fakes"
fi

sample_process() {
  pid=$1
  cpu_file=$2
  rss_file=$3
  : >"$cpu_file"
  : >"$rss_file"
  samples=0
  while [ "$samples" -lt 8 ]; do
    ps -p "$pid" -o %cpu= | awk '{$1=$1; print}' >>"$cpu_file"
    ps -p "$pid" -o rss= | awk '{$1=$1; print}' >>"$rss_file"
    samples=$((samples + 1))
    sleep 0.5
  done
}

run_probe() {
  name=$1
  test_name=$2
  ready="$work_dir/$name.ready"
  output="$work_dir/$name.out"
  cpu="$work_dir/$name.cpu"
  rss="$work_dir/$name.rss"
  TOUCHGRASS_SECURITY_SOAK_BASELINE=$([ "$name" = baseline ] && echo 1 || echo 0) \
    TOUCHGRASS_SECURITY_SOAK_READY_FILE="$ready" \
    TOUCHGRASS_SECURITY_SOAK_HOLD="6s" \
    TOUCHGRASS_SECURITY_SOAK_CONTAINERS="$count" \
    TOUCHGRASS_SECURITY_SOAK_LABEL="$source_label" \
    "$test_binary" -test.v -test.run "^$test_name$" >"$output" 2>&1 &
  probe_pid=$!
  probe_started=$(date +%s)
  attempts=0
  while [ ! -f "$ready" ]; do
    if ! kill -0 "$probe_pid" 2>/dev/null; then
      cat "$output" >&2
      fail "$name probe exited before becoming ready"
    fi
    attempts=$((attempts + 1))
    [ "$attempts" -lt 200 ] || fail "$name probe did not become ready"
    sleep 0.05
  done
  sample_process "$probe_pid" "$cpu" "$rss"
  wait "$probe_pid" || {
    cat "$output" >&2
    fail "$name probe failed"
  }
  probe_finished=$(date +%s)
  echo $((probe_finished - probe_started)) >"$work_dir/$name.elapsed"
  awk '{sum += $1; count += 1} END {if (count == 0) print "0"; else printf "%.2f", sum/count}' "$cpu" >"$work_dir/$name.cpu.avg"
  awk 'BEGIN {max = 0} {if ($1 > max) max = $1} END {printf "%.0f", max}' "$rss" >"$work_dir/$name.rss.max"
}

run_probe baseline TestContainerSecuritySoakBaseline
run_probe enabled TestContainerSecuritySoak

baseline_cpu=$(cat "$work_dir/baseline.cpu.avg")
enabled_cpu=$(cat "$work_dir/enabled.cpu.avg")
baseline_rss=$(cat "$work_dir/baseline.rss.max")
enabled_rss=$(cat "$work_dir/enabled.rss.max")
baseline_elapsed=$(cat "$work_dir/baseline.elapsed")
enabled_elapsed=$(cat "$work_dir/enabled.elapsed")
cpu_overhead=$(awk -v baseline="$baseline_cpu" -v baseline_elapsed="$baseline_elapsed" \
  -v enabled="$enabled_cpu" -v enabled_elapsed="$enabled_elapsed" -v window="$simulated_window_seconds" \
  'BEGIN {seconds=(enabled/100*enabled_elapsed)-(baseline/100*baseline_elapsed); if (seconds < 0) seconds=0; printf "%.2f", seconds/window*100}')
rss_growth=$((enabled_rss - baseline_rss))
[ "$rss_growth" -ge 0 ] || rss_growth=0

awk -v value="$cpu_overhead" -v limit="$max_cpu" 'BEGIN {exit !(value <= limit)}' || \
  fail "added CPU ${cpu_overhead}% exceeds ${max_cpu}% of one core"
[ "$rss_growth" -le "$max_rss_growth_kib" ] || \
  fail "added RSS ${rss_growth} KiB exceeds ${max_rss_growth_kib} KiB"

report=$(grep 'SECURITY_SOAK ' "$work_dir/enabled.out" | tail -1 | sed 's/^[[:space:]]*security_integration_test.go:[0-9]*: //')
[ -n "$report" ] || fail "soak metrics report is missing"

echo "container security soak: PASS"
echo "  source: $source_mode ($count containers)"
echo "  baseline / enabled CPU: ${baseline_cpu}% / ${enabled_cpu}%"
echo "  cadence-normalized added CPU: ${cpu_overhead}% (<= ${max_cpu}% of one core over ${simulated_window_seconds}s)"
echo "  baseline / enabled RSS: ${baseline_rss} KiB / ${enabled_rss} KiB"
echo "  added RSS: ${rss_growth} KiB (<= ${max_rss_growth_kib} KiB)"
echo "  $report"
