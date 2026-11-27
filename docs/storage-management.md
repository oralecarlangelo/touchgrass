# Storage management

Operations → Storage (`#/images`, preserving existing links) combines host filesystem capacity, Docker storage accounting, the image inventory and an optional S3-compatible bucket manager. The dashboard disk label opens this view.

## Disk accounting and cleanup

`GET /api/storage/usage` requires `host.read` and fleet scope. It returns the latest background snapshot immediately, including explicit cold, stale and collection-failure freshness metadata. The expensive Docker disk-usage scan runs at most every five minutes with a dedicated single-permit admission guard and a 45-second daemon deadline, so it cannot occupy the fast Docker health/request budget. The response contains `sampled_at`, `image_layers_bytes` and largest-first `entries` (`id`, `name`, `kind`, nullable `size_bytes`, `status`). A failed refresh preserves the prior snapshot and marks it stale; cold state is never presented as a current zero.

Image layer usage counts shared layers once. Per-image sizes and cache entries can overlap and must not be summed into host usage. Container entries include writable layers only. Bind mounts, container logs, host logs, backups and filesystem metadata are not measured here. The host and Docker data root may be on different filesystems. The UI deliberately does not compute a misleading “other” total.

The existing admin-only dangling-image cleanup remains available with confirmation and actual bytes reclaimed after execution. Logical dangling size is labelled an estimate. Tagged release images, volumes and bucket data are not included in this cleanup. No scheduled cleanup, build-cache purge, volume deletion, or arbitrary host-file deletion is introduced.

## Connect MinIO or another S3-compatible store

An administrator can connect MinIO or another S3-compatible service directly from Operations → Storage. The form suggests MinIO's S3 API port (`9000`), verifies the credentials, discovers visible buckets, and requires an explicit allowlist selection before saving. Scoped credentials without account-wide bucket enumeration can instead supply an explicit allowlist, which the server verifies one bucket at a time. Credentials are submitted only to the authenticated `host.manage` route, are cleared from the form after save/cancel, and are never returned by the API or written to audit details. The endpoint only needs to be reachable by Touch Grass: authenticated uploads and downloads stream through bounded same-origin routes, so a private loopback MinIO endpoint works without exposing credentials or changing bucket ACLs.

The saved connection is an atomic mode-`0600` JSON file beside the SQLite database by default. This deliberately preserves compatibility with the existing operator-managed secret-file format and avoids putting retrievable credentials in SQLite. Set `TOUCHGRASS_OBJECT_STORAGE_FILE` to choose another path; an explicitly configured missing or invalid file still fails startup clearly. Operators using credentials that cannot enumerate account buckets can continue to provision this file manually with a narrow bucket allowlist:

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

Use the S3 API origin, not the MinIO console URL. It must be reachable from Touch Grass; use HTTPS when it crosses a network. Keep credentials scoped to the listed buckets. Read-only access needs ListBucket and GetObject; opt-in writes additionally need PutObject and DeleteObject. No account-wide bucket enumeration, CORS, policy editing or root credentials are required. `buckets` is an operator allowlist, not an assertion that each bucket exists; setup checks access through S3.

All bucket routes require `host.manage` and fleet scope, including setup, browsing and download. Viewer/developer accounts cannot inspect bucket contents. Setting `writable: true` enables object mutations for admins. Connections saved from the UI become active immediately; externally edited files are loaded at startup.

## Bucket actions

- `GET /api/storage/buckets`: `{configured, writable, buckets}`; no secrets.
- `POST /api/storage/connection/discover`: validates a bounded connection request and returns visible bucket names; upstream failures return a generic response without credential or provider details.
- `POST /api/storage/connection`: re-verifies discovery, rejects unknown or duplicate selections, atomically saves the connection and activates it without a restart.
- `GET /api/storage/objects?bucket=…&prefix=…&after=…`: one ListObjectsV2 page of at most 100 objects/common prefixes, with an opaque `next` continuation token. The request has a five-second deadline and a 1 MiB response bound. Search is by key prefix, not substring. Page bytes and counts are clearly labelled; no full-bucket size scan runs during browsing.
- `POST /api/storage/action`: JSON `{action, bucket, key, filename?, confirmation?}`. The body is limited to 16 KiB; every action validates the bucket allowlist.
- `GET /api/storage/content`: streams one allowlisted object as an authenticated attachment with a two-minute deadline and bounded two-transfer admission. Object content is never rendered inline.
- `PUT /api/storage/content`: streams 1–104857600 bytes to an allowlisted writable bucket without buffering the object in memory. The generated UUID key avoids overwriting existing assets, and requested/completed/failed phases are audited. Existing application URLs do not automatically change to reference the new object.
- `folder`: creates a zero-byte key ending in `/`, with an If-None-Match precondition. S3 folders are prefixes, not filesystem directories.
- `delete`: requires `confirmation` equal to the full nonempty `key`, enforced by the backend. Deletes one current key only; never recursively deletes a prefix, removes a bucket, purges historical versions or bypasses object lock. Versioned deletion may add a delete marker rather than free disk space. The UI offers deletion for files only.

An audit intent must persist before issuing a grant or executing a mutation. Completion or grant issuance is then recorded, including the generated upload key. A result-audit failure is logged; the persisted intent remains available for investigation. Signed URLs and credentials are excluded from audit details.

## Verification and deployment

Entry points: `ImagesScreen`, `StoragePanel`, `BucketManager`; API client: `web/src/lib/api.ts`; route policy: `internal/http/routes.go`; storage adapters: `internal/docker/storage.go` and `internal/objectstore/`; audit migration: `0026_storage_audit.sql`.

Tests cover unknown sizes, shared cache accounting, persistent-volume labels, bucket allowlists, read-only writes, bounded listings, pagination, unique upload grants and request cancellation. Route policy matrices and OpenAPI cover all new endpoints. Migration tests preserve existing audit records.

Before enabling writes on a real store, exercise list/pagination, upload/download, folder creation and deletion against a disposable bucket, verify CORS, and check audit receipts. Production data has not been modified by preparing this change. Live host diagnosis, full directory scans, bucket-wide size/count inventories, retention-policy management, bulk operations and version-aware recovery remain outside this initial implementation.
