package store

import (
	"context"
	"database/sql"
	"fmt"
)

// rowQuery describes one scanned list query.
type rowQuery[T any] struct {
	what  string
	query string
	args  []any
	scan  func(scanner) (T, error)
}

// queryRows runs a list query and scans every row.
func queryRows[T any](ctx context.Context, db *sql.DB, query rowQuery[T]) ([]T, error) {
	rows, err := db.QueryContext(ctx, query.query, query.args...)
	if err != nil {
		return nil, fmt.Errorf("querying %s: %w", query.what, err)
	}

	defer func() {
		_ = rows.Close()
	}()

	items := []T{}

	for rows.Next() {
		item, err := query.scan(rows)
		if err != nil {
			return nil, err
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating %s: %w", query.what, err)
	}

	return items, nil
}
