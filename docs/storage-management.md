# Storage management

Operations → Storage (`#/images`, preserving existing links) combines host filesystem capacity, Docker storage accounting, the image inventory and an optional S3-compatible bucket manager. The dashboard disk label opens this view.

## Disk accounting and cleanup

`GET /api/storage/usage` requires `host.read` and fleet scope. It queries Docker's disk-usage API on demand under the existing daemon concurrency guard, with a five-second timeout. It returns `sampled_at`, `image_layers_bytes` and largest-first `entries` (`id`, `name`, `kind`, nullable `size_bytes`, `status`). Failure is shown as unavailable, not zero usage.

Image layer usage counts shared layers once. Per-image sizes and cache entries can overlap and must not be summed into host usage. Container entries include writable layers only. Bind mounts, container logs, host logs, backups and filesystem metadata are not measured here. The host and Docker data root may be on different filesystems. The UI deliberately does not compute a misleading “other” total.

The existing admin-only dangling-image cleanup remains available with confirmation and actual bytes reclaimed after execution. Logical dangling size is labelled an estimate. Tagged release images, volumes and bucket data are not included in this cleanup. No scheduled cleanup, build-cache purge, volume deletion, or arbitrary host-file deletion is introduced.

## Connect MinIO or another S3-compatible store

Create a server-side JSON connection file with permissions `0600`, mount it read-only into Touchgrass, and set `TOUCHGRASS_OBJECT_STORAGE_FILE` to its absolute path. Missing configuration leaves the bucket manager disabled. Invalid configuration fails startup clearly. The secret key is not stored in SQLite, returned by the API, or placed in browser state; the operator manages the file as a secret. Short-lived signed grants necessarily include the S3 access-key identifier, scope and signature.

```json
{
  "endpoint": "https://objects.example.com",
  "region": "us-east-1",
  "access_key": "REPLACE_WITH_SCOPED_ACCESS_KEY",
  "secret_key": "REPLACE_WITH_SCOPED_SECRET_KEY",
  "buckets": ["example-assets", "example-backups"],
  "writable": false
}
```

Use the S3 API origin, not the MinIO console URL. It must be reachable from both Touchgrass and the browser; use HTTPS in production. Configure CORS on the bucket for the Touchgrass UI origin and POST uploads. Keep credentials scoped to the listed buckets. Read-only access needs ListBucket and GetObject; opt-in writes additionally need PutObject and DeleteObject. No account-wide bucket enumeration, policy editing or root credentials are required. `buckets` is an operator allowlist, not an assertion that each bucket exists; opening it checks access through S3.

All bucket routes require `host.manage` and fleet scope, including browsing and download. Viewer/developer accounts cannot inspect bucket contents. Setting `writable: true` enables object mutations for admins; restart is needed after connection changes.

## Bucket actions

- `GET /api/storage/buckets`: `{configured, writable, buckets}`; no secrets.
- `GET /api/storage/objects?bucket=…&prefix=…&after=…`: one ListObjectsV2 page of at most 100 objects/common prefixes, with an opaque `next` continuation token. The request has a five-second deadline and a 1 MiB response bound. Search is by key prefix, not substring. Page bytes and counts are clearly labelled; no full-bucket size scan runs during browsing.
- `POST /api/storage/action`: JSON `{action, bucket, key, filename?, confirmation?}`. The body is limited to 16 KiB; every action validates the bucket allowlist.
- `download`: returns a five-minute signed attachment URL. No object content is rendered inside Touchgrass.
- `upload`: `key` is a destination prefix, `filename` is the source name. Returns `{url, fields, key}` for a signed POST limited to 1–104857600 bytes and five minutes. The generated UUID key avoids overwriting existing assets. The browser uploads directly, then refreshes the listing. An issued grant is audited, but issuance does not prove the browser finished uploading. Existing application URLs do not automatically change to reference the new object.
- `folder`: creates a zero-byte key ending in `/`, with an If-None-Match precondition. S3 folders are prefixes, not filesystem directories.
- `delete`: requires `confirmation` equal to the full nonempty `key`, enforced by the backend. Deletes one current key only; never recursively deletes a prefix, removes a bucket, purges historical versions or bypasses object lock. Versioned deletion may add a delete marker rather than free disk space. The UI offers deletion for files only.

An audit intent must persist before issuing a grant or executing a mutation. Completion or grant issuance is then recorded, including the generated upload key. A result-audit failure is logged; the persisted intent remains available for investigation. Signed URLs and credentials are excluded from audit details.

## Verification and deployment

Entry points: `ImagesScreen`, `StoragePanel`, `BucketManager`; API client: `web/src/lib/api.ts`; route policy: `internal/http/routes.go`; storage adapters: `internal/docker/storage.go` and `internal/objectstore/`; audit migration: `0025_storage_audit.sql`.

Tests cover unknown sizes, shared cache accounting, persistent-volume labels, bucket allowlists, read-only writes, bounded listings, pagination, unique upload grants and request cancellation. Route policy matrices and OpenAPI cover all new endpoints. Migration tests preserve existing audit records.

Before enabling writes on a real store, exercise list/pagination, upload/download, folder creation and deletion against a disposable bucket, verify CORS, and check audit receipts. Production data has not been modified by preparing this change. Live host diagnosis, full directory scans, bucket-wide size/count inventories, retention-policy management, bulk operations and version-aware recovery remain outside this initial implementation.
