-- Admit audited console updates while preserving existing rows and the id sequence.
CREATE TABLE audit_self_update_new (
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
    'notification_read', 'storage_action', 'self_update'
  )),
  result TEXT NOT NULL CHECK (result IN ('success', 'failure')),
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO audit_self_update_new (id, service_id, actor, action, result, detail, created_at)
  SELECT id, service_id, actor, action, result, detail, created_at FROM audit;
UPDATE sqlite_sequence
  SET seq = MAX(seq, COALESCE((SELECT seq FROM sqlite_sequence WHERE name = 'audit'), seq))
  WHERE name = 'audit_self_update_new';
INSERT INTO sqlite_sequence (name, seq)
  SELECT 'audit_self_update_new', seq FROM sqlite_sequence
  WHERE name = 'audit'
    AND NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'audit_self_update_new');
DROP TABLE audit;
ALTER TABLE audit_self_update_new RENAME TO audit;
CREATE INDEX idx_audit_service_created ON audit (service_id, created_at);
CREATE INDEX idx_audit_action_created ON audit (action, created_at);
