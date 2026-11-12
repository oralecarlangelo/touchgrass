# ADR-0017: Per-username login throttle — a shared lane for strangers, a lane of its own for a known browser

- Status: Accepted (2026-11-13)
- Source: S34, the fourth sprint of the roles-and-access plan
- Related: ADR-0010 (accounts and sessions), ADR-0014 (more accounts per
  deployment, which is what makes a per-address limit too little)

## Context

Failed logins were limited per client address: five in a minute, then
429 for that address. Three things made that too little once a
deployment has more than a couple of accounts:

- The address is the TCP peer. An attacker who spreads guesses over
  many addresses is not slowed at all on any one username.
- A success from an address clears its count. Anyone with an account of
  their own can alternate a good login with guesses at someone else's
  and never reach the limit.
- `Users.Verify` returned at once for a username that does not exist
  and ran bcrypt for one that does, so the response time said which
  usernames exist.

A limit per username closes the first two. Done naively it opens a
worse hole: whoever knows a username can keep its counter full and the
real user can never sign in. The task set four conditions: an attacker
must not be able to lock a user out indefinitely, must not learn whether
a username exists, the tracking state must be bounded, and the window
must expire.

## Decision

**Two limits, one answer.** The per-address limit stays as it was (5
failures a minute). A second limit counts failed logins per username,
from every address together: 10 failures in a sliding 15 minutes. Both
are checked before the password is looked at, and both answer the same
`429 rate_limited` body, so a client cannot tell which one it met.

**A refused attempt is not counted.** Only attempts that reached the
password check add to a count. Hammering a limited username does not
extend the limit; it lifts as the counted failures age out, at most 15
minutes after the last one.

**A browser the user has signed in from has a lane of its own.** A
successful login also sets `touchgrass_device`: a random 32-byte token
in an `HttpOnly`, `SameSite=Strict` cookie sent only to
`/api/auth/login`, kept for 30 days. The server remembers which
username it was issued for. A login that presents the token of that
username is counted under the token, not under the username. So:

- strangers share the username's lane, and filling it shuts out only
  other strangers;
- the user, on a browser they have used before, is not affected by it
  and signs in normally;
- the token is not a credential and not a pass: it proves nothing about
  who is asking, its lane has the same 10-in-15-minutes budget, and a
  token issued for one username does nothing for another. Stealing one
  buys an attacker ten more guesses per window.

An admin's password reset forgets the user's remembered browsers along
with their sessions: whoever signed in with the old password is a
stranger again.

**A username that does not exist is treated exactly like one that
does.** Its failures are counted in the same map under the same rules,
and it meets the same limit at the same attempt. `Users.Verify` now
compares the password against a decoy bcrypt hash (random secret, the
cost new passwords get) when the username is unknown, and compares
against the stored hash before it reads the disabled flag. Unknown,
disabled and wrong-password logins do the same work and return the same
401. A device cookie is only ever set by a successful login.

**Bounded state.** Both failure maps hold at most 10,000 keys and at
most `limit` timestamps per key. A key is dropped as soon as its
failures have left the window (the per-address map used to keep an
empty entry for every address ever seen). When a map is full, expired
keys go first, then the key whose latest failure is the oldest. A
username longer than any account can have (64 characters) is keyed by
its SHA-256, so a key has a fixed largest size. Remembered browsers are
capped at 8 per username and 4,096 in total, oldest first; a user who
signs in over and over pushes out only their own. All of it is in
memory, like sessions, and a restart clears it.

## The trade-off

A per-username limit either bounds guessing or guarantees the user can
always get in; a design can only pick how to split the difference.

- With no trusted lane, the limit is a lockout switch for anyone who
  knows a username. Rejected.
- With the lane, a **stranger** is bounded to 960 guesses a day per
  username whatever their addresses, and the **user** is never shut out
  of a browser they have used in the last 30 days.
- What is left: a user on a **new** browser (or after a restart, which
  forgets the tokens) while someone is actively filling their username's
  lane gets 429 until the attacker stops for up to 15 minutes, or until
  they use a remembered browser. The attack has to be sustained, it is
  visible (`rate-limited login for this username` audit rows naming the
  client address), and it denies only fresh browsers. We accept that in
  exchange for a real bound on guessing.

## Rejected alternatives

- **Hard lockout after N failures until an admin unlocks.** The
  indefinite lockout the task rules out, and a new admin chore.
- **Exempting an address that last succeeded for the username.** No new
  cookie, but behind a reverse proxy every client has the proxy's
  address, so the exemption would cover the attacker too.
- **Progressive delay (tarpit) instead of refusal.** It never refuses
  the user, but each held request costs a goroutine and a connection,
  and a patient attacker still gets an unbounded number of guesses.
- **Checking the password even when limited, and letting a correct one
  through.** That is no limit on guessing at all.
- **CAPTCHA, 2FA, or SSO.** Out of scope for this plan.
- **Persisting the counters in SQLite.** An attacker-driven write per
  request for state that is worthless after 15 minutes.

## Consequences

- A login response carries a second cookie. Clients that keep a cookie
  jar need no change; a client that drops it only loses the lane.
- Ten wrong passwords for one username in 15 minutes now refuse further
  attempts from browsers that never signed in as it, where before only
  the address was limited. A script that logs in as many users from one
  address still meets the address limit first.
- Unknown-username logins take as long as wrong-password ones (about
  one bcrypt comparison), so failed logins cost the server more CPU
  than before. The address limit already bounds that per address.
- The decoy hash uses bcrypt's default cost. An account whose hash was
  made at another cost would still differ in timing; touchgrass has
  only ever written the default.
- Not changed: the address limit is still keyed by the TCP peer and
  ignores proxy headers. Behind a reverse proxy every user shares one
  address, so five failures a minute from anyone still refuse every
  login for that minute. Rate-limit `/api/auth/login` at the proxy, as
  the reference nginx config does. Trusting a forwarded-for header from
  a configured proxy would be a separate decision.
- The maps evict when full, so an attacker with enough addresses to
  record 10,000 failures on other usernames inside one window can push
  a victim's count out and start again. At five failures a minute per
  address that is 2,000 address-minutes for every ten extra guesses.
  Failing closed instead (refusing every untracked username when the
  map is full) would hand the same attacker a lockout of everyone.
- The self-service password change still counts wrong current passwords
  per address only. It needs a valid session, so it is not a way to
  guess at another account.
