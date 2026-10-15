package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func isPerformanceMetric(metric string) bool {
	switch metric {
	case model.MetricHTTPP95, model.MetricHTTPErrorRate,
		model.MetricPostgresConnectionPct, model.MetricRedisEvictions:
		return true
	default:
		return false
	}
}

// AlertValue returns one completed-minute value. available is false when the
// source is missing, stale, or has no valid sample; callers must preserve any
// active alert episode in that case.
func (p *Performance) AlertValue(ctx context.Context, serviceID, metric string) (float64, bool, error) {
	if _, err := p.services.Get(ctx, serviceID); err != nil {
		return 0, false, err
	}
	source := performanceMetricSource(metric)
	if source == "" {
		return 0, false, fmt.Errorf("%w: invalid performance metric", ErrInvalidInput)
	}
	now := p.now().UTC()
	cursor, err := p.store.Get(ctx, source)
	if errors.Is(err, store.ErrCollectorCursorNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("loading %s collector status: %w", source, err)
	}
	if cursor.LastSuccessAt.IsZero() || now.Sub(cursor.LastSuccessAt) > collectorStaleAfter {
		return 0, false, nil
	}

	end := now.Truncate(time.Minute)
	rangeQuery := model.PerformanceRange{ServiceID: serviceID, Start: end.Add(-time.Minute), End: end, Limit: 100}
	switch metric {
	case model.MetricHTTPP95, model.MetricHTTPErrorRate:
		rows, err := p.store.ListHTTP(ctx, rangeQuery)
		if err != nil {
			return 0, false, fmt.Errorf("loading completed HTTP bucket: %w", err)
		}
		if len(rows) == 0 {
			return 0, false, nil
		}
		snapshot := summarizeHTTP(rows)
		if metric == model.MetricHTTPP95 {
			return snapshot.P95MS, true, nil
		}
		return snapshot.ErrorRate, true, nil
	case model.MetricPostgresConnectionPct:
		rows, err := p.store.ListPostgres(ctx, rangeQuery)
		if err != nil {
			return 0, false, fmt.Errorf("loading completed postgres bucket: %w", err)
		}
		var used, maximum float64
		for _, row := range rows {
			switch row.Metric {
			case "connections_total":
				used = row.Value
			case "max_connections":
				maximum = row.Value
			}
		}
		if maximum <= 0 {
			return 0, false, nil
		}
		return used / maximum * 100, true, nil
	case model.MetricRedisEvictions:
		rows, err := p.store.ListRedis(ctx, rangeQuery)
		if err != nil {
			return 0, false, fmt.Errorf("loading completed redis bucket: %w", err)
		}
		for _, row := range rows {
			if row.Metric == "evicted_keys" && !row.Reset {
				return row.Delta, true, nil
			}
		}
		return 0, false, nil
	}
	return 0, false, nil
}

func performanceMetricSource(metric string) string {
	switch metric {
	case model.MetricHTTPP95, model.MetricHTTPErrorRate:
		return "nginx"
	case model.MetricPostgresConnectionPct:
		return "postgres"
	case model.MetricRedisEvictions:
		return "redis"
	default:
		return ""
	}
}

func (s *Sampler) evaluatePerformanceRule(ctx context.Context, rule model.AlertRule) error {
	if s.performance == nil {
		return nil
	}
	value, available, err := s.performance.AlertValue(ctx, rule.ServiceID, rule.Metric)
	if err != nil {
		return err
	}
	if !available {
		return nil
	}
	return s.evaluateThreshold(ctx, rule, value > rule.Threshold, time.Now())
}
