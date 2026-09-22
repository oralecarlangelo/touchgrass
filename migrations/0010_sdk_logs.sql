-- 0010_sdk_logs: structured SDK log ingestion (Sentry Logs-style).
-- sdk_logs holds batched logger rows per service: unix-second event time,
-- stored level + OTel severity number, message, attributes JSON, trace
-- correlation, and batch release. Age-trimmed with sdk-log retention;
-- levels are an explicit CHECK because queries filter on stored values.
CREATE TABLE IF NOT EXISTS sdk_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  ts TEXT NOT NULL,
  level TEXT NOT NULL CHECK (level IN ('trace', 'debug', 'info', 'warn', 'error', 'fatal')),
  severity INTEGER NOT NULL CHECK (severity >= 1 AND severity <= 24),
  message TEXT NOT NULL,
  attributes TEXT NOT NULL DEFAULT '{}',
  trace_id TEXT NOT NULL DEFAULT '',
  span_id TEXT NOT NULL DEFAULT '',
  release TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_sdk_logs_service_ts ON sdk_logs (service_id, ts);
CREATE INDEX IF NOT EXISTS idx_sdk_logs_trace ON sdk_logs (trace_id);
