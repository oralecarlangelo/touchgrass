-- 0015_users: multi-user auth with admin/viewer roles (S28).
-- Viewers are read-only; admins hold every power the single v1
-- credential held. The bootstrap admin row is seeded from
-- TOUCHGRASS_ADMIN_PASSWORD on first boot, never by this migration.
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK (role IN ('admin', 'viewer')),
  disabled_at TEXT,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- Audit user management. SQLite cannot alter CHECK constraints, so the
-- audit table is rebuilt (same pattern as 0006, 0011, 0013, and 0014).
CREATE TABLE audit_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT REFERENCES services(id),
  actor TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('cutover', 'rollback', 'login', 'deploy', 'db_backup', 'db_restore', 'service_create', 'service_delete', 'user_create', 'user_update', 'user_delete')),
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
