-- 0021_env: encrypted env entries, applied revisions, apply runs, and the
-- last validator outcome per service (S29 environment & secrets pillar).
CREATE TABLE env_entries (
  service_id TEXT NOT NULL REFERENCES services(id),
  key TEXT NOT NULL,
  ciphertext BLOB NOT NULL,
  nonce BLOB NOT NULL,
  key_id TEXT NOT NULL,
  is_secret INTEGER NOT NULL DEFAULT 0,
  state TEXT NOT NULL CHECK (state IN ('applied', 'added', 'changed', 'removed')),
  updated_by TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (service_id, key)
);
CREATE INDEX idx_env_entries_service ON env_entries (service_id);

CREATE TABLE env_revisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  created_by TEXT NOT NULL,
  created_at TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  rendered_sha256 TEXT NOT NULL,
  applied INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_env_revisions_service ON env_revisions (service_id, id);

CREATE TABLE env_revision_entries (
  revision_id INTEGER NOT NULL REFERENCES env_revisions(id) ON DELETE CASCADE,
  key TEXT NOT NULL,
  ciphertext BLOB NOT NULL,
  nonce BLOB NOT NULL,
  key_id TEXT NOT NULL,
  is_secret INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (revision_id, key)
);

CREATE TABLE env_runs (
  id TEXT PRIMARY KEY,
  service_id TEXT NOT NULL REFERENCES services(id),
  revision_id INTEGER REFERENCES env_revisions(id),
  state TEXT NOT NULL CHECK (state IN ('running', 'succeeded', 'failed')),
  started_at TEXT NOT NULL,
  finished_at TEXT,
  steps_json TEXT NOT NULL DEFAULT '[]',
  error TEXT NOT NULL DEFAULT '',
  validation_lines_json TEXT NOT NULL DEFAULT '[]',
  restored INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_env_runs_service ON env_runs (service_id, started_at);

CREATE TABLE env_validations (
  service_id TEXT PRIMARY KEY REFERENCES services(id),
  ok INTEGER NOT NULL,
  lines_json TEXT NOT NULL DEFAULT '[]',
  at TEXT NOT NULL
);

-- Extend the append-only audit allowlist for env pillar actions.
CREATE TABLE audit_env_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT REFERENCES services(id),
  actor TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN (
    'cutover', 'rollback', 'login', 'deploy', 'db_backup', 'db_restore',
    'service_create', 'service_delete', 'user_create', 'user_update', 'user_delete',
    'container_approve', 'container_ignore', 'security_acknowledge', 'security_resolve', 'security_suppress',
    'security_policy_create', 'security_policy_update', 'security_policy_delete',
    'env.stage', 'env.discard', 'env.import', 'env.reveal', 'env.apply', 'env.apply_failed', 'env.rollback'
  )),
  result TEXT NOT NULL CHECK (result IN ('success', 'failure')),
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO audit_env_new (id, service_id, actor, action, result, detail, created_at)
  SELECT id, service_id, actor, action, result, detail, created_at FROM audit;
DROP TABLE audit;
ALTER TABLE audit_env_new RENAME TO audit;
CREATE INDEX idx_audit_service_created ON audit (service_id, created_at);
CREATE INDEX idx_audit_action_created ON audit (action, created_at);
