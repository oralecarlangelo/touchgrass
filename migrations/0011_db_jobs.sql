-- 0011_db_jobs: postgres backup/restore job history (S22).
-- db_jobs records one row per backup or restore run: kind, target backup
-- name, running/success/failed status, detail, and start/finish times.
-- History caps at the newest 50 rows (trimmed on insert); kinds and
-- statuses are an explicit CHECK because the API filters on stored values.
CREATE TABLE IF NOT EXISTS db_jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  kind TEXT NOT NULL CHECK (kind IN ('backup', 'restore')),
  target TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('running', 'success', 'failed')),
  detail TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL,
  finished_at TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_db_jobs_status ON db_jobs (status);

-- Extend the audit trail with the database job actions. SQLite cannot
-- alter CHECK constraints, so the table is rebuilt (same pattern as 0006).
CREATE TABLE audit_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT REFERENCES services(id),
  actor TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('cutover', 'rollback', 'login', 'deploy', 'db_backup', 'db_restore')),
  result TEXT NOT NULL CHECK (result IN ('success', 'failure')),
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO audit_new (id, service_id, actor, action, result, detail, created_at)
  SELECT id, service_id, actor, action, result, detail, created_at FROM audit;
DROP TABLE audit;
ALTER TABLE audit_new RENAME TO audit;
CREATE INDEX IF NOT EXISTS idx_audit_service_created ON audit (service_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_action_created ON audit (action, created_at);
