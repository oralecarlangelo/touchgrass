package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// evaluateIssues fires enabled new-issue and spike rules.
func (s *Sampler) evaluateIssues(ctx context.Context) error {
	rules, err := s.issueRules.ListEnabled(ctx)
	if err != nil {
		return fmt.Errorf("loading issue rules: %w", err)
	}

	for _, rule := range rules {
		if err := s.evaluateIssueRule(ctx, rule); err != nil {
			s.logger.Warn("issue rule evaluation failed", "rule", rule.ID, "error", err)
		}
	}

	return nil
}

// evaluateIssueRule dispatches one rule by kind.
func (s *Sampler) evaluateIssueRule(ctx context.Context, rule model.IssueRule) error {
	switch rule.Kind {
	case model.IssueRuleNewIssue:
		return s.evaluateNewIssue(ctx, rule)
	case model.IssueRuleSpike:
		return s.evaluateSpike(ctx, rule)
	default:
		return fmt.Errorf("unknown issue rule kind %q", rule.Kind)
	}
}

// evaluateNewIssue notifies once per issue via the notified latch.
func (s *Sampler) evaluateNewIssue(ctx context.Context, rule model.IssueRule) error {
	since := time.Now().Add(-time.Duration(rule.WindowSecs) * time.Second)

	fresh, err := s.issues.UnnotifiedSince(ctx, rule.ServiceID, since)
	if err != nil {
		return fmt.Errorf("loading unnotified issues: %w", err)
	}

	for _, issue := range fresh {
		title := "New issue: " + issue.Title
		body := fmt.Sprintf(
			"first seen %s, %d occurrences",
			issue.FirstSeen.UTC().Format(time.RFC3339), issue.Count,
		)

		if _, err := s.notifications.Insert(ctx, rule.ServiceID, model.NotificationIssue, title, body); err != nil {
			return fmt.Errorf("recording issue notification: %w", err)
		}

		if err := s.issues.MarkNotified(ctx, issue.ID); err != nil {
			return fmt.Errorf("latching issue notification: %w", err)
		}

		s.logger.Info("issue alert fired", "rule", rule.ID, "issue", issue.ID)
	}

	return nil
}

// evaluateSpike edge-triggers when an issue's window count crosses the
// threshold, resetting when it falls back below.
func (s *Sampler) evaluateSpike(ctx context.Context, rule model.IssueRule) error {
	since := time.Now().Add(-time.Duration(rule.WindowSecs) * time.Second)

	active, err := s.issues.ActiveSince(ctx, rule.ServiceID, since)
	if err != nil {
		return fmt.Errorf("loading active issues: %w", err)
	}

	for _, issue := range active {
		count, err := s.occurrences.CountByIssueSince(ctx, issue.ID, since)
		if err != nil {
			return fmt.Errorf("counting issue occurrences: %w", err)
		}

		if count < int64(rule.Threshold) {
			s.clearSpike(rule.ID, issue.ID)

			continue
		}

		if s.spikeFired(rule.ID, issue.ID) {
			continue
		}

		title := "Spike: " + issue.Title
		body := fmt.Sprintf("%d occurrences in %ds", count, rule.WindowSecs)

		if _, err := s.notifications.Insert(ctx, rule.ServiceID, model.NotificationIssue, title, body); err != nil {
			return fmt.Errorf("recording spike notification: %w", err)
		}

		s.setSpike(rule.ID, issue.ID)
		s.logger.Info("spike alert fired", "rule", rule.ID, "issue", issue.ID, "count", count)
	}

	return nil
}

// spikeFired reports whether a rule already fired for an issue.
func (s *Sampler) spikeFired(ruleID, issueID int64) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return s.spikes[ruleID][issueID]
}

// setSpike latches a fired spike.
func (s *Sampler) setSpike(ruleID, issueID int64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.spikes[ruleID] == nil {
		s.spikes[ruleID] = map[int64]bool{}
	}

	s.spikes[ruleID][issueID] = true
}

// clearSpike resets a spike latch.
func (s *Sampler) clearSpike(ruleID, issueID int64) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	delete(s.spikes[ruleID], issueID)
}
