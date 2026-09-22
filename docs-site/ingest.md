# Ingest API

`POST /api/ingest` is the only endpoint SDKs need. It is public by
design — the per-service key in the header is the credential — so app
servers never need admin sessions.

## Request

```http
POST /api/ingest
X-Touchgrass-Key: tg_...
Content-Type: application/json

{
  "type": "exception",
  "message": "connect ECONNREFUSED 10.0.0.4:5432",
  "stack": [
    { "function": "checkout", "file": "/app/orders.ts", "line": 42, "column": 11 }
  ],
  "breadcrumbs": [
    { "at": "2026-09-22T10:00:00Z", "category": "http", "message": "POST /checkout" }
  ],
  "release": "v1.2.3"
}
```

| Field         | Meaning                                                              |
| ------------- | -------------------------------------------------------------------- |
| `type`        | `exception` or `message`                                             |
| `message`     | Human-readable summary; templated into the issue fingerprint         |
| `stack`       | V8-style frames; top three join the fingerprint                      |
| `breadcrumbs` | Trail, oldest first; shown on the issue                              |
| `release`     | Release tag; tracked per issue so regressions map to deploys         |

Bodies are capped at 1MB (`413` past it). Payloads are never logged:
rejections log the key prefix at most.

## Response

```http
202 Accepted

{ "sampled": true }
```

`sampled: false` means the key's sampling rate dropped this report —
normal at rates below 1, not an error. Unknown or revoked keys get
`401` without a body that says which.

## Keys

Mint one key per service (and separate keys per environment) with an
initial `sample_rate` of 1:

```http
POST /api/keys          (admin session)
{ "service_id": "my-api", "sample_rate": 1 }
```

The plaintext key is returned once. List shows prefixes only; revoke
is immediate — in-flight SDKs get `401`s and drop best-effort:

```http
GET  /api/keys?service_id=my-api
POST /api/keys/{id}/revoke   -> 204
```

## Sampling and caps

Sampling is evaluated per report with crypto randomness at write
time. To quiet a noisy service, mint a lower-rate key and revoke the
old one — counts fall proportionally.

Two guards bound storage per service: `TOUCHGRASS_INGEST_MAX_OCCURRENCES`
(count cap at write time, default 10000) and `TOUCHGRASS_RETENTION_ERRORS`
(age trim on schedule, default 720h). Issues whose occurrences all age
out trim with them.
