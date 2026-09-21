package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrIssueNotFound reports an unknown issue id.
var ErrIssueNotFound = errors.New("store: issue not found")

// IssueStore persists fingerprinted error groups.
type IssueStore struct {
	db *DB
}

// NewIssueStore builds an IssueStore on db.
func NewIssueStore(db *DB) *IssueStore {
	return &IssueStore{db: db}
}

// GroupOccurrence upserts the issue, records the release, and stores the
// occurrence in one transaction, returning the occurrence with its ids.
func (s *IssueStore) GroupOccurrence(
	ctx context.Context,
	serviceID, fingerprint, title, release string,
	occurrence model.Occurrence,
) (model.Occurrence, error) {
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return model.Occurrence{}, fmt.Errorf("starting group transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	now := formatTime(time.Now())

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO issues (service_id, fingerprint, title, first_seen, last_seen)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (service_id, fingerprint) DO UPDATE SET last_seen = excluded.last_seen`,
		serviceID, fingerprint, title, now, now,
	); err != nil {
		return model.Occurrence{}, fmt.Errorf("upserting issue: %w", err)
	}

	var issueID int64

	if err := tx.QueryRowContext(ctx,
		"SELECT id FROM issues WHERE service_id = ? AND fingerprint = ?",
		serviceID, fingerprint,
	).Scan(&issueID); err != nil {
		return model.Occurrence{}, fmt.Errorf("reading issue id: %w", err)
	}

	if release != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO issue_releases (issue_id, release) VALUES (?, ?)
			 ON CONFLICT DO NOTHING`,
			issueID, release,
		); err != nil {
			return model.Occurrence{}, fmt.Errorf("recording issue release: %w", err)
		}
	}

	if occurrence.CreatedAt.IsZero() {
		occurrence.CreatedAt = time.Now()
	}

	occurrence.IssueID = &issueID

	id, err := insertOccurrenceRow(ctx, tx, occurrence)
	if err != nil {
		return model.Occurrence{}, err
	}

	if err := tx.Commit(); err != nil {
		return model.Occurrence{}, fmt.Errorf("committing group transaction: %w", err)
	}

	occurrence.ID = id

	return occurrence, nil
}

// Get returns one issue with its live count and releases.
func (s *IssueStore) Get(ctx context.Context, id int64) (model.Issue, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT id, service_id, fingerprint, title, first_seen, last_seen,
		        notified_at, created_at,
		        (SELECT COUNT(*) FROM occurrences WHERE issue_id = issues.id)
		 FROM issues WHERE id = ?`,
		id,
	)

	issue, err := scanIssue(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Issue{}, fmt.Errorf("%w: %d", ErrIssueNotFound, id)
		}

		return model.Issue{}, err
	}

	withReleases, err := s.withReleases(ctx, []model.Issue{issue})
	if err != nil {
		return model.Issue{}, err
	}

	return withReleases[0], nil
}

// ListByService returns issues newest-activity first with counts and
// releases, bounded by limit.
func (s *IssueStore) ListByService(
	ctx context.Context,
	serviceID string,
	limit int,
) ([]model.Issue, error) {
	issues, err := queryRows(ctx, s.db.sql, rowQuery[model.Issue]{
		what: "issues",
		query: `SELECT id, service_id, fingerprint, title, first_seen, last_seen,
		        notified_at, created_at,
		        (SELECT COUNT(*) FROM occurrences WHERE issue_id = issues.id)
		 FROM issues WHERE service_id = ? ORDER BY last_seen DESC, id DESC LIMIT ?`,
		args: []any{serviceID, limit},
		scan: scanIssue,
	})
	if err != nil {
		return nil, err
	}

	return s.withReleases(ctx, issues)
}

// UnnotifiedSince returns issues first seen at or after since that never
// fired a new-issue notification.
func (s *IssueStore) UnnotifiedSince(
	ctx context.Context,
	serviceID string,
	since time.Time,
) ([]model.Issue, error) {
	return queryRows(ctx, s.db.sql, rowQuery[model.Issue]{
		what: "unnotified issues",
		query: `SELECT id, service_id, fingerprint, title, first_seen, last_seen,
		        notified_at, created_at,
		        (SELECT COUNT(*) FROM occurrences WHERE issue_id = issues.id)
		 FROM issues
		 WHERE service_id = ? AND first_seen >= ? AND notified_at IS NULL
		 ORDER BY id`,
		args: []any{serviceID, formatTime(since)},
		scan: scanIssue,
	})
}

// ActiveSince returns issues seen at or after since, oldest first.
func (s *IssueStore) ActiveSince(
	ctx context.Context,
	serviceID string,
	since time.Time,
) ([]model.Issue, error) {
	return queryRows(ctx, s.db.sql, rowQuery[model.Issue]{
		what: "active issues",
		query: `SELECT id, service_id, fingerprint, title, first_seen, last_seen,
		        notified_at, created_at,
		        (SELECT COUNT(*) FROM occurrences WHERE issue_id = issues.id)
		 FROM issues
		 WHERE service_id = ? AND last_seen >= ?
		 ORDER BY id`,
		args: []any{serviceID, formatTime(since)},
		scan: scanIssue,
	})
}

// MarkNotified stamps an issue's new-issue notification.
func (s *IssueStore) MarkNotified(ctx context.Context, id int64) error {
	if _, err := s.db.sql.ExecContext(ctx,
		"UPDATE issues SET notified_at = ? WHERE id = ?",
		formatTime(time.Now()), id,
	); err != nil {
		return fmt.Errorf("marking issue notified: %w", err)
	}

	return nil
}

// TrimBefore deletes issues quiet since cutoff and reports the count.
// Releases cascade; their occurrences age out through the occurrence trim.
func (s *IssueStore) TrimBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx,
		"DELETE FROM issues WHERE last_seen < ?", formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("trimming issues: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed issues: %w", err)
	}

	return trimmed, nil
}

// withReleases attaches sorted releases to issues from one service query.
func (s *IssueStore) withReleases(
	ctx context.Context,
	issues []model.Issue,
) ([]model.Issue, error) {
	if len(issues) == 0 {
		return issues, nil
	}

	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT r.issue_id, r.release FROM issue_releases r
		 JOIN issues i ON i.id = r.issue_id
		 WHERE i.service_id = ? ORDER BY r.release`,
		issues[0].ServiceID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying issue releases: %w", err)
	}

	defer func() {
		_ = rows.Close()
	}()

	mapped := map[int64][]string{}

	for _, issue := range issues {
		mapped[issue.ID] = []string{}
	}

	for rows.Next() {
		var (
			issueID int64
			release string
		)

		if err := rows.Scan(&issueID, &release); err != nil {
			return nil, fmt.Errorf("scanning issue release: %w", err)
		}

		mapped[issueID] = append(mapped[issueID], release)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating issue releases: %w", err)
	}

	for i := range issues {
		issues[i].Releases = mapped[issues[i].ID]
	}

	return issues, nil
}

// scanIssue maps one issues row with its live count.
func scanIssue(row scanner) (model.Issue, error) {
	var (
		issue      model.Issue
		firstSeen  string
		lastSeen   string
		notifiedAt sql.NullString
		createdAt  string
	)

	if err := row.Scan(
		&issue.ID,
		&issue.ServiceID,
		&issue.Fingerprint,
		&issue.Title,
		&firstSeen,
		&lastSeen,
		&notifiedAt,
		&createdAt,
		&issue.Count,
	); err != nil {
		return model.Issue{}, fmt.Errorf("scanning issue: %w", err)
	}

	first, err := parseTime(firstSeen, "first_seen")
	if err != nil {
		return model.Issue{}, err
	}

	issue.FirstSeen = first

	last, err := parseTime(lastSeen, "last_seen")
	if err != nil {
		return model.Issue{}, err
	}

	issue.LastSeen = last

	notified, err := parseNullTime(notifiedAt, "notified_at")
	if err != nil {
		return model.Issue{}, err
	}

	issue.NotifiedAt = notified

	created, err := parseTime(createdAt, "created_at")
	if err != nil {
		return model.Issue{}, err
	}

	issue.CreatedAt = created
	issue.Releases = []string{}

	return issue, nil
}
