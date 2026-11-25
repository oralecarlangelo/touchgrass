-- 0025_retention_indexes: timestamp indexes for the two retention targets
-- that had none. Maintenance picks the oldest rows of every target by its
-- timestamp each pass; without these it scanned (and, for the byte budget,
-- sorted) the whole table, and the metrics range query behind the performance
-- timeline did the same when it was not narrowed to one service.
CREATE INDEX IF NOT EXISTS idx_metrics_sampled_at ON metrics (sampled_at);
CREATE INDEX IF NOT EXISTS idx_security_observations_observed_at ON security_observations (observed_at);
