# Fleet, performance and storage reliability

Status: implementation and validation in progress (2026-11-27).

## Verified causes

- Production request logs recorded onboarding discovery taking 18,130 ms. The HTTP server write deadline is 10 seconds. The old blue/green path could execute six sequential three-second probes plus Docker discovery.
- Docker's operation deadline began after acquiring a shared permit, so queue time was unbounded. A regression test holding the only permit reproduced this: a 20 ms operation waited for its outer one-second deadline.
- An authenticated baseline reproduced `/api/storage/usage` returning 502 after 11,030 ms. In the same sample, fleet inventory returned 200 in 24 ms, Docker images 200 in 693 ms and onboarding 200 in 145 ms. Failures are intermittent; those successful samples do not establish load resilience.
- A direct `docker system df` on production took 29.626 seconds, confirming expensive daemon accounting independently of the application queue. Subsequent warm, type-specific queries took 1.65 seconds for images, 3.94 seconds for volumes, 0.01 seconds for build cache and 0.26 seconds for containers.
- The performance screenshot's dropped-record totals are cumulative, not necessarily active loss. A disabled writable-layer scanner also retains a previous persisted failure. Collector status must distinguish current configuration, partial coverage, fresh failures and historical loss.

## Changes underway

- Include Docker queue wait in each operation deadline. Bound fleet stats producers to two workers, avoiding one queued goroutine per container; a generic 100-container test verifies bounded concurrency and complete collection.
- Bound mandatory onboarding discovery to two seconds and optional probes to a shared three-second budget. Reuse a selected color's probe result and return partial drafts with explicit warnings. Busy mandatory discovery returns a retryable 503 rather than overrunning the response deadline.
- Isolate fleet host, inventory and history loading; abort superseded requests, avoid overlapping polls, retain good data on refresh failure, and support local retries.
- Move expensive storage/image inspection away from page request latency and retain explicit snapshot freshness.
- Add an administrator connection/discovery flow for S3-compatible storage, including MinIO, explicit bucket selection and read-only defaults.

## Verification recorded so far

- Queue-budget regression failed before the fix and passed after it.
- Onboarding generic blue/green tests verify a single probe per color, shared deadlines and a useful partial draft when health probes stall.
- Rendered generic fleet fixture checks passed: delayed host data did not block inventory; a failed refresh retained rows and offered retry; changing history range hid the previous window and did not refetch inventory; closing the management dialog aborted discovery.
- Rendered generic performance/storage fixtures passed: disabled sources show setup status; historical drops do not imply current failure; missing PostgreSQL extension guidance and detected source config are visible/copyable; host capacity renders before delayed accounting; cold images show collection status; saving a new connection with the same bucket resets prior browser state.
- Deployment, concurrent endpoint checks and final test gates remain outstanding. No completion or production-fix claim is made yet.

## Performance collector scope and PostgreSQL limitation

- Collector health now separates current loss from history. A newly increased Nginx drop counter marks one pass as partial; an unchanged counter recovers on the next clean pass while retaining the cumulative total. Cursor read failures remain explicit rather than reporting healthy coverage.
- Optional collectors disabled by configuration are recorded as setup-needed without erasing prior success or drop history. Status persistence is fail-open and cannot prevent startup.
- Administrators can copy a reviewable configuration draft using PostgreSQL and Redis/Valkey candidates from the existing fleet inventory, including unmanaged containers. The UI does not write server configuration, infer credentials, restart services, or enable the intentionally optional writable-layer scanner.
- Timeline deployment and runtime markers honor the request limit (50 from the performance page) instead of returning up to 1,000 of each marker type.
- Production read-only inspection found `pg_stat_statements` available but not installed and absent from `shared_preload_libraries`. Core PostgreSQL metrics remain available; only query-level coverage is partial. Enabling it requires an intentional PostgreSQL configuration change, restart, `CREATE EXTENSION pg_stat_statements` in the monitored database, and suitable read access for the configured collector role. No production restart or extension change was performed.

Validation: the full `make test-affected` suite passed; focused Go tests for `internal/dbstats`, `internal/service`, and `cmd/touchgrass` passed; the performance Vitest file passed 11 tests; `npm run verify` and `git diff --check` passed. New-code lint found no performance/dbstats findings after formatting; unrelated concurrent storage work was still being linted separately.

## Private object storage integration

Production MinIO listens privately on loopback and a Docker network. Application image-serving URLs are not S3 API origins. Touch Grass therefore uses authenticated same-origin streaming for object transfers; no public MinIO exposure is required. Transfer admission is bounded independently, credentials stay server-side, configured bucket allowlists remain enforced, and UI writes remain opt-in. The deployment needs an exact Nginx transfer location with matching 100 MiB and bounded timeout settings.

The existing application credentials tested by the setup investigation did not authorize the intended buckets. A dedicated regular MinIO identity with a policy limited to the two Ticketnation media buckets was provisioned; root credentials are not used for application requests. Direct permission checks confirmed both allowed buckets are accessible and an unrelated bucket is denied. UI writes default to disabled. Existing objects and bucket policies were not changed by provisioning.

## Existing operational limits

- Full repository style lint still reports 716 findings outside the new-code gate; the new-code lint gate reports zero issues. These pre-existing findings were not expanded into an unrelated cleanup.
- Vite retains the existing large-bundle warning (about 338 kB gzip); this change targets server latency, request concurrency and independent data display.
- The installed MinIO release is `RELEASE.2025-09-07T16-13-09Z`, older than the fix named in [MinIO's service-account policy advisory](https://github.com/minio/minio/security/advisories/GHSA-jjjj-jwhf-8rgr). This integration uses a separately restricted regular identity rather than a child of root. A MinIO upgrade remains a separate infrastructure follow-up; no storage-engine replacement/restart was performed here.
