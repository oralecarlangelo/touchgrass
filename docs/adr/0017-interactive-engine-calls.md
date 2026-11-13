# ADR-0017: Interactive engine calls — a reserved permit, bounded waits, and refusals that are not failures

- Status: Accepted (2026-11-13)
- Source: found while verifying the schema-migrations work (S35)
- Amends: the request guard's contract in `internal/docker/guard.go`

## Context

Every one-shot call to the container engine goes through one request
guard. It allowed two calls at a time, shared by every caller, and
paused all calls for a minute after three failures.

Two things went wrong with that on a host with about 27 containers:

- **A page waited behind the collectors.** One metrics pass asks the
  engine for a stats sample per container, about a second each, two at
  a time. For most of each pass both permits were held and a couple of
  dozen collector calls were queued for the next one. A console read
  that needed the engine joined that queue. It waited up to the HTTP
  write timeout (10 s), the server cut the response, and the client
  got an empty reply with no error to show.
- **A refusal counted as a failure.** "No such container" was recorded
  like a daemon that did not answer. Three reads of a container that
  had just been removed paused every engine call for a minute.

Measuring also showed a third fact that this decision does not remove:
the engine itself can be slow while the collectors run. On the test
host a direct `GET /images/json` to the engine socket, bypassing Touch
Grass, took up to 13 s during a collection pass.

## Decision

**A call is interactive when someone is waiting on it.**
`docker.WithInteractive(ctx)` marks a context. The HTTP server marks
every `GET` route in the route table. It does not mark writes: several
hand their context to a job that outlives the request (deploys, env
applies, backups), and those must keep waiting their turn.

**One permit is reserved for interactive calls.** Background calls
still share `MaxConcurrent` (2) permits. An interactive call may take
one of those or the reserved one. The engine therefore sees at most
`MaxConcurrent + 1` calls at once, up from `MaxConcurrent`.

**An interactive call has bounds, and missing them is "busy", not
"failed".** It waits at most 2 s for a permit and at most 7 s for the
engine's answer, 9 s together, under the 10 s write timeout. Past
either bound the call returns `docker.ErrEngineBusy`. Neither case
counts toward the circuit breaker: the collectors keep running with
the full timeouts and remain its evidence. Other errors on an
interactive call count as before.

**The HTTP layer answers `503 engine_unavailable`** for
`ErrEngineBusy` and for `ErrDaemonBackoff`, which used to surface as
`500 internal`.

**An engine refusal is an answer.** The guard does not count an error
the engine classifies as not-found or conflict (the container, image
or path is gone, or in the wrong state). The rule sits in the guard,
so every current and future call gets it without wrapping its result.

## Alternatives considered

- **Reserve one of the two existing permits.** Keeps the engine at two
  concurrent calls but halves collector throughput; a 27-container
  stats pass would no longer fit in its 30 s interval.
- **Bounded wait only, no reserved permit.** Simple, but with both
  permits held for most of every pass, reads would fail busy much of
  the time instead of being served.
- **Mark background calls instead of interactive ones.** A collector
  that forgets the mark would then compete as interactive. Unmarked
  defaulting to patient is the safer failure.
- **A per-request time budget across all engine calls.** Bounds a
  handler that makes several calls, but a context value survives
  `context.WithoutCancel`, so a job started from a request would
  inherit a spent budget.

## Consequences

- A read that needs the engine answers, or fails with a retryable 503,
  before the write timeout. A handler that makes several slow engine
  calls in a row can still exceed it; each call is bounded, the sum is
  not.
- The engine can see three concurrent calls instead of two.
- A read can now fail with `engine_unavailable` where it used to wait.
  Clients should treat the code as retryable.
- A job started from a `GET` handler would inherit the interactive
  bounds. No such handler exists; a new one must not pass its request
  context to background work.
- The engine being slow under collection load is unchanged. Reducing
  what the collectors ask of the engine is separate work.
