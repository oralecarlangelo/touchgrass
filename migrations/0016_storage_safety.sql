-- 0016_storage_safety: persisted collector progress and retention indexes.
-- Collector errors are safe categories only; rejected payloads never belong here.
CREATE TABLE IF NOT EXISTS collector_cursors (
  source TEXT PRIMARY KEY,
  identity TEXT NOT NULL DEFAULT '',
  offset INTEGER NOT NULL DEFAULT 0,
  last_recorded_at TEXT,
  last_success_at TEXT,
  last_error TEXT NOT NULL DEFAULT '',
  dropped_records INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_occurrences_created_at ON occurrences (created_at);
CREATE INDEX IF NOT EXISTS idx_issues_last_seen ON issues (last_seen);
CREATE INDEX IF NOT EXISTS idx_log_lines_ts ON log_lines (ts);
CREATE INDEX IF NOT EXISTS idx_sdk_logs_ts ON sdk_logs (ts);
CREATE INDEX IF NOT EXISTS idx_deploys_created_at ON deploys (created_at);
CREATE INDEX IF NOT EXISTS idx_deploy_probes_sampled_at ON deploy_probes (sampled_at);
CREATE INDEX IF NOT EXISTS idx_db_jobs_created_at ON db_jobs (created_at);
