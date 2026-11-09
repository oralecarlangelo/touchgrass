# ADR-0012: Encrypted env store — secrets at rest under an operator key

- Status: Accepted (2026-11-10)
- Supersedes: threat-model boundary 5 ("no secrets at rest by design") for
  the values of managed services' env files only

## Context

Every managed service reads its runtime configuration from an env file on
the host (for example `/opt/acme/env/api.env`). Today that file is edited
by hand over SSH: no history, no validation before the restart, no record
of who changed what, and a typo takes the service down until someone
notices. Operators asked for the file to be managed from the console with
the same discipline deploys already have — staged change, confirm, run,
roll back — without adopting a separate secrets product.

Managing the file means holding its values. The original threat model
kept touchgrass free of secrets at rest: its own credentials come from the
environment and nothing sensitive lives in SQLite. An env pillar cannot
honour that literally, so the question is how to hold values safely rather
than whether to hold them.

## Decision

touchgrass stores env values **encrypted at rest** in its SQLite file:

- AES-256-GCM, a random 12-byte nonce per value, associated data = service
  id + key name, so a ciphertext cannot be moved between rows.
- One operator-provided key, loaded at boot from `TOUCHGRASS_ENV_KEY_FILE`
  (32 raw or 64 hex bytes; the binary refuses any mode but `0600`) or, for
  development, `TOUCHGRASS_ENV_KEY`. Every row records `key_id` (first 8 hex
  characters of SHA-256 of the key) so a future rotation can tell entries
  apart. With no key the pillar is disabled: 503 `env_key_missing` on every
  env route and `configured:false, reason:"key_missing"` on the view.
- Plaintext exists only in request scope. The view returns `value:null,
  masked:true` for every secret regardless of role; non-secret values are
  shown. Reveal is a separate admin-only, rate-limited (10/min/user),
  audited call. Store models never serialise ciphertext; logs and audit
  rows carry key names, summaries, and counts only; buffers are zeroed.
- Changes are staged, then applied as one run: render (sorted `KEY=VALUE`,
  managed header, raw values) → validate (`validate_cmd` against the
  candidate) → atomic write with a `.bak` → `restart_cmd` → probe → new
  revision. A failed restart or probe restores the `.bak` and restarts
  again. Every revision snapshots all entries (encrypted) so rollback is a
  staged diff plus the same pipeline.
- File writes and commands run with the process's privileges or through
  `sudo -n` when the service config says so — the deploy engine's existing
  trust contract, blast radius one service. Hosts that already own the
  install path may set `write_cmd`/`read_cmd`: touchgrass then pipes the
  rendered file to the command and parses the command's output, and the
  command owns ownership, mode, and atomic install.

The key file is a new trust boundary: whoever holds the SQLite file *and*
the key reads every value; the SQLite file alone yields key names and
secret flags only. Key custody (0600, outside DB backups, separate backup)
is the operator's responsibility and is documented in `docs-site/env.md`.

## Rejected alternatives

- **External secrets manager (Vault, Infisical, sops + age):** rejected
  for v1 because it adds a second service to run, back up, and authenticate
  against on a single host, while the operational need is a managed file
  with history. The pillar's contract (stage, apply, rollback, audit) does
  not preclude an adapter later.
- **Plaintext in SQLite guarded by file permissions:** rejected; backups of
  the DB routinely leave the host, and the existing model already relies on
  `chmod 600` for PII — adding credentials to that would widen the blast
  radius of one copied file to every service.
- **Per-user or per-service keys, KMS envelopes, automatic rotation:**
  deferred. One key with a recorded `key_id` keeps v1 small and leaves a
  clear path to rotation (re-encrypt rows whose `key_id` differs).
- **Editing the file in place with no store:** rejected; it gives up
  history, staged review, validation before restart, and rollback — the
  reasons the pillar exists.

## Consequences

- `docs/threat-model.md` gains boundary 7 (key file ↔ SQLite) and a
  section on the env pillar; boundary 5 now covers touchgrass's own
  credentials only.
- New package `internal/secret`; new tables `env_entries`,
  `env_revisions`, `env_revision_entries`, `env_runs`, `env_validations`;
  new audit actions `env.stage`, `env.discard`, `env.import`,
  `env.reveal`, `env.apply`, `env.apply_failed`, `env.rollback`.
- The SSE stream carries a new event type `env.apply` with `run_id` and
  `step` fields; existing events are unchanged.
- Losing the key loses stored values and revisions; the file on disk and
  `import` rebuild the working set. Operators must back the key up
  separately from the database.
- Validator output is persisted; validators must print key names and
  reasons, never values.
