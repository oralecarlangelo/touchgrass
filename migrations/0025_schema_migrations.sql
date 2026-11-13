-- 0025_schema_migrations: schema migration awareness for monitored
-- applications (ADR-0018). A service may name a migration tool, the
-- database whose history touchgrass reads, and the container that
-- ships the migrations and runs the commands.
--
-- The database URL holds a password. It is stored the same way an env
-- value is (AES-256-GCM, ADR-0012): ciphertext, nonce and key id, bound
-- to the service by the envelope's associated data. No API returns it.
CREATE TABLE service_migrations (
  service_id TEXT PRIMARY KEY REFERENCES services(id),
  tool TEXT NOT NULL,
  url_ciphertext BLOB NOT NULL,
  url_nonce BLOB NOT NULL,
  url_key_id TEXT NOT NULL,
  database_label TEXT NOT NULL,
  container TEXT NOT NULL DEFAULT '',
  workdir TEXT NOT NULL DEFAULT '',
  migrations_dir TEXT NOT NULL DEFAULT '',
  command_json TEXT NOT NULL DEFAULT '[]',
  updated_by TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- Every migrate deploy, resolve and seed touchgrass runs. A seed leaves
-- no record in the application's database, so this table is the only
-- seed history there is; commands run outside touchgrass are not in it.
CREATE TABLE migration_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  kind TEXT NOT NULL CHECK (kind IN ('deploy', 'resolve', 'seed')),
  tool TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT '',
  actor TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('running', 'succeeded', 'failed')),
  exit_code INTEGER,
  container TEXT NOT NULL DEFAULT '',
  image TEXT NOT NULL DEFAULT '',
  output TEXT NOT NULL DEFAULT '',
  output_truncated INTEGER NOT NULL DEFAULT 0,
  started_at TEXT NOT NULL,
  finished_at TEXT
);
CREATE INDEX idx_migration_runs_service ON migration_runs (service_id, id);

-- Extend the append-only audit allowlist. SQLite cannot alter a CHECK,
-- so the table is rebuilt (same pattern as 0021 and 0024). Every row is
-- copied with its id. Nothing references audit, so the DROP fires no
-- cascade.
CREATE TABLE audit_migrations_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT REFERENCES services(id),
  actor TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN (
    'cutover', 'rollback', 'login', 'deploy', 'db_backup', 'db_restore',
    'service_create', 'service_delete', 'user_create', 'user_update', 'user_delete',
    'container_approve', 'container_ignore', 'security_acknowledge', 'security_resolve', 'security_suppress',
    'security_policy_create', 'security_policy_update', 'security_policy_delete',
    'env.stage', 'env.discard', 'env.import', 'env.reveal', 'env.apply', 'env.apply_failed', 'env.rollback',
    'deploy_record', 'alert_rule_create', 'alert_rule_delete', 'issue_rule_create', 'issue_rule_delete',
    'notification_read',
    'migration_config', 'migration_deploy', 'migration_resolve', 'seed_run'
  )),
  result TEXT NOT NULL CHECK (result IN ('success', 'failure')),
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO audit_migrations_new (id, service_id, actor, action, result, detail, created_at)
  SELECT id, service_id, actor, action, result, detail, created_at FROM audit;
DROP TABLE audit;
ALTER TABLE audit_migrations_new RENAME TO audit;
CREATE INDEX idx_audit_service_created ON audit (service_id, created_at);
CREATE INDEX idx_audit_action_created ON audit (action, created_at);
