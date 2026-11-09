# Environment & secrets

touchgrass keeps each service's environment file under management: entries
are stored encrypted, staged edits are reviewed as a diff, and an apply
renders the file onto the host, validates it, restarts the service and
probes it, keeping the previous file for rollback.

<!-- TODO(api): the backend agent fills this section — storage model,
encryption key, target configuration keys, validator contract, audit
entries, and the REST routes (also mirrored in openapi.yaml). -->

## How it works on the host

_TODO(api)._ Until this lands, the UI assumes:

- the encryption key file is named by `TOUCHGRASS_ENV_KEY_FILE` on the host
  (the server also accepts the raw key in `TOUCHGRASS_ENV_KEY`); the UI names
  the file variable in its empty state (`envKeyEnvVar` in `web/src/lib/env.ts`);
- the env target lives in the service config's `env` block:
  `{ "env": { "target": { "path", "owner", "mode" }, "validate_cmd",
  "restart_cmd", "read_cmd", "write_cmd", "probe_url", "sudo" } }` (see
  `envTargetConfigSnippet` in the same file).

## The Environment tab

Open a service and pick **Environment**. The fleet-level **Operations →
Environment** page lists every service with its status (up to date, changes
not applied, apply running, no target, no key) and links to the same tab.

### States you will meet

| What you see | Why | What to do |
| --- | --- | --- |
| **Configure the env target** | The service config has no `env.target` | Admins see the exact JSON to add; reload the tab afterwards |
| **This host has no encryption key** | The server started without a key (`503 env_key_missing`) | Set the key on the host and restart touchgrass |
| **No entries yet** | Target configured, nothing tracked | **Import the current file** reads what is on the host; secret-looking keys are marked secret by name |
| **An apply is running** | Another session (or this one) started an apply | The table is read-only; the live progress is pinned at the top |
| Read-only account | You are a viewer | You see keys, plain values and the pending summary; no edits, no reveal |

### Entries

Each row shows the key (monospace), its value, who changed it and when
(hover for the full timestamp), a `required` badge when the validator asks
for the key, and a state chip once a change is staged: **Added** (green),
**Changed** (amber), **Removed** (red, struck through). The same colour runs
down the left edge of the row so a staged change reads like a diff.

- **Search** by key substring (press `/` to focus), filter **Pending**,
  **Secrets**, **Required**, sort by key or last updated.
- **Edit** a plain value inline: Enter saves, Esc cancels. Saving stages the
  change; nothing reaches the host yet.
- **Secrets** are always shown as `••••••••`. Admins can reveal one for
  20 seconds (it re-masks on its own) or copy it to the clipboard without
  showing it. Every reveal is audited and rate limited; after a `429` the
  eye shows a countdown. Viewers never see a reveal control.
- **Mark as secret / plain** toggles masking. Going from secret to plain
  reveals the value to your browser first.
- **Remove** stages a removal. **Undo** reverts a staged add or removal; a
  staged change to a secret cannot be undone row-by-row (the applied value
  never reaches the browser) — discard all changes instead.

### Adding entries

The row above the table stages one entry: key, value, secret toggle,
**Stage**. Keys follow `^[A-Z_][A-Z0-9_]*$`; values are a single line. The
secret toggle pre-selects itself for secret-looking names (`*_KEY`, `*_SECRET`,
`*_TOKEN`, `*PASSWORD*`, …).

Paste one or many `KEY=VALUE` lines into the key field and the **Paste .env
lines** sheet opens: comments and blank lines are dropped, quotes are
unwrapped, inline comments stripped, duplicates and invalid keys are flagged,
and you accept or skip each line (and flip its secret flag) before staging.
This is how a batch of payment keys lands in one go.

### Review and apply

While staged changes exist, a bar stays at the bottom: `3 changes not
applied · 1 added · 1 changed · 1 removed`, with **Review & apply** and
**Discard** (confirmed).

**Review & apply** shows the server-side diff as three lists, a summary
field prefilled from the keys (`Rotate GCASH_KEY; Add SMTP_HOST`), the five
steps the apply will run — render → validate → write → restart → probe — and
the confirmation gate: type the service name exactly, the same gate a
cutover uses. Apply stays disabled until the name matches.

On acceptance the dialog becomes a **progress view** pinned above the table:
a vertical stepper driven by the live `env.apply` events (polling takes over
if the stream drops), the log lines, and elapsed time. On success the table
reloads and a toast confirms. On failure the failing step is highlighted, the
validator's output is shown verbatim, you are told whether the previous file
was restored, and **Retry** reopens the review — staged changes stay staged.

### Revisions

**Revisions** opens a drawer listing every applied file, newest first, with
summary, author, time, the added/changed/removed counts, the first eight
characters of the rendered file's SHA-256 and a **current** badge. **Roll back
to this** uses the same typed-name gate and the same progress view.

### Validation

Above the table: the last validator result (passed/failed and when) and the
count of required keys. When the validator reports missing required keys, a
callout lists them with an **Add** shortcut that prefills the key.

### Keyboard and accessibility

`/` focuses search, Enter saves an inline edit, Esc cancels an edit, clears
the search, or closes any dialog or drawer. Every icon button carries an
ARIA label; dialogs trap focus; the table collapses to one card per entry
below 1024 px so the tab works on a tablet.
