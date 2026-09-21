-- 0008_issues: fingerprinted error groups over occurrences. Issues are
-- upserted at ingest time; releases ride a child table that cascades.
-- occurrences.issue_id stays null for pre-S8 rows (no backfill: the Go
-- grouper owns fingerprinting and dogfood starts in S9).
CREATE TABLE IF NOT EXISTS issues (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  fingerprint TEXT NOT NULL,
  title TEXT NOT NULL,
  first_seen TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  last_seen TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  notified_at TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  UNIQUE (service_id, fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_issues_service_seen ON issues (service_id, last_seen);
CREATE TABLE IF NOT EXISTS issue_releases (
  issue_id INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  release TEXT NOT NULL,
  PRIMARY KEY (issue_id, release)
);
CREATE TABLE IF NOT EXISTS issue_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  kind TEXT NOT NULL CHECK (kind IN ('new_issue', 'spike')),
  threshold INTEGER NOT NULL DEFAULT 0,
  window_secs INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_issue_rules_service ON issue_rules (service_id);
ALTER TABLE occurrences ADD COLUMN issue_id INTEGER REFERENCES issues(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_occurrences_issue ON occurrences (issue_id);
