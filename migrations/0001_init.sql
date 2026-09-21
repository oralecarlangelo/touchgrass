-- 0001_init: core service inventory tables.
CREATE TABLE IF NOT EXISTS services (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  strategy TEXT NOT NULL CHECK (strategy IN ('bluegreen', 'recreate')),
  compose_project TEXT NOT NULL,
  compose_dir TEXT NOT NULL,
  config TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_services_name ON services (name);
