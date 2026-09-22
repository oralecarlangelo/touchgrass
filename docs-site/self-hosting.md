# Self-hosting

One static binary, one SQLite file, no dependencies. Everything below
runs anywhere Linux runs; the [troubleshooting](/troubleshooting) page
covers the common mistakes.

## Run it

```bash
export TOUCHGRASS_ADMIN_PASSWORD='...'   # required, never commit it
export TOUCHGRASS_DB=/data/touchgrass.db # default ./touchgrass.db
./touchgrass serve                       # listens on 127.0.0.1:8080
```

`migrate up` runs automatically on boot; the binary self-applies
schema versions. Put it behind nginx or Caddy for TLS, set
`TOUCHGRASS_COOKIE_SECURE=true` behind HTTPS, and rate-limit
`/api/auth/login` — the reference nginx config does all three.

## Configuration

| Variable                           | Default            | Meaning                                              |
| ---------------------------------- | ------------------ | ---------------------------------------------------- |
| `TOUCHGRASS_ADMIN_PASSWORD`        | (required)         | Single admin password, bcrypt-hashed in memory       |
| `TOUCHGRASS_DB`                    | `./touchgrass.db`  | SQLite path (WAL sidecars live alongside it)         |
| `TOUCHGRASS_ADDR`                  | `127.0.0.1:8080`   | Listen address; keep loopback behind a proxy         |
| `APP_ENV`                          | `prod`             | `dev` enables the Vite proxy; `prod` serves embedded |
| `TOUCHGRASS_COOKIE_SECURE`         | `false`            | Set `true` behind TLS                                |
| `TOUCHGRASS_METRICS_INTERVAL`      | `30s`              | Container sample cadence                             |
| `TOUCHGRASS_WATCH_INTERVAL`        | `5s`               | Live-color poll cadence                              |
| `TOUCHGRASS_LOG_POLL_INTERVAL`     | `5s`               | Container log tail cadence                           |
| `TOUCHGRASS_CUTOVER_TIMEOUT`       | `10m`              | Deploy script ceiling                                |
| `TOUCHGRASS_RETENTION_METRICS`     | `168h`             | Metric sample age trim                               |
| `TOUCHGRASS_RETENTION_NOTIFICATIONS` | `720h`           | Notification age trim                                |
| `TOUCHGRASS_RETENTION_DEPLOYS`     | `8760h`            | Deploy + probe-sample age trim                       |
| `TOUCHGRASS_RETENTION_ERRORS`      | `720h`             | Occurrence age trim (issues follow)                  |
| `TOUCHGRASS_INGEST_MAX_OCCURRENCES` | `10000`           | Occurrence count cap per service                     |
| `TOUCHGRASS_RETENTION_LOGS`        | `168h`             | Log line age trim                                    |
| `TOUCHGRASS_LOGS_MAX_LINES`        | `50000`            | Log line count cap per service                       |
| `TOUCHGRASS_RETENTION_SDK_LOGS`    | `7`                | SDK (application) log age trim, in whole days        |

Durations parse Go syntax (`30s`, `10m`, `168h`).
`TOUCHGRASS_RETENTION_SDK_LOGS` is the exception: a whole-day count.

## Data and backups

State is `touchgrass.db` plus `-wal`/`-shm` sidecars — back them up
together, ideally with SQLite's backup API against a quiet moment.
The DB holds prod log lines and error payloads: `chmod 600`, keep it
off shared backups, and treat read access as production access.

## Sizing

The steady-state footprint is small: sampling every 30s, log polls
every 5s with a 2k-line cap, retention sweeping on schedule. The two
knobs that move disk are `TOUCHGRASS_LOGS_MAX_LINES` and
`TOUCHGRASS_INGEST_MAX_OCCURRENCES` — size them from a day of real
volume, then leave them alone.
