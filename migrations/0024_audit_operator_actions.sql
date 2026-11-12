-- 0024_audit_operator_actions: audit the mutations the developer role
-- gains (S32, ADR-0014 addendum). Alert rule and issue rule create and
-- delete, marking notifications read, and manual deploy records wrote
-- no audit row while only admins could call them; now that a scoped
-- non-admin can, each is attributed.
--
-- SQLite cannot alter a CHECK, so the audit table is rebuilt (same
-- pattern as 0006, 0011, 0013, 0014, 0015, 0020 and 0021). Every row is
-- copied with its id. Nothing references audit, so the DROP fires no
-- cascade.
CREATE TABLE audit_operator_new (
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
    'notification_read'
  )),
  result TEXT NOT NULL CHECK (result IN ('success', 'failure')),
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO audit_operator_new (id, service_id, actor, action, result, detail, created_at)
  SELECT id, service_id, actor, action, result, detail, created_at FROM audit;
DROP TABLE audit;
ALTER TABLE audit_operator_new RENAME TO audit;
CREATE INDEX idx_audit_service_created ON audit (service_id, created_at);
CREATE INDEX idx_audit_action_created ON audit (action, created_at);
