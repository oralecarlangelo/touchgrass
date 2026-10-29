# Container Security Monitoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every Docker container visible across its full lifecycle and add alert-first, fleet-wide container security findings without changing managed-service deploy behavior.

**Architecture:** A complete `All: true` reconciliation owns authoritative inventory while the existing Docker event stream supplies low-latency transitions and coalesced reconciliation wakeups. Explicit baselines and bounded posture, process, and writable-layer observations feed a deterministic finding engine; safe APIs and React views expose inventory, security posture, and finding workflows. All Docker operations added here are read-only.

**Tech Stack:** Go 1.26, Docker Engine Go client, SQLite/modernc, stdlib `net/http`, React, TypeScript, Vite, Tailwind, Vitest, Go integration tests.

**Spec:** `docs/superpowers/specs/2026-10-29-container-security-monitoring-design.md`

## Global Constraints

- Core behavior must remain project-agnostic; no customer, repository, service ID, domain, provider, or Compose-project-specific branches.
- First release is alert-only: no Docker exec, stop, restart, network mutation, file copy, container/image deletion, or automatic containment.
- Container ID is instance identity; a reused name must never merge two instances.
- Operator approval, never first observation, establishes a trusted baseline.
- Persist no full process arguments, environment variables, arbitrary labels, file contents, raw Docker inspect objects, or sensitive mount source paths.
- Reconciliation defaults to 30 seconds, process scans to 30 seconds, and layer diffs to ten minutes; all are bounded and cancellable.
- Running-container stats remain a single sample per interval and unavailable stopped-container metrics serialize as `null`, not zero.
- Deterministic rules and explicit correlations control severity; no opaque anomaly score can independently produce a critical finding.
- Existing APIs stay backward compatible; new fields are additive and new mutations are admin-only and audited.
- Keep JSON slices non-nil, errors user-safe at HTTP boundaries, structured logs low-cardinality, and SQLite retention bounded.
- Every task follows TDD, adds generic fixtures, runs focused tests before broad verification, and commits one independently reviewable change.

## Review Focus

- A container disappearing between list and inspect must be treated as a lifecycle race, not a failed complete reconciliation; Task 3 pins this with `TestFleetInventoryReconcileSkipsVanishedContainer`.
- A container recreated under the same name must close the old instance and create a new identity; Tasks 2 and 3 pin this with `TestContainerStoreReconcileKeepsNameReuseDistinct` and `TestFleetInventoryReconcileNameReuse`.
- Secrets placed in environment variables, labels, mount sources, and process arguments must never enter SQLite, logs, API responses, or UI models; Tasks 1, 9, 10, 11, and 15 each test their boundary.
- Event loss, Docker restart, or a closed event stream must be repaired by full reconciliation while showing degraded freshness; Task 4 pins all three cases.
- A partial posture/process/diff collection failure must preserve prior open findings and mark coverage partial rather than resolving them; Tasks 8, 10, and 11 pin this behavior.

---

## File Structure

### New Go files

- `internal/model/containers.go` — durable container identity, classification, lifecycle, freshness, and query contracts.
- `internal/model/security.go` — policies, observations, finding episodes, severity, confidence, and summary contracts.
- `internal/docker/inventory.go` — complete list and safe normalized inspection adapters.
- `internal/docker/processes.go` — bounded process projection from Docker top.
- `internal/docker/diff.go` — bounded writable-layer change projection.
- `internal/service/fleet_inventory.go` — complete reconciliation loop and ownership/classification resolution.
- `internal/service/security.go` — policy evaluation, finding lifecycle, notification emission, and summary reads.
- `internal/service/security_posture.go` — normalized container-configuration checks.
- `internal/service/security_process.go` — process observation rules and process-count baseline.
- `internal/service/security_diff.go` — writable-layer rules and process/diff correlation.
- `internal/store/container_inventory.go` — transactional inventory reconciliation and lifecycle queries.
- `internal/store/security.go` — baselines, policies, observations, findings, and workflow persistence.
- `internal/http/container_inventory.go` — inventory detail plus approve/ignore handlers.
- `internal/http/security.go` — summary, findings, policy, and finding-workflow handlers.

### New migrations

- `migrations/0019_container_inventory.sql` — durable instance inventory, activation state, event identity extension, and instance linkage for fleet samples.
- `migrations/0020_container_security.sql` — baselines, policies, bounded observations, findings, and evidence metadata.

### New web files

- `web/src/components/ContainerDetail.tsx` — lifecycle, safe configuration, baseline, coverage, and findings detail.
- `web/src/components/SecurityScreen.tsx` — security summary and finding table.
- `web/src/components/security/FindingDetail.tsx` — bounded evidence and workflow actions.
- `web/src/components/security/PolicyDialog.tsx` — generic selector/rule editor.
- `web/src/lib/security.ts` — deterministic filtering, severity ordering, and presentation helpers.

### Existing files with focused changes

- `internal/docker/client.go`, `events.go`, `stats.go` — new narrow interfaces, expanded events, and nullable stopped-container semantics.
- `internal/service/fleet.go`, `resources.go`, `maintenance.go` — instance-linked sampling, compatibility reads, and retention.
- `internal/store/fleet.go`, `performance.go`, `maintenance.go` — additive IDs, event fields, and bounded cleanup.
- `internal/http/server.go`, `system.go`, `harness_test.go` — route wiring and service dependencies.
- `internal/config/config.go`, `config_test.go` — validated scan intervals and worker limits.
- `cmd/touchgrass/main.go` — constructors and lifecycle wiring only.
- `web/src/lib/api.ts`, `views.ts`, `App.tsx` — additive contracts and navigation.
- `web/src/components/Dashboard.tsx`, `FleetScreen.tsx`, `NotificationsScreen.tsx` — distinct service/container/security posture.
- `docs-site/fleet.md`, `docs-site/openapi.yaml`, `docs/threat-model.md`, `docs/ARCHITECTURE.md` — operator contract and security boundary.

---

### Task 1: Complete Docker inventory and safe inspection primitives

**Files:**
- Create: `internal/docker/inventory.go`
- Create: `internal/docker/inventory_test.go`
- Modify: `internal/docker/client.go:16-118`
- Modify: `internal/docker/client_integration_test.go:1-90`

**Interfaces:**
- Consumes: Docker Engine client already owned by `docker.Client`.
- Produces: `ListAll(ctx context.Context) ([]Container, error)` and `Inspect(ctx context.Context, id string) (Inspection, error)`; `Inspection` contains only normalized security-relevant fields and no environment variables or arbitrary labels.

- [ ] **Step 1: Write failing unit tests for all-state listing and normalized inspection**

Add table-driven tests named `TestFromSummaryMapsAllLifecycleStates`, `TestNormalizeInspectionExcludesSensitiveFields`, and `TestNormalizeInspectionRedactsMountSources`. Assert stopped/dead states survive mapping, only standard Compose labels remain, environment values are absent, and mount output contains type/destination but no host source.

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./internal/docker -run 'Test(FromSummaryMapsAllLifecycleStates|NormalizeInspection)' -count=1`

Expected: FAIL because `Inspection`, `normalizeInspection`, or `ListAll` does not exist.

- [ ] **Step 3: Implement the narrow Docker interfaces**

Add these signatures in `internal/docker/inventory.go`:

```go
type InventoryLister interface {
    ListAll(ctx context.Context) ([]Container, error)
    Inspect(ctx context.Context, id string) (Inspection, error)
}

func (c *Client) ListAll(ctx context.Context) ([]Container, error)
func (c *Client) Inspect(ctx context.Context, id string) (Inspection, error)
```

`ListAll` must use `container.ListOptions{All: true}`. Keep existing `List` and `SizedList` running-only so deploy, onboarding, log, and stats behavior does not change. Normalize inspection before returning it from the Docker package.

- [ ] **Step 4: Add an integration assertion for stopped containers**

Extend the Docker integration test to create a generic stopped container fixture, call `ListAll`, and assert its ID/state appears. Test cleanup owns removal of only the fixture it created.

- [ ] **Step 5: Run focused and integration-safe verification**

Run: `go test ./internal/docker -count=1`

Expected: PASS. Integration-tag tests may skip when Docker is unavailable.

- [ ] **Step 6: Commit**

```bash
git add internal/docker/client.go internal/docker/inventory.go internal/docker/inventory_test.go internal/docker/client_integration_test.go
git commit -m "feat(docker): add complete container inventory"
```

### Task 2: Durable container identity and transactional inventory store

**Files:**
- Create: `migrations/0019_container_inventory.sql`
- Create: `internal/model/containers.go`
- Create: `internal/store/container_inventory.go`
- Create: `internal/store/container_inventory_test.go`
- Modify: `internal/model/fleet.go:24-58`
- Modify: `internal/store/fleet.go:40-125`
- Test: `internal/store/db_test.go`

**Interfaces:**
- Consumes: normalized `docker.Container` and `docker.Inspection` projections from Task 1.
- Produces: `model.ContainerInstance`, `model.InventoryBatch`, `model.ContainerQuery`, `store.ContainerStore.Reconcile(ctx, batch)`, `Get(ctx, id)`, `List(ctx, query)`, and `Summary(ctx)`.

- [ ] **Step 1: Write migration and store tests first**

Add tests:

```go
func TestContainerStoreReconcilePersistsStoppedContainer(t *testing.T)
func TestContainerStoreReconcileKeepsNameReuseDistinct(t *testing.T)
func TestContainerStoreReconcileDoesNotMarkMissingOnIncompleteBatch(t *testing.T)
func TestContainerStoreListReturnsNonNilEmpty(t *testing.T)
func TestMigration0019PreservesLegacyFleetSamples(t *testing.T)
```

The name-reuse test writes two IDs with one name and asserts two lifecycle rows, with only the newer row current. The incomplete-batch test proves absence marking happens only after a successful complete list.

- [ ] **Step 2: Run focused store tests and verify failure**

Run: `go test ./internal/store -run 'Test(ContainerStore|Migration0019)' -count=1`

Expected: FAIL because the migration, models, and store do not exist.

- [ ] **Step 3: Add model enums and query contracts**

Define explicit `Unknown` zero sentinels, then `managed`, `approved_observed`, `unknown`, `ignored`, and `missing_expected` classifications. Define lifecycle/current/stale fields, nullable live metrics, safe image/configuration fingerprints, and bounded list filters. All JSON slices must initialize to empty.

- [ ] **Step 4: Add migration 0019 and transactional reconciliation**

The migration must create durable instance/activation storage, add container ID linkage to new fleet samples and events, and preserve legacy name-only rows. `ContainerStore.Reconcile` must upsert every observed ID and mark unseen current IDs absent in the same transaction only when `InventoryBatch.Complete` is true.

- [ ] **Step 5: Run store and migration tests**

Run: `go test ./internal/store ./migrations -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add migrations/0019_container_inventory.sql internal/model/containers.go internal/model/fleet.go internal/store/container_inventory.go internal/store/container_inventory_test.go internal/store/fleet.go internal/store/db_test.go
git commit -m "feat(store): persist container lifecycle inventory"
```

### Task 3: Authoritative inventory reconciliation loop

**Files:**
- Create: `internal/service/fleet_inventory.go`
- Create: `internal/service/fleet_inventory_test.go`
- Modify: `internal/service/resources.go:155-225`
- Modify: `internal/service/fleet.go:182-258`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/touchgrass/main.go:48-139,240-390`

**Interfaces:**
- Consumes: `docker.InventoryLister`, `store.ContainerStore`, `store.ServiceStore`, and existing ownership matching.
- Produces: `FleetInventory.Run(ctx)`, `Reconcile(ctx) (model.InventorySummary, error)`, `RequestReconcile()`, `Containers(ctx, query)`, and `Container(ctx, id)`.

- [ ] **Step 1: Write failing service tests**

Add `TestFleetInventoryReconcileAllStates`, `TestFleetInventoryReconcileSkipsVanishedContainer`, `TestFleetInventoryReconcileNameReuse`, `TestFleetInventoryMarksIncompleteOnListFailure`, `TestFleetInventoryBoundsInspectConcurrency`, and `TestFleetInventoryStopsOnCancellation`.

The vanished-container fixture returns not-found from `Inspect` after listing; assert the cycle completes without falsely marking unrelated instances missing. The concurrency fixture records peak inspector calls and asserts it never exceeds the configured worker limit.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/service -run 'TestFleetInventory' -count=1`

Expected: FAIL because `FleetInventory` does not exist.

- [ ] **Step 3: Implement the reconciler and coalesced trigger**

Use these construction contracts:

```go
type FleetInventoryConfig struct {
    Services      *store.ServiceStore
    Containers    *store.ContainerStore
    Docker        docker.InventoryLister
    Interval      time.Duration
    InspectLimit  int
    Logger        *slog.Logger
}

func NewFleetInventory(cfg FleetInventoryConfig) *FleetInventory
```

`Run` performs an immediate reconciliation, uses one ticker, and coalesces event wakeups through a capacity-one channel. Bound inspection workers and treat Docker not-found during inspection as a race.

Add `TOUCHGRASS_INVENTORY_INTERVAL` with default `30s` and
`TOUCHGRASS_INVENTORY_WORKERS` with default `4` and accepted range `1-32`.
Invalid values fail configuration loading; `cmd` passes only validated values.

- [ ] **Step 4: Link running resource samples to container IDs**

Keep `SizedList` and stats running-only. Add container ID to new `ContainerSample` rows, and make fleet reads join current inventory so stopped containers appear with null CPU/memory while legacy sample APIs remain compatible.

- [ ] **Step 5: Wire one owned background loop**

Construct `FleetInventory` in `cmd/touchgrass/main.go`, add it to `backgroundLoops`, increment the wait group exactly once, and pass it to the HTTP server later without placing business logic in `cmd`.

- [ ] **Step 6: Run focused plus race tests**

Run: `go test -race ./internal/service ./internal/store ./internal/docker -count=1`

Expected: PASS with no goroutine leaks or races.

- [ ] **Step 7: Commit**

```bash
git add internal/service/fleet_inventory.go internal/service/fleet_inventory_test.go internal/service/resources.go internal/service/fleet.go internal/config/config.go internal/config/config_test.go cmd/touchgrass/main.go
git commit -m "feat(fleet): reconcile complete container inventory"
```

### Task 4: Event-driven lifecycle updates and recovery

**Files:**
- Modify: `internal/docker/events.go:16-118`
- Modify: `internal/docker/events_test.go`
- Modify: `internal/model/performance.go:181-194`
- Modify: `internal/store/performance.go:399-460`
- Modify: `internal/service/fleet.go:20-160`
- Modify: `internal/service/fleet_inventory.go`
- Test: `internal/service/fleet_test.go`

**Interfaces:**
- Consumes: Task 3 `FleetInventory.RequestReconcile` and Task 2 container ID storage.
- Produces: expanded idempotent lifecycle history and event-triggered reconciliation without making events authoritative.

- [ ] **Step 1: Write event projection and recovery tests**

Extend allowed-action tests for create, pause, unpause, kill, rename, destroy/remove. Add `TestContainerEventCollectorRequestsCoalescedReconcile`, `TestContainerEventCollectorRepairsClosedStreamWithFullReconcile`, `TestContainerEventCollectorMarksCursorDegraded`, and `TestContainerEventCollectorPersistsContainerID`.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/docker ./internal/service -run 'Test(ContainerEvent|FromEvent)' -count=1`

Expected: FAIL for missing actions, IDs, and trigger behavior.

- [ ] **Step 3: Expand the bounded event contract**

Persist container ID and safe action metadata. Never persist exec command/arguments. Count exec lifecycle only as a bounded event type if Docker supplies it, and do not create a finding here.

- [ ] **Step 4: Trigger and recover through reconciliation**

After a persisted lifecycle event, call the injected zero-argument reconcile request function. Stream failure records a safe error category and requests a reconcile before bounded backoff. A successful full reconcile clears inventory staleness independently of event-stream freshness.

- [ ] **Step 5: Run race and cancellation tests**

Run: `go test -race ./internal/docker ./internal/service ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/docker/events.go internal/docker/events_test.go internal/model/performance.go internal/store/performance.go internal/service/fleet.go internal/service/fleet_inventory.go internal/service/fleet_test.go
git commit -m "feat(fleet): reconcile container lifecycle events"
```

### Task 5: Inventory and freshness HTTP APIs

**Files:**
- Create: `internal/http/container_inventory.go`
- Create: `internal/http/container_inventory_test.go`
- Modify: `internal/http/server.go:90-185`
- Modify: `internal/http/system.go:55-112`
- Modify: `internal/http/harness_test.go:90-230`
- Modify: `docs-site/openapi.yaml`

**Interfaces:**
- Consumes: Task 3 `FleetInventory` read methods.
- Produces: additive `GET /api/system/containers`, `GET /api/system/containers/{id}`, and collector freshness fields.

- [ ] **Step 1: Write failing handler contract tests**

Add `TestHandleContainersIncludesStoppedAndNullMetrics`, `TestHandleContainerDetailExcludesSensitiveInspection`, `TestHandleContainersReturnsEmptyArray`, `TestHandleContainersRejectsUnknownSort`, and `TestHandleContainersShowsStaleCollector`.

- [ ] **Step 2: Run handlers and verify failure**

Run: `go test ./internal/http -run 'TestHandleContainer' -count=1`

Expected: FAIL because the new server dependency/routes and safe projections do not exist.

- [ ] **Step 3: Add the server dependency and handlers**

Add `FleetInventory *service.FleetInventory` to HTTP config/server wiring. Keep the existing list response shape backward compatible while adding instance ID, classification, lifecycle, health, risk counts, first/last seen, and freshness. Detail responses use a dedicated safe DTO rather than marshaling store models directly.

- [ ] **Step 4: Document the API contract**

Update OpenAPI schemas with nullable live metrics, bounded filters/pagination, safe detail fields, and explicit 400/401/404 responses.

- [ ] **Step 5: Run HTTP and route-parity verification**

Run: `go test ./internal/http -count=1`

Expected: PASS, including existing auth route coverage.

- [ ] **Step 6: Commit**

```bash
git add internal/http/container_inventory.go internal/http/container_inventory_test.go internal/http/server.go internal/http/system.go internal/http/harness_test.go docs-site/openapi.yaml
git commit -m "feat(api): expose complete container inventory"
```

### Task 6: Fleet UI and truthful overview posture

**Files:**
- Create: `web/src/components/ContainerDetail.tsx`
- Create: `web/src/components/ContainerDetail.test.tsx`
- Modify: `web/src/lib/api.ts:1032-1060`
- Modify: `web/src/components/FleetScreen.tsx:130-600`
- Modify: `web/src/components/Dashboard.tsx:40-180`
- Modify: `web/src/App.tsx:1-330`

**Interfaces:**
- Consumes: Task 5 inventory list/detail API.
- Produces: all-state fleet filters, null-safe metrics, container detail navigation, and distinct managed/container/collector dashboard summaries.

- [ ] **Step 1: Write failing UI behavior tests**

Add tests asserting stopped containers render with `n/a` metrics, a critical unknown container cannot be summarized as healthy, stale collection displays degraded, name-reused instances have unique row keys, and filters cover All/Running/Stopped/Unknown/Risky.

- [ ] **Step 2: Run UI tests and verify failure**

Run: `cd web && npm test -- ContainerDetail FleetScreen Dashboard`

Expected: FAIL for missing types/components and old service-only summary behavior.

- [ ] **Step 3: Extend API types and FleetScreen**

Model nullable metrics and instance IDs exactly as OpenAPI defines them. Use instance ID for row/navigation identity. Keep Manage separate from container approval, which arrives in Task 7.

- [ ] **Step 4: Replace the misleading summary**

Dashboard must show separate Managed services, Containers, and Collector freshness summaries. No arithmetic may infer container health from the managed-service list.

- [ ] **Step 5: Run web verification**

Run: `cd web && npm run verify && npm test`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/api.ts web/src/components/FleetScreen.tsx web/src/components/Dashboard.tsx web/src/components/ContainerDetail.tsx web/src/components/ContainerDetail.test.tsx web/src/App.tsx
git commit -m "feat(web): show complete container fleet posture"
```

### Task 7: Activation, approval, ignore, and baseline workflow

**Files:**
- Create: `migrations/0020_container_security.sql`
- Create: `internal/model/security.go`
- Create: `internal/store/security.go`
- Create: `internal/store/security_test.go`
- Modify: `internal/store/container_inventory.go`
- Modify: `internal/service/fleet_inventory.go`
- Modify: `internal/http/container_inventory.go`
- Modify: `internal/http/container_inventory_test.go`
- Modify: `internal/model/audit.go`

**Interfaces:**
- Consumes: Task 2 inventory identity and Task 1 normalized inspection.
- Produces: activation state, approved baselines, generic policies, `ApproveContainer`, `IgnoreContainer`, classification precedence, and audited admin mutations.

- [ ] **Step 1: Write failing baseline and authorization tests**

Add `TestSecurityStoreActivationDoesNotAutoApproveExisting`, `TestApproveContainerRecordsFingerprintAndActor`, `TestIgnoreContainerRequiresReason`, `TestExpiredIgnoreReturnsUnknown`, `TestClassificationPrecedence`, and handler tests proving viewers receive 403 while admins produce audit entries.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/store ./internal/service ./internal/http -run 'Test(.*Container|SecurityStore|Classification).*' -count=1`

Expected: FAIL because activation, baselines, and mutation endpoints do not exist.

- [ ] **Step 3: Add security storage and deterministic fingerprinting**

Migration 0020 creates baselines, policies, findings, observations, and safe evidence metadata. Implement `FingerprintInspection(model.SafeInspection) string` from normalized security fields only; environment values, arbitrary labels, and raw mount sources must not be accepted by the input type.

- [ ] **Step 4: Implement classification mutations**

Expose admin-only:

```text
POST /api/system/containers/{id}/approve
POST /api/system/containers/{id}/ignore
```

Approve records image/config fingerprint and actor. Ignore requires a non-empty reason and explicit expiry or explicit non-expiring flag. Both record safe audit details.

- [ ] **Step 5: Run tests and migration verification**

Run: `go test -race ./internal/store ./internal/service ./internal/http -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add migrations/0020_container_security.sql internal/model/security.go internal/store/security.go internal/store/security_test.go internal/store/container_inventory.go internal/service/fleet_inventory.go internal/http/container_inventory.go internal/http/container_inventory_test.go internal/model/audit.go
git commit -m "feat(security): add container approval baselines"
```

### Task 8: Finding engine, episodes, notifications, and recovery

**Files:**
- Create: `internal/service/security.go`
- Create: `internal/service/security_test.go`
- Modify: `internal/store/security.go`
- Modify: `internal/store/security_test.go`
- Modify: `internal/model/metrics.go:50-68`
- Modify: `internal/http/server.go`
- Modify: `cmd/touchgrass/main.go`

**Interfaces:**
- Consumes: security observations and baselines from Task 7.
- Produces: `Security.Evaluate(ctx, evaluation)`, `Summary`, `Findings`, `Acknowledge`, `Resolve`, `Suppress`, a `security` notification kind, and collector-coverage state.

- [ ] **Step 1: Write failing episode-state tests**

Add `TestSecurityEvaluateDeduplicatesOpenEpisode`, `TestSecurityEvaluateCreatesRecovery`, `TestSecurityEvaluateDoesNotResolveOnPartialCoverage`, `TestSecurityEvaluateReopensNewEpisode`, `TestSecuritySuppressRequiresActorAndReason`, and `TestCriticalFindingCreatesOneSecurityNotification`.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/service ./internal/store -run 'TestSecurity' -count=1`

Expected: FAIL because the finding engine and workflow do not exist.

- [ ] **Step 3: Implement explicit enums and episode transactions**

Use zero-value `Unknown` sentinels for severity, confidence, category, and status. Store observation plus finding episode update transactionally. Fingerprint uses stable rule/container/baseline dimensions; timestamps and occurrence counts do not change identity.

- [ ] **Step 4: Add notification and SSE emission**

Critical and configured high findings insert one in-app `security` notification per new episode. Recovery closes the episode and emits one recovery event only if it previously opened. Notification failure must not roll back persisted finding state; it records safe degraded delivery status.

- [ ] **Step 5: Wire one security service**

Construct `Security` in `cmd`, inject it into scanners and HTTP, and do not add another unbounded background loop in this task.

- [ ] **Step 6: Run race tests**

Run: `go test -race ./internal/service ./internal/store ./internal/http -count=1`

Expected: PASS with concurrent duplicate evaluations producing one open episode.

- [ ] **Step 7: Commit**

```bash
git add internal/service/security.go internal/service/security_test.go internal/store/security.go internal/store/security_test.go internal/model/security.go internal/model/metrics.go internal/http/server.go cmd/touchgrass/main.go
git commit -m "feat(security): add durable finding episodes"
```

### Task 9: Security posture scanner and image/configuration drift

**Files:**
- Create: `internal/service/security_posture.go`
- Create: `internal/service/security_posture_test.go`
- Modify: `internal/service/fleet_inventory.go`
- Modify: `internal/service/security.go`

**Interfaces:**
- Consumes: Task 1 `Inspection`, Task 7 policies/baselines, and Task 8 `Security.Evaluate`.
- Produces: deterministic posture observations for privilege, namespace, daemon mount, root/read-only, capability, limit, port, mutable-tag, and fingerprint drift rules.

- [ ] **Step 1: Write a rule matrix before implementation**

Create table-driven tests covering every rule, allowed-policy override, and severity. Include `TestPostureScannerDoesNotPersistEnvironmentOrMountSource` and `TestPostureScannerDockerSocketIsCritical`.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/service -run 'TestPosture' -count=1`

Expected: FAIL because `PostureScanner` does not exist.

- [ ] **Step 3: Implement pure rule evaluation**

Define:

```go
func EvaluatePosture(instance model.ContainerInstance, inspection model.SafeInspection, policy model.SecurityPolicy) []model.SecurityObservation
```

Keep it pure and deterministic. It emits explicit rule IDs and evidence fields selected from safe normalized input only.

- [ ] **Step 4: Evaluate posture after successful new/changed inspection**

Inventory reconciliation invokes posture evaluation only after persistence of the instance. A failed inspection marks posture coverage partial and cannot resolve existing posture findings.

- [ ] **Step 5: Run focused and broad service tests**

Run: `go test -race ./internal/service ./internal/docker ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/security_posture.go internal/service/security_posture_test.go internal/service/fleet_inventory.go internal/service/security.go
git commit -m "feat(security): detect container posture drift"
```

### Task 10: Bounded process collection and suspicious-process rules

**Files:**
- Create: `internal/docker/processes.go`
- Create: `internal/docker/processes_test.go`
- Create: `internal/service/security_process.go`
- Create: `internal/service/security_process_test.go`
- Modify: `internal/service/fleet_inventory.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Consumes: Docker top API, inventory current-running set, policies, and Task 8 finding engine.
- Produces: `Processes(ctx, id) (ProcessSnapshot, error)`, normalized safe process observations, process-count baselines, and partial coverage state.

- [ ] **Step 1: Write parser/privacy tests**

Add `TestNormalizeProcessesKeepsPIDParentUserAndCommand`, `TestNormalizeProcessesDropsFullArguments`, `TestNormalizeProcessesRejectsControlCharacters`, and `TestNormalizeProcessesReturnsNonNilEmpty`.

- [ ] **Step 2: Write process-rule tests**

Add table cases for kernel-like command names, high-risk executable paths, allowed paths, parent/child policy mismatch, and sustained PID spike. `TestProcessScannerFailurePreservesFinding` must assert partial coverage prevents resolution.

- [ ] **Step 3: Run focused tests and verify failure**

Run: `go test ./internal/docker ./internal/service -run 'Test(NormalizeProcesses|ProcessScanner)' -count=1`

Expected: FAIL for missing adapter/scanner.

- [ ] **Step 4: Implement bounded adapter and scanner loop**

Add `ProcessLister` where consumed, cap rows per container, reject malformed headers safely, and never return or persist full arguments. The scanner uses a validated interval and worker limit, scans running containers only, and shares the top-level cancellation context.

- [ ] **Step 5: Wire validated configuration**

Add `TOUCHGRASS_SECURITY_PROCESS_INTERVAL` default `30s` and `TOUCHGRASS_SECURITY_WORKERS` with a conservative positive default and hard upper bound. Invalid values fail configuration load with precise errors.

- [ ] **Step 6: Run race, config, and leak-sensitive tests**

Run: `go test -race ./internal/docker ./internal/service ./internal/config -count=1`

Expected: PASS and scanner cancellation completes within the test deadline.

- [ ] **Step 7: Commit**

```bash
git add internal/docker/processes.go internal/docker/processes_test.go internal/service/security_process.go internal/service/security_process_test.go internal/service/fleet_inventory.go internal/config/config.go internal/config/config_test.go
git commit -m "feat(security): detect suspicious container processes"
```

### Task 11: Writable-layer scanning and deterministic correlation

**Files:**
- Create: `internal/docker/diff.go`
- Create: `internal/docker/diff_test.go`
- Create: `internal/service/security_diff.go`
- Create: `internal/service/security_diff_test.go`
- Modify: `internal/service/security.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Consumes: Docker diff API, approved writable-path policies, Task 10 process observations, and Task 8 findings.
- Produces: bounded layer observations plus explicit process/diff correlation yielding critical findings.

- [ ] **Step 1: Write diff normalization and privacy tests**

Add tests for added/changed/deleted kinds, maximum rows/path lengths, control-character rejection, non-nil empty results, and proof that no file content API is called or represented.

- [ ] **Step 2: Write rule and correlation tests**

Add `TestDiffScannerFlagsUserLocalExecutable`, `TestDiffScannerAllowsConfiguredCache`, `TestDiffScannerFlagsRuntimeModification`, `TestCorrelateAddedExecutableAndRunningProcessCritical`, `TestCorrelationExplainsContributingRules`, and `TestDiffFailurePreservesPriorFinding`.

- [ ] **Step 3: Run focused tests and verify failure**

Run: `go test ./internal/docker ./internal/service -run 'Test(Diff|Correlate)' -count=1`

Expected: FAIL because diff and correlation code do not exist.

- [ ] **Step 4: Implement bounded diff collection**

Expose `Diff(ctx context.Context, id string) ([]LayerChange, error)`. Schedule after start/image change and at the validated ten-minute default. Persist aggregate counts and only finding-relevant normalized paths.

- [ ] **Step 5: Implement explicit correlation rules**

Correlation accepts process and diff observations for one container/time window and emits a finding with ordered contributing rule IDs. No generic numeric anomaly score is permitted.

- [ ] **Step 6: Add configuration and run broad tests**

Add `TOUCHGRASS_SECURITY_DIFF_INTERVAL` default `10m`, validate its bounds, then run: `go test -race ./internal/docker ./internal/service ./internal/config ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/docker/diff.go internal/docker/diff_test.go internal/service/security_diff.go internal/service/security_diff_test.go internal/service/security.go internal/config/config.go internal/config/config_test.go
git commit -m "feat(security): correlate container layer drift"
```

### Task 12: Security APIs and workflow authorization

**Files:**
- Create: `internal/http/security.go`
- Create: `internal/http/security_test.go`
- Modify: `internal/http/server.go`
- Modify: `internal/http/harness_test.go`
- Modify: `docs-site/openapi.yaml`

**Interfaces:**
- Consumes: Task 8 security service reads/mutations and Task 7 policies.
- Produces: summary, finding, policy, acknowledge, resolve, and suppress endpoints with bounded DTOs.

- [ ] **Step 1: Write complete handler matrix tests**

Cover:

```text
GET    /api/security/summary
GET    /api/security/findings
GET    /api/security/findings/{id}
GET    /api/security/policies
POST   /api/security/findings/{id}/acknowledge
POST   /api/security/findings/{id}/resolve
POST   /api/security/findings/{id}/suppress
POST   /api/security/policies
PATCH  /api/security/policies/{id}
DELETE /api/security/policies/{id}
```

Tests assert viewer read/admin mutation separation, required actor/reason resolution from authenticated context, bounded pagination, allowlisted sort fields, non-nil arrays, and absence of raw commands/secrets.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/http -run 'TestHandleSecurity' -count=1`

Expected: FAIL because routes and DTOs do not exist.

- [ ] **Step 3: Implement handlers and safe DTO projections**

Parse/validate only in handlers, delegate business transitions to `Security`, and translate internal errors through existing user-safe response helpers. Every mutation is admin-wrapped in `server.go` and audited by the service.

- [ ] **Step 4: Update OpenAPI and auth route coverage**

Document enum values, pagination limits, mutation bodies, stale/partial coverage, and 400/401/403/404 responses. Extend the read-only viewer route test and admin mutation test matrix.

- [ ] **Step 5: Run HTTP verification**

Run: `go test -race ./internal/http ./internal/service ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/http/security.go internal/http/security_test.go internal/http/server.go internal/http/harness_test.go docs-site/openapi.yaml
git commit -m "feat(api): expose container security findings"
```

### Task 13: Security dashboard, finding detail, and policy workflows

**Files:**
- Create: `web/src/components/SecurityScreen.tsx`
- Create: `web/src/components/SecurityScreen.test.tsx`
- Create: `web/src/components/security/FindingDetail.tsx`
- Create: `web/src/components/security/FindingDetail.test.tsx`
- Create: `web/src/components/security/PolicyDialog.tsx`
- Create: `web/src/lib/security.ts`
- Create: `web/src/lib/security.test.ts`
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/lib/views.ts`
- Modify: `web/src/App.tsx`
- Modify: `web/src/components/ContainerDetail.tsx`
- Modify: `web/src/components/NotificationsScreen.tsx`

**Interfaces:**
- Consumes: Tasks 5, 7, and 12 APIs.
- Produces: Security navigation under Signals, accessible finding workflows, policy editor, container-linked evidence, and security notification presentation.

- [ ] **Step 1: Write helper and component tests first**

Test severity ordering, stable filtering, critical/high counts, stale/partial coverage, loading/error/empty states, keyboard-accessible finding detail, admin-only mutations, viewer read-only rendering, and narrow-screen table behavior. Assert full command arguments and raw inspection keys have no UI type or rendering path.

- [ ] **Step 2: Run UI tests and verify failure**

Run: `cd web && npm test -- security SecurityScreen FindingDetail ContainerDetail`

Expected: FAIL because security contracts/components are absent.

- [ ] **Step 3: Add typed API clients and presentation helpers**

Keep severity/status/category as string unions matching OpenAPI. API list methods normalize missing arrays to `[]`; mutations use existing same-origin request helpers.

- [ ] **Step 4: Implement Security and container-detail workflows**

Use text/icon plus color for severity. Show contributing rule IDs, bounded paths, fingerprints, coverage, first/last seen, and recommended response. Do not add containment buttons. Link findings to container details and security notifications to finding details.

- [ ] **Step 5: Implement generic policy dialog**

Selectors use image/Compose/service metadata only; no Ticketnation defaults. Require ignore/suppression reason and explicit expiry/non-expiring choice before submit.

- [ ] **Step 6: Run complete web verification**

Run: `cd web && npm run verify && npm test`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/SecurityScreen.tsx web/src/components/SecurityScreen.test.tsx web/src/components/security web/src/lib/security.ts web/src/lib/security.test.ts web/src/lib/api.ts web/src/lib/views.ts web/src/App.tsx web/src/components/ContainerDetail.tsx web/src/components/NotificationsScreen.tsx
git commit -m "feat(web): add container security workspace"
```

### Task 14: Retention, collector health, and operational limits

**Files:**
- Modify: `internal/model/maintenance.go`
- Modify: `internal/store/maintenance.go`
- Modify: `internal/store/maintenance_test.go`
- Modify: `internal/service/maintenance.go`
- Modify: `internal/service/maintenance_test.go`
- Modify: `internal/service/security.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `web/src/components/performance/CollectorStatus.tsx`

**Interfaces:**
- Consumes: inventory/security tables and collector state from prior tasks.
- Produces: bounded retention, collector incidents, partial/stale health, and safe degradation when limits are exceeded.

- [ ] **Step 1: Write retention and collector-episode tests**

Add tests proving raw observations/events trim before durable unresolved findings, resolved findings retain their configured window, evidence metadata respects byte caps, collector failure creates one episode, success creates one recovery, and evaluation failure cannot resolve existing findings.

- [ ] **Step 2: Run focused tests and verify failure**

Run: `go test ./internal/store ./internal/service ./internal/config -run 'Test(.*Retention|.*Collector|.*Maintenance)' -count=1`

Expected: FAIL for missing security retention classes and collector episodes.

- [ ] **Step 3: Add validated retention configuration and maintenance classes**

Add these validated settings inside the existing SQLite byte budget:

- `TOUCHGRASS_RETENTION_SECURITY_OBSERVATIONS`, default `168h`;
- `TOUCHGRASS_RETENTION_CONTAINER_EVENTS`, default `720h`;
- `TOUCHGRASS_RETENTION_SECURITY_FINDINGS`, default `2160h`, applied only to resolved findings;
- a fixed 32 KiB maximum for safe evidence metadata attached to one finding episode.

Maintenance deletes in bounded batches and reports safe table-class counters.

- [ ] **Step 4: Make degradation explicit**

When scan cost or storage pressure forces backoff, update collector status with source, coverage, last success, safe error category, and effective interval. Never silently drop work or report healthy freshness.

- [ ] **Step 5: Run maintenance and race verification**

Run: `go test -race ./internal/store ./internal/service ./internal/config -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/model/maintenance.go internal/store/maintenance.go internal/store/maintenance_test.go internal/service/maintenance.go internal/service/maintenance_test.go internal/service/security.go internal/config/config.go internal/config/config_test.go web/src/components/performance/CollectorStatus.tsx
git commit -m "feat(security): bound container monitoring retention"
```

### Task 15: Generic compromise replay, privacy gate, and release documentation

**Files:**
- Create: `internal/service/testdata/compromised-web-container.json`
- Create: `internal/service/security_integration_test.go`
- Create: `scripts/container-security-soak.sh`
- Modify: `Makefile`
- Modify: `docs-site/fleet.md`
- Modify: `docs-site/troubleshooting.md`
- Modify: `docs/threat-model.md`
- Modify: `docs/ARCHITECTURE.md`
- Modify: `README.md`

**Interfaces:**
- Consumes: the complete inventory/security feature.
- Produces: reusable non-Ticketnation validation evidence, privacy regression gate, soak measurements, operator runbook, and release readiness criteria.

- [ ] **Step 1: Add the generic incident replay fixture and integration test**

The fixture represents a generic web container with an added executable under `/home/app/.local/bin`, a kernel-like process name, a suspicious parent/child relationship, and an application runtime modification. It contains no real customer name, production ID, IP, domain, credential, or binary content.

Test `TestSecurityReplayCorrelatesOneCriticalFinding` and assert exactly one critical episode with explicit contributing rules. Add `TestSecurityReplayDoesNotPersistSecrets` with canary secrets placed in environment, labels, mount source, and process arguments; scan the SQLite file, logs, JSON responses, and rendered fixture output for absence.

- [ ] **Step 2: Run integration tests and verify behavior**

Run: `go test -tags=integration ./internal/service -run 'TestSecurityReplay' -count=1`

Expected: PASS with one critical finding and zero canary leakage. Any failure is a release-blocking gap in Tasks 1-14 and must be fixed in the owning task before continuing.

- [ ] **Step 3: Add a bounded soak script and Make target**

The script creates up to 100 generic fixture containers or deterministic fakes when Docker is unavailable, measures reconciliation/event delay, CPU, RSS, goroutine count, and SQLite growth, and exits non-zero when the spec targets are exceeded. It cleans up only resources bearing its unique test label.

- [ ] **Step 4: Update operator and threat documentation**

Document all-state inventory, activation review, unknown/missing states, security findings, polling limitations, null stopped metrics, collector staleness, read-only Docker operations, Docker-socket root equivalence, retention, and the explicit absence of automatic containment.

- [ ] **Step 5: Run the complete validation matrix**

Run:

```bash
make fmt
make vet
make lint
make test
make test-race
make test-int
cd web && npm run verify && npm test
./scripts/container-security-soak.sh
```

Expected: every command PASS; soak report meets p95 reconciliation under five seconds, event update under five seconds, added CPU at or below two percent of one core, added RSS at or below 64 MiB, and bounded SQLite growth.

- [ ] **Step 6: Verify the production migration path on a copy**

Create an online backup of a representative pre-0019 SQLite database, apply migrations to the copy, start Touch Grass in shadow mode, verify legacy fleet/performance/deploy data, and confirm rollback means restoring the untouched backup plus previous binary. Record evidence without production secrets.

- [ ] **Step 7: Commit**

```bash
git add internal/service/testdata/compromised-web-container.json internal/service/security_integration_test.go scripts/container-security-soak.sh Makefile docs-site/fleet.md docs-site/troubleshooting.md docs/threat-model.md docs/ARCHITECTURE.md README.md
git commit -m "test(security): validate container compromise detection"
```

---

## Phase Gates

### Gate A: inventory shadow release after Task 6

- Deploy with inventory/security mutations disabled.
- Confirm `docker ps -a` parity, identity correctness, stale-state behavior, and resource overhead.
- Review every imported container before enabling unknown-container notifications.

### Gate B: baseline and posture release after Task 9

- Enable activation, approval, ignore, and posture findings.
- Run posture findings in warning-only shadow mode for at least one normal deployment cycle.
- Review false positives and approve generic policy changes through audited configuration.

### Gate C: runtime integrity release after Task 15

- Enable process and diff scanning gradually.
- Require generic compromise replay, privacy gate, soak gate, backup/migration rehearsal, and full test matrix.
- Keep containment disabled. Critical findings notify and preserve bounded evidence only.

## Production Rollback

- Before each migration phase, take and verify an online SQLite backup.
- Preserve the previous Touch Grass binary/image and environment configuration.
- Rollback stops the new binary, restores the verified pre-migration SQLite backup, and starts the previous binary; forward-only migrations are never manually reversed in place.
- Touch Grass failure must not alter monitored containers, serving traffic, deploy scripts, or the SSH fallback path.
