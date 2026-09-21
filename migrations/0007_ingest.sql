-- 0007_ingest: SDK error ingestion. api_keys holds one revocable,
-- hashed key per service; occurrences holds raw error reports ahead of
-- Sprint 8 fingerprinting. Occurrences are count-capped per service at
-- write time and age-trimmed with error retention.
CREATE TABLE IF NOT EXISTS api_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  key_hash TEXT NOT NULL UNIQUE,
  key_prefix TEXT NOT NULL,
  sample_rate REAL NOT NULL DEFAULT 1,
  revoked_at TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_api_keys_service ON api_keys (service_id);
CREATE TABLE IF NOT EXISTS occurrences (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  type TEXT NOT NULL,
  message TEXT NOT NULL,
  stack TEXT NOT NULL DEFAULT '[]',
  breadcrumbs TEXT NOT NULL DEFAULT '[]',
  release TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_occurrences_service_id ON occurrences (service_id, id);
