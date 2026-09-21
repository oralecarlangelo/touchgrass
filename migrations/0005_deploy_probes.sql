-- 0005_deploy_probes: per-interval health samples taken against the public
-- URL during deploy operations. Failed samples in the operation window
-- are the independent probe log behind deploys.downtime_secs. Trimmed
-- with deploy-history retention.
CREATE TABLE IF NOT EXISTS deploy_probes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  ok INTEGER NOT NULL,
  status_code INTEGER NOT NULL,
  latency_ms INTEGER NOT NULL,
  sampled_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_deploy_probes_service_sampled ON deploy_probes (service_id, sampled_at);
