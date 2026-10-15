# Performance operations

Touch Grass correlates bounded native infrastructure signals without an APM
agent, OpenTelemetry, application request bodies, or raw SQL. Correlation is an
investigation aid, not a distributed trace and not proof that one signal caused
another.

## Safety contract

- Touch Grass receives read-only Docker, host-filesystem, Nginx-log,
  PostgreSQL-statistics, and Redis-statistics access.
- The Nginx stream contains an approved route family, never a raw URI, query,
  client address, header, cookie, referrer, or user agent.
- PostgreSQL collection uses numeric statistics only. Redis collection uses
  `INFO`, bounded command-family totals, and no keys or values.
- Minute and hourly aggregates are retention-bound. SQLite maintenance uses
  bounded delete batches, passive WAL checkpoints, a byte budget, and online
  snapshots. Automatic snapshots keep the newest
  `TOUCHGRASS_SQLITE_BACKUP_KEEP` files (default 14).
- Historical data remains readable when one collector is stale. A missing
  source never becomes a numeric zero.

Run `./scripts/performance-soak.sh` after a build to repeat the privacy,
overhead, SQLite-growth, and API-latency smoke check.

## Prepare Nginx

1. Copy `deploy/nginx/route-families.map` and
   `deploy/nginx/timing-log.conf` into Nginx's `http` context.
2. Create `/var/log/nginx/ticketnation-performance` for the Nginx worker. Do
   not grant Touch Grass write access.
3. Review every route-map change with the API owner and privacy reviewer.
   New families must also be added to the parser allowlist; unknown paths stay
   `other`.
4. Run `nginx -t`, reload Nginx, and confirm JSON lines are produced. A timing
   log failure must not stop request serving; alert on Nginx write errors.
5. Rotate the dedicated file by rename/create and signal Nginx to reopen logs.
   Do not use copy-truncate unless testing its dropped-record warning.

The Compose deployment mounts the output directory read-only at
`/var/log/nginx/ticketnation-performance`. Set:

```bash
TOUCHGRASS_PERFORMANCE_SERVICE_ID=tn-api
TOUCHGRASS_NGINX_TIMING_DIR=/var/log/nginx/ticketnation-performance
TOUCHGRASS_NGINX_TIMING_LOG=/var/log/nginx/ticketnation-performance/requests.jsonl
```

The service ID must already exist in Touch Grass. A missing service fails
startup rather than attributing data to the wrong workload.

## Roll out one source at a time

Take a SQLite snapshot and capture baseline Touch Grass CPU, memory, and DB
size first. Use at least one representative traffic window per step.

1. **Nginx:** set the timing path and service ID; keep PostgreSQL and Redis
   container variables empty. Verify request count, route families, latency,
   rotation recovery, and zero privacy matches.
2. **PostgreSQL:** set `TOUCHGRASS_POSTGRES_CONTAINER`, user, and database.
   The commands run through the already-mounted Docker socket using local
   read-only statistics queries. Verify connections, transactions, resets,
   and collector freshness before continuing.
3. **Redis:** set `TOUCHGRASS_REDIS_CONTAINER`. Verify memory, clients,
   hits/misses, evictions, command-family totals, and freshness.
4. **Existing Docker/log signals:** verify container CPU/memory, lifecycle
   events, and log-volume buckets align with the same service. These reuse the
   existing Touch Grass Docker and log collectors.

Source switches are independent and require no schema rollback:

| Source | Enable | Disable / rollback |
| --- | --- | --- |
| Nginx timing | absolute `TOUCHGRASS_NGINX_TIMING_LOG` | unset only that path |
| PostgreSQL | set `TOUCHGRASS_POSTGRES_CONTAINER` | unset the container (also hides PostgreSQL health/backups) |
| Redis | set `TOUCHGRASS_REDIS_CONTAINER` | unset the container (also hides Redis health) |

Restart Touch Grass after changing source configuration. Old aggregates remain
until retention expires. Never delete migrations to roll back a collector.

## Collector stale, error, and loss response

1. Open **Performance → Collector status**. Record the source, last success,
   last recorded sample, safe error code, and dropped-record count.
2. Confirm the source independently:
   - Nginx: file exists in the Touch Grass container, is readable, grows, and
     its inode/size match the host.
   - PostgreSQL: container exists; the configured local-trust role can query
     `pg_stat_database` and connection limits.
   - Redis: container exists and `redis-cli INFO` succeeds.
   - Docker/log sources: socket is readable and the target container still
     exists under the expected Compose labels.
3. Fix only the failed source. Do not clear its cursor. A rename rotation is
   detected by file identity; truncation resets the offset and increments the
   loss counter.
4. Confirm a new success timestamp. Historical panels should have remained
   available throughout the outage.

If parse drops increase, stop the Nginx source first, preserve several rejected
lines outside SQLite, and compare fields to `timing-log.conf`. Never paste raw
production paths or headers into an issue.

## PostgreSQL resets and `pg_stat_statements`

PostgreSQL restart, failover, `pg_stat_reset`, or counter wrap invalidates the
previous baseline. Touch Grass marks that bucket as a reset and does not emit a
negative rate. Start comparisons after the reset marker.

Statement-level metrics are optional and need a maintenance window:

1. Record a pre-change parameter snapshot and verify extension compatibility.
2. Add `pg_stat_statements` to `shared_preload_libraries` using the platform's
   managed configuration, restart PostgreSQL, and run
   `CREATE EXTENSION IF NOT EXISTS pg_stat_statements` in the target database.
3. Confirm application health and numeric statement rows. Touch Grass stores
   query ID and numeric deltas only, never raw SQL or parameters.
4. Roll back by disabling statement collection first. To remove the server
   preload, schedule another restart, restore the prior parameter value, and
   optionally drop the extension only after retention/ownership review.

## Redis pressure

- Rising memory near `maxmemory` plus evictions indicates capacity or policy
  pressure; inspect the approved Redis admin tooling, not keys in Touch Grass.
- Blocked clients or persistence errors are operational incidents even when
  HTTP latency has not risen yet.
- A hit-ratio shift is a correlation. Validate application behavior and deploy
  timing before assigning cause.

## SQLite disk pressure and recovery

Touch Grass warns when the configured `TOUCHGRASS_SQLITE_MAX_BYTES` budget is
exceeded. Check the DB, `-wal`, snapshot directory, and host filesystem—not
only the DB main file. Do not delete a live WAL.

For recovery:

1. Stop Touch Grass cleanly and preserve the DB, `-wal`, and `-shm` together.
2. Copy the newest file from `TOUCHGRASS_SQLITE_BACKUP_DIR` to a new path; keep
   the damaged files untouched for diagnosis.
3. Run `sqlite3 <copy> 'PRAGMA integrity_check;'`; require `ok`.
4. Start Touch Grass against the verified copy, let migrations run, and check
   collector status plus bounded Performance reads.
5. If no snapshot is usable, copy all three live files together and use
   SQLite's documented recovery tooling offline. Never run destructive repair
   against the only copy.

Passive checkpoints can report busy readers; the next maintenance pass retries.
Investigate repeated busy state or sustained WAL growth before forcing a
checkpoint.

In the Compose deployment, snapshots live under `/data/backups/sqlite` on the
persistent `touchgrass-data` volume. Retention removes only regular files that
match Touch Grass's timestamped `touchgrass-*.db` snapshot name; unrelated
files are left untouched.

## Investigation workflow

1. Choose the smallest useful range and service.
2. Compare p95/p99 and error rate with container, PostgreSQL, Redis, log, deploy,
   restart, OOM, and health-transition signals.
3. Use range-preserving links to logs, issues, deploy history, or database
   health. Treat “correlates with” as a lead, not a conclusion.
4. Record the hypothesis, confirming evidence, counter-evidence, and a measured
   remediation. Application fixes belong in their own change and rollback.

## Full rollback

1. Unset `TOUCHGRASS_NGINX_TIMING_LOG`,
   `TOUCHGRASS_POSTGRES_CONTAINER`, and `TOUCHGRASS_REDIS_CONTAINER`; restart
   Touch Grass and verify its health endpoint.
2. Remove the read-only Nginx and host mounts after the stopped container no
   longer needs them.
3. Remove the Nginx timing `access_log` directive and route map, run
   `nginx -t`, then reload. Keep normal application access logging unchanged.
4. Leave schema and historical aggregates intact for retention. Restore the
   prior Touch Grass image/config if the UI or API itself must roll back.
5. Compare CPU, memory, disk, and request latency to the captured baseline and
   document the rollback result.
