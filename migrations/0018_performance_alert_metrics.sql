CREATE TABLE alert_rules_v2 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  metric TEXT NOT NULL CHECK (metric IN (
    'cpu', 'mem', 'disk', 'http_p95_ms', 'http_error_rate',
    'postgres_connection_pct', 'redis_evictions'
  )),
  threshold REAL NOT NULL,
  duration_secs INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

INSERT INTO alert_rules_v2 (id, service_id, metric, threshold, duration_secs, enabled, created_at)
SELECT id, service_id, metric, threshold, duration_secs, enabled, created_at FROM alert_rules;

DROP TABLE alert_rules;
ALTER TABLE alert_rules_v2 RENAME TO alert_rules;
CREATE INDEX idx_rules_service ON alert_rules (service_id);
