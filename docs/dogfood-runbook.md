# Dogfood runbook (S9): tn-api staging, then production

Staging-first rollout of `@touchgrass/node` into tn-api with promotion
criteria, tuning knobs, and rollback. Run on the EC2 host beside the
staging compose stack.

## 0. Prerequisites

- touchgrass serving on the box (`touchgrass serve`, `TOUCHGRASS_DB`
  set, admin password in env, not on disk).
- Admin session cookie for key minting:
  `curl -c jar -X POST :8080/api/auth/login -d '{"password":"..."}'`.
- `node -e "console.log(require('./package.json').type)"` in tn-api to
  pick the import style (SDK ships ESM `dist/index.js`).

## 1. Mint a staging key

```bash
curl -b jar -X POST :8080/api/keys \
  -d '{"service_id":"tn-api","sample_rate":1}'
# → {"id":1,"key":"tg_...","key_prefix":"tg_XXXXXXXX",...}
```

Record the plaintext once; the API never shows it again. Start at
`sample_rate: 1`; tune down in step 5.

## 2. Install the SDK in tn-api staging

```bash
npm install @touchgrass/node
```

Earliest in the entrypoint (before routes boot):

```ts
import { init, addBreadcrumb } from '@touchgrass/node';

init({
  endpoint: 'http://127.0.0.1:8080', // touchgrass on the same box
  key: process.env.TOUCHGRASS_KEY!, // staging key, env only
  release: process.env.APP_VERSION ?? 'staging',
  scrub: [/token=[^&\s]+/g], // team-reviewed PII patterns (step 5)
});
```

Add breadcrumbs at route boundaries (`addBreadcrumb({ category: 'http',
message: 'POST /checkout' })`) — cheap, high-signal in issue detail.

## 3. Verify (AC-4, first half)

1. Deploy staging, force one exception (staging-only debug route or
   `node -e "require('tn-api-entry'); throw new Error('dogfood-probe')"`
   against staging config — never in prod).
2. Within 60s the Issues view must show one grouped issue with trace +
   release; `GET /api/issues?service_id=tn-api` returns it.
3. Confirm the notification center holds the `issue` note (needs a
   `new_issue` rule for tn-api — create one in the Issues view first).

## 4. Promote to production

Only when staging has been quiet and stable (no SDK-attributed
errors, no event-loop or latency change) through representative
traffic:

1. Mint a **separate** prod key (`sample_rate: 1` initially).
2. Ship the same init with the prod key + prod release tag.
3. Watch the first hour: issue rate, spike alerts, occurrence cap.

## 5. Tune after a week of real volume

- **Sampling**: drop noisy-service keys below 1 via a fresh key +
  revoke the old one (`POST /api/keys/{id}/revoke`). Verify counts
  fall proportionally.
- **Scrub**: review captured payloads with the team; extend `scrub`
  patterns for any PII that slipped through (keys, tokens, emails).
  Server never logs payloads; scrub runs before send.
- **Alert thresholds**: adjust spike `threshold`/`window_secs` until
  pages mean action; delete rules that only fire noise.
- **Caps**: confirm `occurrences` stays under
  `TOUCHGRASS_INGEST_MAX_OCCURRENCES` per service and error retention
  (`TOUCHGRASS_RETENTION_ERRORS`) trims on schedule.

## 6. Rollback

- SDK trouble: remove the `init(...)` call (or the package) and
  redeploy — the app is unchanged without it (AC-6, proven by the
  fail-open suite: no sync IO, no throws, unref'd timers).
- Key compromise: `POST /api/keys/{id}/revoke` — in-flight SDKs get
  401s and drop best-effort; mint a replacement.

## 7. AC-4 sign-off

- [ ] Forced staging exception grouped in UI < 60s.
- [ ] Ingestion outage leaves tn-api unaffected (stop touchgrass,
      exercise staging, compare latency/behavior; SDK suite already
      proves the mechanism).

## 8. S10 log validation (EC2)

1. Emit a known marker from tn-api staging
   (`node -e "console.error('dogfood-log-probe-<ts>')"`, or a staging
   debug route) and note the wall-clock time.
2. Within 30s, `GET
   /api/logs?service_id=tn-api&q=dogfood-log-probe-<ts>` must return
   the stderr line; the Logs view live-tail must show it without a
   manual refresh.
3. Open the line's context (`GET /api/logs/{id}`): ±20 neighbors
   around the anchor, oldest-first.
4. Confirm caps hold: `log_lines` per service stays under
   `TOUCHGRASS_LOGS_MAX_LINES` and `TOUCHGRASS_RETENTION_LOGS`
   trims on schedule (watch for `retention trimmed logs` in stdout).
