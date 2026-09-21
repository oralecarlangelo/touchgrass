-- 0009_logs: container log aggregation. log_lines holds tailed lines
-- with docker timestamps; log_lines_fts is an external-content FTS5
-- index synced by triggers (lines are immutable, so no update trigger).
-- Count-capped per service at write time, age-trimmed with log retention.
CREATE TABLE IF NOT EXISTS log_lines (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  service_id TEXT NOT NULL REFERENCES services(id),
  container TEXT NOT NULL,
  stream TEXT NOT NULL CHECK (stream IN ('stdout', 'stderr')),
  line TEXT NOT NULL,
  ts TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_log_lines_service_id ON log_lines (service_id, id);
CREATE INDEX IF NOT EXISTS idx_log_lines_service_ts ON log_lines (service_id, ts);
CREATE VIRTUAL TABLE IF NOT EXISTS log_lines_fts USING fts5(
  line, service_id UNINDEXED, content='log_lines', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS log_lines_ai AFTER INSERT ON log_lines BEGIN
  INSERT INTO log_lines_fts(rowid, line, service_id)
  VALUES (new.id, new.line, new.service_id);
END;
CREATE TRIGGER IF NOT EXISTS log_lines_ad AFTER DELETE ON log_lines BEGIN
  INSERT INTO log_lines_fts(log_lines_fts, rowid, line, service_id)
  VALUES ('delete', old.id, old.line, old.service_id);
END;
