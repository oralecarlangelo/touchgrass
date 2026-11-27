-- Match the status-filtered Security inbox ordering so SQLite can stop at its page limit.
CREATE INDEX idx_security_findings_status_rank
    ON security_findings(status,
        (CASE severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3
            WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END) DESC,
        last_seen_at DESC, id DESC);

-- Cover Fleet risk counts by requested container without indexing inactive history.
CREATE INDEX idx_security_findings_active_container
    ON security_findings(container_id, status, severity)
    WHERE status IN ('open','acknowledged');
