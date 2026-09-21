-- 0003_metrics_history: metric rollups, alert rules, notifications, deploy history.
CREATE TABLE IF NOT EXISTS metrics (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  container_name TEXT NOT NULL,
  sampled_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  cpu_percent REAL NOT NULL,
  mem_bytes INTEGER NOT NULL,
  mem_limit INTEGER NOT NULL,
  disk_bytes INTEGER NOT NULL,
  restarts INTEGER NOT NULL,
  uptime_secs INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_metrics_service_sampled ON metrics (service_id, sampled_at);

CREATE TABLE IF NOT EXISTS alert_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  metric TEXT NOT NULL CHECK (metric IN ('cpu', 'mem', 'disk')),
  threshold REAL NOT NULL,
  duration_secs INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_rules_service ON alert_rules (service_id);

CREATE TABLE IF NOT EXISTS notifications (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  kind TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
  read_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications (created_at);

CREATE TABLE IF NOT EXISTS deploys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  sha TEXT NOT NULL,
  actor TEXT NOT NULL,
  type TEXT NOT NULL CHECK (type IN ('cutover', 'rollback', 'manual')),
  outcome TEXT NOT NULL CHECK (outcome IN ('success', 'failure')),
  started_at TEXT,
  finished_at TEXT,
  duration_secs INTEGER,
  downtime_secs REAL,
  notes TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_deploys_service ON deploys (service_id);

-- Default rule per service: RAM above 85% for 5 minutes (FR-R2 example).
INSERT INTO alert_rules (service_id, metric, threshold, duration_secs)
SELECT id, 'mem', 85.0, 300 FROM services;
