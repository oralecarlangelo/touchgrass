-- 0020_container_security: approval baselines, policies, observations, and findings.
CREATE TABLE container_security_baselines (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  container_id TEXT NOT NULL REFERENCES container_instances(container_id) ON DELETE CASCADE,
  image_fingerprint TEXT NOT NULL,
  configuration_fingerprint TEXT NOT NULL,
  approved_by TEXT NOT NULL,
  approved_at TEXT NOT NULL,
  active INTEGER NOT NULL DEFAULT 1,
  UNIQUE(container_id, image_fingerprint, configuration_fingerprint)
);
CREATE INDEX idx_security_baselines_container ON container_security_baselines(container_id, active, approved_at);

CREATE TABLE container_security_ignores (
  container_id TEXT PRIMARY KEY REFERENCES container_instances(container_id) ON DELETE CASCADE,
  reason TEXT NOT NULL,
  expires_at TEXT,
  non_expiring INTEGER NOT NULL DEFAULT 0,
  actor TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE security_policies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  scope TEXT NOT NULL,
  scope_id TEXT NOT NULL DEFAULT '',
  policy_json TEXT NOT NULL DEFAULT '{}',
  updated_by TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  UNIQUE(scope, scope_id)
);

CREATE TABLE security_observations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  container_id TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  category TEXT NOT NULL,
  severity TEXT NOT NULL,
  confidence TEXT NOT NULL,
  detected INTEGER NOT NULL,
  coverage_complete INTEGER NOT NULL,
  evidence_json TEXT NOT NULL DEFAULT '{}',
  observed_at TEXT NOT NULL
);
CREATE INDEX idx_security_observations_container_time ON security_observations(container_id, observed_at);

CREATE TABLE security_findings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  fingerprint TEXT NOT NULL,
  episode INTEGER NOT NULL,
  container_id TEXT NOT NULL,
  rule_id TEXT NOT NULL,
  category TEXT NOT NULL,
  severity TEXT NOT NULL,
  confidence TEXT NOT NULL,
  status TEXT NOT NULL,
  occurrence_count INTEGER NOT NULL DEFAULT 1,
  evidence_json TEXT NOT NULL DEFAULT '{}',
  opened_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  acknowledged_at TEXT,
  acknowledged_by TEXT NOT NULL DEFAULT '',
  resolved_at TEXT,
  resolved_by TEXT NOT NULL DEFAULT '',
  suppressed_until TEXT,
  suppression_reason TEXT NOT NULL DEFAULT '',
  suppression_actor TEXT NOT NULL DEFAULT '',
  notification_status TEXT NOT NULL DEFAULT '',
  UNIQUE(fingerprint, episode)
);
CREATE INDEX idx_security_findings_status_severity ON security_findings(status, severity, last_seen_at);
CREATE UNIQUE INDEX idx_security_findings_open_episode ON security_findings(fingerprint)
  WHERE status IN ('open', 'acknowledged', 'suppressed');

ALTER TABLE collector_cursors ADD COLUMN coverage_complete INTEGER NOT NULL DEFAULT 1;
ALTER TABLE collector_cursors ADD COLUMN effective_interval_secs INTEGER NOT NULL DEFAULT 0;

-- Security findings may be global or tied to an unmanaged container, so
-- notifications now permit a null service while preserving existing rows.
CREATE TABLE notifications_security_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT REFERENCES services(id),
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  read_at TEXT
);
INSERT INTO notifications_security_new (id, service_id, kind, title, body, created_at, read_at)
  SELECT id, service_id, kind, title, body, created_at, read_at FROM notifications;
DROP TABLE notifications;
ALTER TABLE notifications_security_new RENAME TO notifications;
CREATE INDEX idx_notifications_created ON notifications (created_at);

-- Extend the append-only audit allowlist for container security mutations.
CREATE TABLE audit_security_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT REFERENCES services(id),
  actor TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN (
    'cutover', 'rollback', 'login', 'deploy', 'db_backup', 'db_restore',
    'service_create', 'service_delete', 'user_create', 'user_update', 'user_delete',
    'container_approve', 'container_ignore', 'security_acknowledge', 'security_resolve', 'security_suppress',
    'security_policy_create', 'security_policy_update', 'security_policy_delete'
  )),
  result TEXT NOT NULL CHECK (result IN ('success', 'failure')),
  detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO audit_security_new (id, service_id, actor, action, result, detail, created_at)
  SELECT id, service_id, actor, action, result, detail, created_at FROM audit;
DROP TABLE audit;
ALTER TABLE audit_security_new RENAME TO audit;
CREATE INDEX idx_audit_service_created ON audit (service_id, created_at);
CREATE INDEX idx_audit_action_created ON audit (action, created_at);
