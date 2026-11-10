# ADR-0013: Generated first-boot admin password — one-time, replaced at first login

- Status: Accepted (2026-11-10)
- Amends: ADR-0010 ("`TOUCHGRASS_ADMIN_PASSWORD` seeds the bootstrap admin"):
  the variable becomes optional

## Context

Until now the server refused to start without `TOUCHGRASS_ADMIN_PASSWORD`,
and seeded the `admin` account from it on first boot. In practice that
variable was the hardest part of an install:

- It has to reach the process through whatever carries configuration — a
  unit file, a compose file, a CI secret — so the admin's password is
  written down in at least one more place than the database, usually by a
  tool that cannot show it back.
- The 12-character minimum is checked only when the admin is seeded, so a
  short value produced a crash loop, not a message on the form where it
  was typed.
- Operators who generated a value and lost it had no way back in: the
  variable is ignored once an enabled admin exists, and there was no reset
  command.
- Every CLI subcommand loaded the same configuration, so `touchgrass env …`
  also demanded an admin password it never used.

A fixed default password ("admin/admin, change it later") would remove the
variable, but the console is routinely published on the internet and holds
deploy powers and, since ADR-0012, every managed secret. A known password
is exposed from the moment the port opens until someone logs in.

## Decision

1. **`TOUCHGRASS_ADMIN_PASSWORD` is optional.** When it is set, behaviour
   is unchanged: it seeds the bootstrap admin while no enabled admin
   exists, and the operator's value is not forced to change.
2. **When it is unset and no enabled admin exists, the server generates a
   one-time password** (`crypto/rand.Text`: 26 base32 characters, 130
   bits), writes it to `initial-admin-password` beside the database (mode
   0600, written before the account row so a crash never leaves an admin
   nobody can open), and logs a warning naming the file. The password is
   never logged or printed by the server.
3. **A one-time password must be replaced before anything else.** The
   account carries `must_change_password`; while it is set every API route
   except `GET /api/auth/me`, `POST /api/auth/password` and
   `POST /api/auth/logout` answers 403 `password_change_required`, and the
   SPA shows only the change-password screen. The initial password file is
   deleted when the password is replaced.
4. **`POST /api/auth/password` is self-service for every role.** It needs
   the current password, applies the managed-account policy, rejects
   reusing the current password, counts a wrong current password against
   the login rate limit, audits the change, and ends the caller's other
   sessions.
5. **`touchgrass users reset-password [--username admin]` is the offline
   recovery path.** Run on the host against the live database, it gives
   one enabled account a new one-time password, prints it once to the
   operator's terminal, and audits the reset as `cli:<os user>`. Host
   access is already equivalent to full control of the console, so this
   grants nothing new.
6. **A password configured later wins while the generated one is unused.**
   Setting `TOUCHGRASS_ADMIN_PASSWORD` and restarting replaces a pending
   generated password and clears the restriction; once the admin has
   chosen a password the variable is ignored, as before.

## Consequences

- A fresh install needs no secret: start it, read one root-owned file,
  log in, choose a password. Installers no longer carry the admin's
  password.
- The password an operator ends up with is one they chose in a browser
  with their password manager, not one a deployment tool holds.
- Upgrades are transparent: existing accounts get
  `must_change_password = 0`, and deployments that set the variable keep
  working byte for byte.
- The one-time password exists in clear in one file until first login.
  It sits in the data directory with the database (which holds the
  bcrypt hashes and, with ADR-0012, the encrypted env store), is 0600,
  and is removed on first login. Anyone who can read it can already read
  or replace the database.
- An admin reset through **Users** clears `must_change_password`: that
  password was chosen by a person. Forcing a change after an admin reset
  is left to the roles-and-access work.
- The restriction is enforced in the auth middleware, not per handler, so
  a new route is locked by default.

## Alternatives considered

- **Fixed default password with a forced change.** Simplest to explain,
  but leaves a public console open to anyone until the first login.
- **First-visitor setup page.** Same exposure window, and it races the
  operator against scanners that watch certificate-transparency logs.
- **Print the generated password to the log.** Convenient with
  `docker logs`, but logs are shipped, retained and read far more widely
  than a 0600 file in the data directory.
- **Regenerate at every boot until first login.** Would silently replace
  a one-time password an operator had already copied; a restart must not
  invalidate it.
