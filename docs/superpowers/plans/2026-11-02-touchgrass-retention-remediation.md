# Touchgrass Retention Remediation Plan

## Objective

Stop Docker exec events from amplifying into full-fleet security scans, retain only
security state transitions as raw observations, provide 30 days of generic logs
within bounded storage, and safely compact the production SQLite database while
preserving critical operational records.

## Constraints

- Use TDD for every behavior change.
- Preserve unresolved security findings, audit records, users, policies,
  baselines, approvals, and deploy history.
- Do not expose log bodies, credentials, or customer payloads during diagnostics.
- Production cleanup requires a verified recovery copy and explicit pre/post
  counts before the original database can be retired.
- Keep rollback artifacts until production verification is complete.

## Tasks

1. Filter event-driven reconciliation so only topology-changing Docker events
   wake the fleet reconciler; routine exec and health events remain observable
   but cannot trigger a full scan.
2. Persist raw security observations only for finding transitions while keeping
   finding occurrence counts and current evidence accurate.
3. Apply bounded retention defaults: 30-day generic/SDK logs, 24-hour routine
   exec events and raw security observations, 90-day topology events, one-year
   resolved findings, and a log-line cap sized for measured 30-day volume.
4. Make maintenance deletion capacity per target rather than one shared global
   batch, and cover preservation of audit and unresolved findings.
5. Run full tests, race tests, vet, incremental lint, and a final branch review;
   commit and push the verified release.
6. Deploy the exact ARM64 commit to production with binary rollback, confirm the
   observation ingestion rate collapses, then create a verified recovery copy,
   remove expired raw telemetry in bounded batches, checkpoint, compact, swap,
   and verify critical counts and public health.

## Verification

- Routine exec events produce zero reconcile requests; topology events produce
  one coalesced request.
- Repeated unchanged security evaluations create one transition observation,
  while findings retain accurate occurrence counts.
- Retention tests prove raw telemetry expires without deleting audit or active
  findings.
- Production health, login, findings, system API, collector status, row counts,
  disk, WAL, backup, binary SHA, and rollback artifact are recorded.
