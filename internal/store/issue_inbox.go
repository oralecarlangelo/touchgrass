package store

import (
	"context"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

type inboxRow struct {
	issue  model.Issue
	cursor model.IssueCursor
}

// IssueInbox queries a stable page across the permitted services.
func (s *IssueStore) IssueInbox(
	ctx context.Context,
	filter model.IssueInboxQuery,
) ([]model.Issue, model.IssueCursor, error) {
	if !filter.AllServices && len(filter.ServiceIDs) == 0 {
		return []model.Issue{}, model.IssueCursor{}, nil
	}

	limit := min(max(filter.Limit, 1), 100)
	where, args := issueInboxFilters(filter)

	rows, err := queryRows(ctx, s.db.read, rowQuery[inboxRow]{
		what: "issue inbox",
		query: `SELECT id, service_id, fingerprint, title, first_seen, last_seen, notified_at, created_at,
		        (SELECT COUNT(*) FROM occurrences WHERE issue_id = issues.id)
		        FROM issues` + where + ` ORDER BY last_seen DESC, id DESC LIMIT ?`,
		args: append(args, limit+1),
		scan: func(row scanner) (inboxRow, error) {
			var result inboxRow

			issue, scanErr := scanIssueCapturingSeen(row, &result.cursor.LastSeen)
			result.issue = issue
			result.cursor.ID = issue.ID

			return result, scanErr
		},
	})
	if err != nil {
		return nil, model.IssueCursor{}, err
	}

	var next model.IssueCursor

	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[len(rows)-1].cursor
	}

	issues := make([]model.Issue, 0, len(rows))
	for _, row := range rows {
		issues = append(issues, row.issue)
	}

	issues, err = s.withReleases(ctx, issues)
	if err != nil {
		return nil, model.IssueCursor{}, err
	}

	return issues, next, nil
}

func issueInboxFilters(filter model.IssueInboxQuery) (string, []any) {
	where := " WHERE 1=1"
	args := []any{}

	if !filter.AllServices {
		clause, ids := inClause(filter.ServiceIDs)
		where += " AND service_id IN " + clause

		args = append(args, ids...)
	}

	if filter.Search != "" {
		where += " AND (instr(lower(title), lower(?)) > 0 OR instr(lower(fingerprint), lower(?)) > 0)"

		args = append(args, filter.Search, filter.Search)
	}

	if filter.Release != "" {
		where += " AND EXISTS (SELECT 1 FROM issue_releases WHERE issue_id = issues.id AND release = ?)"

		args = append(args, filter.Release)
	}

	if filter.Start != "" {
		where += " AND last_seen >= ?"

		args = append(args, filter.Start)
	}

	if filter.End != "" {
		where += " AND last_seen <= ?"

		args = append(args, filter.End)
	}

	if filter.Before.ID > 0 {
		where += " AND (last_seen, id) < (?, ?)"

		args = append(args, filter.Before.LastSeen, filter.Before.ID)
	}

	return where, args
}
