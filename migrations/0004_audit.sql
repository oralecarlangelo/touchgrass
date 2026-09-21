-- 0004_audit: append-only action log. The application exposes insert/list
-- only; no update, delete, or retention path may target this table.
CREATE TABLE IF NOT EXISTS audit (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT REFERENCES services(id),
  actor TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('cutover', 'rollback', 'login')),
  result TEXT NOT NULL CHECK (result IN ('success', 'failure')),
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_audit_service_created ON audit (service_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_action_created ON audit (action, created_at);
