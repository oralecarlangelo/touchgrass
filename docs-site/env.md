# Environment & secrets

Each managed service can own one runtime env file (for example
`/opt/acme/env/api.env`) that touchgrass renders, validates, writes,
and rolls back. Values are encrypted at rest in the touchgrass
SQLite file, secrets are masked everywhere in the UI and API, every
reveal is audited, and every apply leaves a revision you can return to.

## Key custody

The pillar is **off until you give it a key**. Generate one and keep
it outside the SQLite file and its backups:

```bash
sudo install -d -m 0700 /etc/touchgrass
head -c 32 /dev/urandom | sudo tee /etc/touchgrass/env.key >/dev/null
sudo chmod 0600 /etc/touchgrass/env.key
export TOUCHGRASS_ENV_KEY_FILE=/etc/touchgrass/env.key
```

| Variable                  | Meaning                                                                                  |
| ------------------------- | ---------------------------------------------------------------------------------------- |
| `TOUCHGRASS_ENV_KEY_FILE` | Path to 32 raw bytes or 64 hex characters. Mode **must** be `0600`; the binary refuses otherwise |
| `TOUCHGRASS_ENV_KEY`      | 64 hex characters inline — development only                                              |

Values are sealed with AES-256-GCM, a fresh random nonce per value, and
the service id plus key name as associated data, so a ciphertext moved
to another row fails to open. Each row records `key_id` (the first 8 hex
characters of SHA-256 of the key) so a future rotation can tell old
rows apart. Without a key every env route answers 503 `env_key_missing`
and `GET /api/services/{id}/env` reports `configured:false,
reason:"key_missing"`.

Losing the key loses every stored value: the file on disk still works,
and `import` rebuilds the store from it, but revisions and pending
changes are gone. Back the key up separately from `touchgrass.db`.

## Configure a service

Add an `env` block to the service's config JSON beside its strategy
fields:

```json
"env": {
  "target": { "path": "/opt/acme/env/api.env", "owner": "acme-ops", "mode": "0600" },
  "validate_cmd": "/opt/acme/bin/validate-env.sh --root /opt/acme/env",
  "restart_cmd": "/opt/acme/bin/restart-active.sh api",
  "read_cmd": "",
  "write_cmd": "",
  "probe_url": "http://127.0.0.1:8080/__release",
  "probe_timeout_secs": 120,
  "sudo": true
}
```

- `target.path` must be absolute. `owner` and `mode` apply to the
  written file (`mode` defaults to `0600`).
- `validate_cmd`, `restart_cmd`, and `probe_url` are each optional;
  an empty one skips that step and the run log says so. Commands are
  split on whitespace — no shell quoting.
- The validator receives the **candidate** file path as its last
  argument and as `TOUCHGRASS_ENV_CANDIDATE`, plus
  `TOUCHGRASS_ENV_TARGET` and `TOUCHGRASS_ENV_SERVICE`. Exit non-zero
  to reject; print `INVALID <service>:<KEY>: <reason>` lines and the UI
  marks those keys as required.
- `sudo: true` runs every file operation and command through
  `sudo -n`; grant the touchgrass user a NOPASSWD rule for `sh`, `mv`,
  `rm`, `cat`, `test`, `env`, and your two scripts. Alternatively leave
  `sudo` false and spell `sudo` inside each command string.
- `read_cmd` and `write_cmd` are optional adapters for hosts that own
  the install themselves. With `write_cmd`, apply pipes the rendered
  file to the command's stdin instead of writing `target.path`; the
  command owns ownership, mode, atomic install, and any extra
  validation, and a non-zero exit fails the run at the write step with
  its output as `validation_lines` (nothing is restored — the command
  never installed the file). A later restart or probe failure replays
  the previously applied revision through `write_cmd`. With
  `read_cmd`, import parses the command's stdout instead of reading
  `target.path`. Every command receives `TOUCHGRASS_ENV_TARGET`,
  `TOUCHGRASS_ENV_SERVICE`, and (for validate/write/restart)
  `TOUCHGRASS_ENV_CANDIDATE` and `TOUCHGRASS_ENV_RUN`.

## Import an existing file

Onboarding reads the file already on the host into the store as
*applied* entries and records it as the first revision:

```bash
touchgrass env import --service api          # or POST /api/services/api/env/import
```

Keys whose names contain `KEY`, `SECRET`, `TOKEN`, `PASSWORD`, `PASS`,
`DSN`, or `PRIVATE`, and `URI`/`URL` values that embed credentials,
default to secret. Flip the flag per entry afterwards if the guess was
wrong. Import refuses with 409 `env_has_entries` when the store already
holds entries; pass `{"replace": true}` (or `--replace`) to overwrite.

## Change values

Changes are **staged**: `PUT /api/services/{id}/env/entries/{KEY}` with
`{"value": "...", "is_secret": true}` and `DELETE …/entries/{KEY}`
record pending adds, changes, and removals without touching the file.
The env view shows each entry's state (`applied`, `added`, `changed`,
`removed`) and `pending_count`; a changed non-secret entry also carries
`applied_value` so the UI can offer undo. `GET …/env/diff` lists the
pending keys and the sha256 of the file an apply would write. `POST
…/env/discard` drops everything pending; deleting a key that was only
ever staged (`added`) drops the staged add outright.

Keys match `^[A-Z_][A-Z0-9_]*$`. Values are UTF-8 without newlines, up
to 64 KiB, and are written raw — quote them yourself if your loader
needs quotes.

Secrets never come back from the view: `value` is `null` and `masked`
is `true` for admins and viewers alike, and `applied_value` is never
present for them. An admin can call `POST …/entries/{KEY}/reveal` (10
per minute per user; a 429 carries `Retry-After`); each call writes an
`env.reveal` audit row naming the key.

## Apply

```bash
touchgrass env apply --service api --summary "rotate DB password"
# or POST /api/services/api/env/apply {"confirm": "api", "summary": "..."}
```

`confirm` must equal the service name. The run returns `202 {run_id}`
and then:

1. **render** — header `# managed by touchgrass — do not edit`, then
   `KEY=VALUE` lines sorted by key, LF endings — to a temp file beside
   the target with the configured mode and owner;
2. **validate** — `validate_cmd` against the candidate (60 s); failure
   deletes the candidate, stores the output as `last_validation`
   (with `missing_required` parsed from a `missing required keys: A, B`
   line), and fails the run with `env_validation_failed`;
3. **write** — atomic rename over the target, keeping one `.bak`;
4. **restart** — `restart_cmd` (10 min, lines streamed);
5. **probe** — `probe_url` until it answers 200 (default 120 s);
6. **done** — a new revision snapshots every entry, pending entries
   become applied, and `env.apply` is audited with the key names.

If restart or probe fails the `.bak` goes back, `restart_cmd` runs
again, the run is marked failed with `restored: true`, and
`env.apply_failed` is audited. Progress streams on `GET /api/events`
as a named `event: env.apply` whose data carries `type`, `run_id`,
`service_id`, `step`, `message`, and `at`. `GET …/env/runs/{run_id}`
returns `{run_id, status: running|succeeded|failed, steps: [{name,
status, message, at}], error?, validation_lines?, restored?,
started_at, finished_at?}`. One run per service at a time (409
`env_apply_in_progress`); while one runs the view reports
`apply_in_progress: true` and `active_run_id`.

## Roll back

`GET …/env/revisions` lists revisions newest first with the keys each
one added, changed, or removed. `POST …/env/revisions/{rev}/rollback
{"confirm": "api"}` stages that snapshot as pending — so the diff shows
exactly what will change — and runs the same apply pipeline. The
result is a new revision, so history only ever grows.

## CLI

For installers and break-glass work the binary carries thin
subcommands that use the same service layer, key, and audit trail
(actor `cli:<os user>`):

```bash
touchgrass env import --service api [--replace]
touchgrass env set --service api [--secret] DATABASE_URL=postgres://...
touchgrass env apply --service api [--summary TEXT]
```

They need `TOUCHGRASS_DB` and the key variable in the environment and
run against the database directly; stop nothing, but avoid running
`apply` from two places at once.
