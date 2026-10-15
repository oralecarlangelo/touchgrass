CREATE TABLE http_metric_buckets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  bucket_start TEXT NOT NULL,
  service_id TEXT NOT NULL,
  environment TEXT NOT NULL,
  method TEXT NOT NULL,
  route_family TEXT NOT NULL,
  upstream TEXT NOT NULL,
  status_class INTEGER NOT NULL,
  request_count INTEGER NOT NULL DEFAULT 0,
  error_count INTEGER NOT NULL DEFAULT 0,
  duration_total_ms REAL NOT NULL DEFAULT 0,
  upstream_connect_total_ms REAL NOT NULL DEFAULT 0,
  upstream_header_total_ms REAL NOT NULL DEFAULT 0,
  upstream_response_total_ms REAL NOT NULL DEFAULT 0,
  bytes_sent INTEGER NOT NULL DEFAULT 0,
  request_bytes INTEGER NOT NULL DEFAULT 0,
  h_10 INTEGER NOT NULL DEFAULT 0,
  h_25 INTEGER NOT NULL DEFAULT 0,
  h_50 INTEGER NOT NULL DEFAULT 0,
  h_100 INTEGER NOT NULL DEFAULT 0,
  h_250 INTEGER NOT NULL DEFAULT 0,
  h_500 INTEGER NOT NULL DEFAULT 0,
  h_1000 INTEGER NOT NULL DEFAULT 0,
  h_2500 INTEGER NOT NULL DEFAULT 0,
  h_5000 INTEGER NOT NULL DEFAULT 0,
  h_inf INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  UNIQUE(bucket_start, service_id, environment, method, route_family, upstream, status_class)
);

CREATE TABLE performance_ingest_batches (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source TEXT NOT NULL,
  ingest_key TEXT NOT NULL,
  bucket_key TEXT NOT NULL,
  bucket_start TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(source, ingest_key, bucket_key)
);

CREATE TABLE performance_hourly_rollups (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source TEXT NOT NULL,
  bucket_start TEXT NOT NULL,
  service_id TEXT NOT NULL,
  environment TEXT NOT NULL,
  method TEXT NOT NULL,
  route_family TEXT NOT NULL,
  upstream TEXT NOT NULL,
  status_class INTEGER NOT NULL,
  request_count INTEGER NOT NULL DEFAULT 0,
  error_count INTEGER NOT NULL DEFAULT 0,
  duration_total_ms REAL NOT NULL DEFAULT 0,
  upstream_connect_total_ms REAL NOT NULL DEFAULT 0,
  upstream_header_total_ms REAL NOT NULL DEFAULT 0,
  upstream_response_total_ms REAL NOT NULL DEFAULT 0,
  bytes_sent INTEGER NOT NULL DEFAULT 0,
  request_bytes INTEGER NOT NULL DEFAULT 0,
  h_10 INTEGER NOT NULL DEFAULT 0,
  h_25 INTEGER NOT NULL DEFAULT 0,
  h_50 INTEGER NOT NULL DEFAULT 0,
  h_100 INTEGER NOT NULL DEFAULT 0,
  h_250 INTEGER NOT NULL DEFAULT 0,
  h_500 INTEGER NOT NULL DEFAULT 0,
  h_1000 INTEGER NOT NULL DEFAULT 0,
  h_2500 INTEGER NOT NULL DEFAULT 0,
  h_5000 INTEGER NOT NULL DEFAULT 0,
  h_inf INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  UNIQUE(source, bucket_start, service_id, environment, method, route_family, upstream, status_class)
);

CREATE TABLE postgres_metric_buckets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  bucket_start TEXT NOT NULL,
  service_id TEXT NOT NULL,
  environment TEXT NOT NULL,
  metric TEXT NOT NULL,
  value REAL NOT NULL,
  delta REAL NOT NULL DEFAULT 0,
  reset INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  UNIQUE(bucket_start, service_id, environment, metric)
);

CREATE TABLE postgres_statement_buckets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  bucket_start TEXT NOT NULL,
  service_id TEXT NOT NULL,
  environment TEXT NOT NULL,
  query_id TEXT NOT NULL,
  calls INTEGER NOT NULL DEFAULT 0,
  total_exec_ms REAL NOT NULL DEFAULT 0,
  rows_count INTEGER NOT NULL DEFAULT 0,
  shared_blocks_hit INTEGER NOT NULL DEFAULT 0,
  shared_blocks_read INTEGER NOT NULL DEFAULT 0,
  temp_blocks_read INTEGER NOT NULL DEFAULT 0,
  temp_blocks_written INTEGER NOT NULL DEFAULT 0,
  reset INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  UNIQUE(bucket_start, service_id, environment, query_id)
);

CREATE TABLE redis_metric_buckets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  bucket_start TEXT NOT NULL,
  service_id TEXT NOT NULL,
  environment TEXT NOT NULL,
  metric TEXT NOT NULL,
  command_family TEXT NOT NULL DEFAULT '',
  value REAL NOT NULL,
  delta REAL NOT NULL DEFAULT 0,
  reset INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  UNIQUE(bucket_start, service_id, environment, metric, command_family)
);

CREATE TABLE log_volume_buckets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  bucket_start TEXT NOT NULL,
  service_id TEXT NOT NULL,
  environment TEXT NOT NULL,
  severity TEXT NOT NULL,
  line_count INTEGER NOT NULL DEFAULT 0,
  dropped_count INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL,
  UNIQUE(bucket_start, service_id, environment, severity)
);

CREATE TABLE container_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source_event_id TEXT NOT NULL UNIQUE,
  occurred_at TEXT NOT NULL,
  service_id TEXT NOT NULL,
  environment TEXT NOT NULL,
  event_type TEXT NOT NULL,
  container_name TEXT NOT NULL,
  compose_service TEXT NOT NULL DEFAULT '',
  expected INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

CREATE INDEX idx_http_metric_buckets_time_service ON http_metric_buckets(bucket_start, service_id);
CREATE INDEX idx_performance_ingest_batches_time ON performance_ingest_batches(bucket_start);
CREATE INDEX idx_performance_hourly_time_service ON performance_hourly_rollups(bucket_start, service_id);
CREATE INDEX idx_postgres_metric_time_service ON postgres_metric_buckets(bucket_start, service_id);
CREATE INDEX idx_postgres_statement_time_service ON postgres_statement_buckets(bucket_start, service_id);
CREATE INDEX idx_redis_metric_time_service ON redis_metric_buckets(bucket_start, service_id);
CREATE INDEX idx_log_volume_time_service ON log_volume_buckets(bucket_start, service_id);
CREATE INDEX idx_container_events_time_service ON container_events(occurred_at, service_id);
