package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// EnvFleetCounts returns only counts and apply times for the given services.
func (s *EnvStore) EnvFleetCounts(ctx context.Context, ids []string) ([]model.EnvFleetSummary, error) {
	if len(ids) == 0 {
		return []model.EnvFleetSummary{}, nil
	}

	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("encoding environment summary scope: %w", err)
	}

	return queryRows(ctx, s.db.read, rowQuery[model.EnvFleetSummary]{
		what: "environment summaries",
		query: `SELECT s.id, COUNT(e.key),
			SUM(CASE WHEN e.is_secret = 1 THEN 1 ELSE 0 END),
			SUM(CASE WHEN e.state <> 'applied' THEN 1 ELSE 0 END),
			(SELECT created_at FROM env_revisions WHERE service_id = s.id AND applied = 1
			 ORDER BY id DESC LIMIT 1)
			FROM services s LEFT JOIN env_entries e ON e.service_id = s.id
			WHERE s.id IN (SELECT value FROM json_each(?)) GROUP BY s.id`,
		args: []any{string(encoded)},
		scan: scanEnvFleetCounts,
	})
}

func scanEnvFleetCounts(row scanner) (model.EnvFleetSummary, error) {
	var (
		counts  model.EnvFleetSummary
		applied sql.NullString
	)

	if err := row.Scan(&counts.ServiceID, &counts.KeyCount, &counts.SecretCount, &counts.PendingCount, &applied); err != nil {
		return model.EnvFleetSummary{}, fmt.Errorf("scanning environment summary: %w", err)
	}

	var err error

	counts.LastAppliedAt, err = parseNullTime(applied, "last_applied_at")

	return counts, err
}
