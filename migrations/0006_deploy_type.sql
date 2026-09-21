-- 0006_deploy_type: allow the generic 'deploy' type/action so the recreate
-- strategy runs through the same engine, history, and audit trail.
-- SQLite cannot alter CHECK constraints, so both tables are rebuilt.
CREATE TABLE deploys_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  sha TEXT NOT NULL,
  actor TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('cutover', 'rollback', 'manual', 'deploy')),
  outcome TEXT NOT NULL CHECK (outcome IN ('success', 'failure')),
  started_at TEXT,
  finished_at TEXT,
  duration_secs INTEGER,
  downtime_secs REAL,
  notes TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO deploys_new (id, service_id, sha, actor, type, outcome, started_at, finished_at, duration_secs, downtime_secs, notes, created_at)
  SELECT id, service_id, sha, actor, type, outcome, started_at, finished_at, duration_secs, downtime_secs, notes, created_at FROM deploys;
DROP TABLE deploys;
ALTER TABLE deploys_new RENAME TO deploys;
CREATE INDEX IF NOT EXISTS idx_deploys_service ON deploys (service_id);

CREATE TABLE audit_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT REFERENCES services(id),
  actor TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('cutover', 'rollback', 'login', 'deploy')),
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
