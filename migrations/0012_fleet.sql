-- 0012_fleet: fleet monitoring samples (S23).
-- host_samples holds one host row per sample interval: CPU percent
-- (null until the second /proc/stat delta), memory and disk used/total
-- bytes, and the 1-minute load average. All value columns stay nullable
-- because non-Linux hosts and unreadable /proc fields degrade to null.
-- container_samples holds one row per container per interval, covering
-- EVERY container on the daemon: managed rows carry managed=1 plus their
-- service_id (deliberately duplicating the metrics table so the fleet
-- view is one query), unmanaged rows carry managed=0 and no service.
-- Both tables trim under TOUCHGRASS_RETENTION_METRICS.
CREATE TABLE IF NOT EXISTS host_samples (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  sampled_at TEXT NOT NULL,
  cpu_percent REAL,
  mem_used INTEGER,
  mem_total INTEGER,
  disk_used INTEGER,
  disk_total INTEGER,
  load1 REAL
);
CREATE INDEX IF NOT EXISTS idx_host_samples_sampled ON host_samples (sampled_at);

CREATE TABLE IF NOT EXISTS container_samples (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  sampled_at TEXT NOT NULL,
  container_name TEXT NOT NULL,
  project TEXT NOT NULL DEFAULT '',
  managed INTEGER NOT NULL DEFAULT 0,
  service_id TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT '',
  cpu_percent REAL NOT NULL,
  mem_bytes INTEGER NOT NULL,
  mem_limit INTEGER NOT NULL,
  restarts INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_container_samples_sampled ON container_samples (sampled_at);
CREATE INDEX IF NOT EXISTS idx_container_samples_name_sampled ON container_samples (container_name, sampled_at);
