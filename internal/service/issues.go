package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Issues lists a service's issues newest-activity first.
func (in *Ingestor) Issues(ctx context.Context, serviceID string) ([]model.Issue, error) {
	if _, err := in.services.Get(ctx, serviceID); err != nil {
		return nil, err
	}

	return in.issues.ListByService(ctx, serviceID, maxIssuesListed)
}

// Issue returns one issue with its live stats.
func (in *Ingestor) Issue(ctx context.Context, id int64) (model.Issue, error) {
	return in.issues.Get(ctx, id)
}

// IssueOccurrences returns an issue's occurrences newest first.
func (in *Ingestor) IssueOccurrences(
	ctx context.Context,
	id int64,
	limit int,
) ([]model.Occurrence, error) {
	if _, err := in.issues.Get(ctx, id); err != nil {
		return nil, err
	}

	return in.occurrences.ListByIssue(ctx, id, clampLimit(limit))
}

// RecentOccurrences returns a service's occurrences newest first,
// across all issues, for timeline views.
func (in *Ingestor) RecentOccurrences(
	ctx context.Context,
	serviceID string,
	limit int,
) ([]model.Occurrence, error) {
	if _, err := in.services.Get(ctx, serviceID); err != nil {
		return nil, err
	}

	return in.occurrences.ListByService(ctx, serviceID, clampLimit(limit))
}

// Issue log-linking bounds: default ±60s around the newest occurrence,
// at most ±1h, newest lines first.
const (
	defaultIssueLogWindowSecs = 60
	maxIssueLogWindowSecs     = 3600
)

// IssueLogs returns log lines around an issue's newest occurrence,
// newest first. Empty when the issue has no occurrences yet.
func (in *Ingestor) IssueLogs(
	ctx context.Context,
	id int64,
	windowSecs int,
	limit int,
) ([]model.LogLine, error) {
	issue, err := in.issues.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	recent, err := in.occurrences.ListByIssue(ctx, id, 1)
	if err != nil {
		return nil, fmt.Errorf("loading newest occurrence: %w", err)
	}

	if len(recent) == 0 {
		return []model.LogLine{}, nil
	}

	window := clampIssueLogWindow(windowSecs)
	center := recent[0].CreatedAt
	since := center.Add(-window)
	until := center.Add(window)

	lines, err := in.logs.Search(ctx, store.LogFilter{
		ServiceID: issue.ServiceID, Since: &since, Until: &until, Limit: clampLimit(limit),
	})
	if err != nil {
		return nil, err
	}

	return withLevels(lines), nil
}

// clampIssueLogWindow bounds the ±window around an occurrence.
func clampIssueLogWindow(windowSecs int) time.Duration {
	if windowSecs < 1 {
		return defaultIssueLogWindowSecs * time.Second
	}

	if windowSecs > maxIssueLogWindowSecs {
		return maxIssueLogWindowSecs * time.Second
	}

	return time.Duration(windowSecs) * time.Second
}

// CreateIssueRule validates and stores an issue alert rule.
func (in *Ingestor) CreateIssueRule(
	ctx context.Context,
	create model.IssueRuleCreate,
) (model.IssueRule, error) {
	if _, err := in.services.Get(ctx, create.ServiceID); err != nil {
		return model.IssueRule{}, err
	}

	switch create.Kind {
	case model.IssueRuleNewIssue:
		create.Threshold = 0
	case model.IssueRuleSpike:
		if create.Threshold < 1 {
			return model.IssueRule{}, fmt.Errorf("%w: spike threshold must be positive", ErrInvalidInput)
		}
	default:
		return model.IssueRule{}, fmt.Errorf("%w: rule kind %q (want new_issue or spike)", ErrInvalidInput, create.Kind)
	}

	if create.WindowSecs < 1 {
		return model.IssueRule{}, fmt.Errorf("%w: window must be positive seconds", ErrInvalidInput)
	}

	return in.issueRules.Create(ctx, model.IssueRule{
		ServiceID: create.ServiceID, Kind: create.Kind,
		Threshold: create.Threshold, WindowSecs: create.WindowSecs, Enabled: true,
	})
}

// IssueRules returns a service's issue alert rules.
func (in *Ingestor) IssueRules(ctx context.Context, serviceID string) ([]model.IssueRule, error) {
	if _, err := in.services.Get(ctx, serviceID); err != nil {
		return nil, err
	}

	return in.issueRules.ListByService(ctx, serviceID)
}

// DeleteIssueRule removes an issue alert rule.
func (in *Ingestor) DeleteIssueRule(ctx context.Context, id int64) error {
	return in.issueRules.Delete(ctx, id)
}
