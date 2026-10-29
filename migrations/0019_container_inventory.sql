-- 0019_container_inventory: durable all-state container identity.
CREATE TABLE container_instances (
  container_id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  image TEXT NOT NULL DEFAULT '',
  image_id TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  compose_project TEXT NOT NULL DEFAULT '',
  compose_service TEXT NOT NULL DEFAULT '',
  service_id TEXT NOT NULL DEFAULT '',
  classification TEXT NOT NULL DEFAULT 'unknown' CHECK (classification IN (
    'managed', 'approved_observed', 'unknown', 'ignored', 'missing_expected'
  )),
  current INTEGER NOT NULL DEFAULT 1,
  present INTEGER NOT NULL DEFAULT 1,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  absent_at TEXT,
  image_fingerprint TEXT NOT NULL DEFAULT '',
  configuration_fingerprint TEXT NOT NULL DEFAULT '',
  inspection_json TEXT NOT NULL DEFAULT '{}',
  cpu_percent REAL,
  mem_bytes INTEGER,
  mem_limit INTEGER,
  restarts INTEGER,
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_container_instances_current ON container_instances(current, present, name);
CREATE INDEX idx_container_instances_classification ON container_instances(classification, current);
CREATE INDEX idx_container_instances_service ON container_instances(service_id, current);

CREATE TABLE container_inventory_state (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  reconciled_at TEXT,
  complete INTEGER NOT NULL DEFAULT 0,
  last_error_category TEXT NOT NULL DEFAULT '',
  observed_count INTEGER NOT NULL DEFAULT 0
);
INSERT INTO container_inventory_state(singleton) VALUES (1);

CREATE TABLE container_security_activation (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  activated_at TEXT,
  activated_by TEXT NOT NULL DEFAULT ''
);
INSERT INTO container_security_activation(singleton) VALUES (1);

ALTER TABLE container_samples ADD COLUMN container_id TEXT;
ALTER TABLE container_events ADD COLUMN container_id TEXT;
CREATE INDEX idx_container_samples_id_sampled ON container_samples(container_id, sampled_at);
CREATE INDEX idx_container_events_container_time ON container_events(container_id, occurred_at);
