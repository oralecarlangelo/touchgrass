package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const (
	defaultPerformanceRange = time.Hour
	maxMinuteRange          = 7 * 24 * time.Hour
	maxHourlyRange          = 30 * 24 * time.Hour
	maxPerformancePoints    = 1500
	maxPerformanceRows      = 100
	collectorStaleAfter     = 3 * time.Minute
)

var performanceRouteFamilies = map[string]bool{
	"health": true, "auth": true, "public_events": true, "account_orders": true,
	"checkout": true, "callbacks": true, "admin": true, "organization": true,
	"static": true, "other": true,
}

var performanceSorts = map[string]bool{
	"p95_ms": true, "request_count": true, "error_rate": true, "route_family": true,
}

var collectorSources = []string{
	"nginx", "postgres", "redis", "log_volume", "docker_events",
	"security_posture", "security_process", "security_diff",
}

// PerformanceConfig wires bounded investigation reads.
type PerformanceConfig struct {
	Services *store.ServiceStore
	Store    *store.PerformanceStore
	Metrics  *store.MetricStore
	Deploys  *store.DeployStore
	Now      func() time.Time
}

// Performance validates and correlates bounded native-source aggregates.
type Performance struct {
	services *store.ServiceStore
	store    *store.PerformanceStore
	metrics  *store.MetricStore
	deploys  *store.DeployStore
	now      func() time.Time
}

// PerformanceSnapshot is the aggregate used for current/comparison windows.
type PerformanceSnapshot struct {
	RequestCount int64   `json:"request_count"`
	ErrorCount   int64   `json:"error_count"`
	ErrorRate    float64 `json:"error_rate"`
	AverageMS    float64 `json:"average_ms"`
	P50MS        float64 `json:"p50_ms"`
	P95MS        float64 `json:"p95_ms"`
	P99MS        float64 `json:"p99_ms"`
}

// PerformanceAnomaly uses correlation language deliberately; it does not
// claim distributed-trace-level causation.
type PerformanceAnomaly struct {
	Kind    string  `json:"kind"`
	Score   float64 `json:"score"`
	Summary string  `json:"summary"`
}

// PerformanceSummary is the global/service overview response.
type PerformanceSummary struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Resolution string    `json:"resolution"`
	PerformanceSnapshot
	Previous  PerformanceSnapshot  `json:"previous"`
	Anomalies []PerformanceAnomaly `json:"anomalies"`
}

// PerformanceTimelinePoint aligns available signals to one minute/hour key.
type PerformanceTimelinePoint struct {
	BucketStart           time.Time `json:"bucket_start"`
	RequestCount          int64     `json:"request_count"`
	ErrorCount            int64     `json:"error_count"`
	ErrorRate             float64   `json:"error_rate"`
	P50MS                 float64   `json:"p50_ms"`
	P95MS                 float64   `json:"p95_ms"`
	P99MS                 float64   `json:"p99_ms"`
	LogErrors             int64     `json:"log_errors"`
	LogWarnings           int64     `json:"log_warnings"`
	CPUPercent            *float64  `json:"cpu_percent"`
	MemoryPercent         *float64  `json:"memory_percent"`
	PostgresConnectionPct *float64  `json:"postgres_connection_pct"`
	RedisEvictions        *float64  `json:"redis_evictions"`
}

// PerformanceTimeline includes aligned points and bounded correlation markers.
type PerformanceTimeline struct {
	Start       time.Time                  `json:"start"`
	End         time.Time                  `json:"end"`
	Resolution  string                     `json:"resolution"`
	Points      []PerformanceTimelinePoint `json:"points"`
	Events      []model.ContainerEvent     `json:"events"`
	Deployments []model.Deploy             `json:"deployments"`
}

// PerformanceRoute is one route-family row with its preceding-window values.
type PerformanceRoute struct {
	Method      string `json:"method"`
	RouteFamily string `json:"route_family"`
	PerformanceSnapshot
	Previous PerformanceSnapshot `json:"previous"`
}

// NewPerformance builds the bounded query service.
func NewPerformance(cfg PerformanceConfig) *Performance {
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Performance{services: cfg.Services, store: cfg.Store, metrics: cfg.Metrics, deploys: cfg.Deploys, now: now}
}

// Summary returns current and preceding-window HTTP aggregates.
func (p *Performance) Summary(ctx context.Context, query model.PerformanceQuery) (PerformanceSummary, error) {
	query, err := p.normalize(ctx, query)
	if err != nil {
		return PerformanceSummary{}, err
	}
	current, err := p.httpBuckets(ctx, query)
	if err != nil {
		return PerformanceSummary{}, err
	}
	previousQuery := query
	duration := query.End.Sub(query.Start)
	previousQuery.Start, previousQuery.End = query.Start.Add(-duration), query.Start
	previous, err := p.httpBuckets(ctx, previousQuery)
	if err != nil {
		return PerformanceSummary{}, err
	}
	currentSnapshot := summarizeHTTP(current)
	previousSnapshot := summarizeHTTP(previous)
	return PerformanceSummary{
		Start: query.Start, End: query.End, Resolution: query.Resolution,
		PerformanceSnapshot: currentSnapshot, Previous: previousSnapshot,
		Anomalies: rankAnomalies(currentSnapshot, previousSnapshot),
	}, nil
}

// Routes returns bounded route-family aggregates sorted by an allowlisted field.
func (p *Performance) Routes(ctx context.Context, query model.PerformanceQuery) ([]PerformanceRoute, error) {
	query, err := p.normalize(ctx, query)
	if err != nil {
		return nil, err
	}
	current, err := p.httpBuckets(ctx, query)
	if err != nil {
		return nil, err
	}
	duration := query.End.Sub(query.Start)
	previousQuery := query
	previousQuery.Start, previousQuery.End = query.Start.Add(-duration), query.Start
	previous, err := p.httpBuckets(ctx, previousQuery)
	if err != nil {
		return nil, err
	}
	currentRoutes := groupRoutes(current)
	previousRoutes := groupRoutes(previous)
	rows := make([]PerformanceRoute, 0, len(currentRoutes))
	for key, aggregate := range currentRoutes {
		parts := strings.SplitN(key, "\x1f", 2)
		rows = append(rows, PerformanceRoute{
			Method: parts[0], RouteFamily: parts[1],
			PerformanceSnapshot: summarizeHTTP(aggregate), Previous: summarizeHTTP(previousRoutes[key]),
		})
	}
	sortRoutes(rows, query.Sort)
	if len(rows) > query.Limit {
		rows = rows[:query.Limit]
	}
	if rows == nil {
		rows = []PerformanceRoute{}
	}
	return rows, nil
}

// Postgres returns bounded native metric rows.
func (p *Performance) Postgres(ctx context.Context, query model.PerformanceQuery) ([]model.PostgresMetricBucket, error) {
	query, err := p.normalize(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := p.store.ListPostgres(ctx, performanceRange(query, query.Limit))
	if rows == nil {
		rows = []model.PostgresMetricBucket{}
	}
	return rows, err
}

// Redis returns bounded native metric rows.
func (p *Performance) Redis(ctx context.Context, query model.PerformanceQuery) ([]model.RedisMetricBucket, error) {
	query, err := p.normalize(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := p.store.ListRedis(ctx, performanceRange(query, query.Limit))
	if rows == nil {
		rows = []model.RedisMetricBucket{}
	}
	return rows, err
}

type timelineAccum struct {
	point       PerformanceTimelinePoint
	histogram   model.HTTPHistogram
	cpuTotal    float64
	memoryTotal float64
	resources   int
	memoryRows  int
	postgres    map[string]float64
}

// Timeline aligns partial native signals without treating missing sources as zero.
func (p *Performance) Timeline(ctx context.Context, query model.PerformanceQuery) (PerformanceTimeline, error) {
	query, err := p.normalize(ctx, query)
	if err != nil {
		return PerformanceTimeline{}, err
	}
	points := map[time.Time]*timelineAccum{}
	point := func(at time.Time) *timelineAccum {
		key := truncatePerformanceTime(at, query.Resolution)
		if points[key] == nil {
			points[key] = &timelineAccum{point: PerformanceTimelinePoint{BucketStart: key}, postgres: map[string]float64{}}
		}
		return points[key]
	}
	httpRows, err := p.httpBuckets(ctx, query)
	if err != nil {
		return PerformanceTimeline{}, err
	}
	for _, row := range httpRows {
		item := point(row.BucketStart)
		item.point.RequestCount += row.RequestCount
		item.point.ErrorCount += row.ErrorCount
		item.histogram.Merge(row.Histogram)
	}
	logs, err := p.store.ListLogVolume(ctx, performanceRange(query, 10000))
	if err != nil {
		return PerformanceTimeline{}, fmt.Errorf("loading log volume: %w", err)
	}
	for _, row := range logs {
		item := point(row.BucketStart)
		switch row.Severity {
		case model.LogLevelError:
			item.point.LogErrors += row.LineCount
		case model.LogLevelWarn:
			item.point.LogWarnings += row.LineCount
		}
	}
	postgresRows, err := p.store.ListPostgres(ctx, performanceRange(query, 10000))
	if err != nil {
		return PerformanceTimeline{}, fmt.Errorf("loading postgres timeline: %w", err)
	}
	for _, row := range postgresRows {
		point(row.BucketStart).postgres[row.Metric] = row.Value
	}
	redisRows, err := p.store.ListRedis(ctx, performanceRange(query, 10000))
	if err != nil {
		return PerformanceTimeline{}, fmt.Errorf("loading redis timeline: %w", err)
	}
	for _, row := range redisRows {
		if row.Metric == "evicted_keys" {
			value := row.Delta
			point(row.BucketStart).point.RedisEvictions = &value
		}
	}
	metricRows, err := p.metrics.ListRange(ctx, performanceRange(query, 10000))
	if err != nil {
		return PerformanceTimeline{}, fmt.Errorf("loading resource timeline: %w", err)
	}
	for _, row := range metricRows {
		item := point(row.SampledAt)
		item.cpuTotal += row.CPUPercent
		if row.MemLimit > 0 {
			item.memoryTotal += float64(row.MemBytes) / float64(row.MemLimit) * 100
			item.memoryRows++
		}
		item.resources++
	}
	events, err := p.store.ListContainerEvents(ctx, performanceRange(query, 1000))
	if err != nil {
		return PerformanceTimeline{}, fmt.Errorf("loading runtime events: %w", err)
	}
	deployments, err := p.deploys.ListRange(ctx, performanceRange(query, 1000))
	if err != nil {
		return PerformanceTimeline{}, fmt.Errorf("loading deployments: %w", err)
	}
	result := make([]PerformanceTimelinePoint, 0, len(points))
	for _, item := range points {
		item.point.ErrorRate = percent(item.point.ErrorCount, item.point.RequestCount)
		item.point.P50MS = histogramPercentile(item.histogram, 0.50)
		item.point.P95MS = histogramPercentile(item.histogram, 0.95)
		item.point.P99MS = histogramPercentile(item.histogram, 0.99)
		if item.resources > 0 {
			cpu := item.cpuTotal / float64(item.resources)
			item.point.CPUPercent = &cpu
		}
		if item.memoryRows > 0 {
			memory := item.memoryTotal / float64(item.memoryRows)
			item.point.MemoryPercent = &memory
		}
		if total, ok := item.postgres["connections_total"]; ok {
			if maximum := item.postgres["max_connections"]; maximum > 0 {
				value := total / maximum * 100
				item.point.PostgresConnectionPct = &value
			}
		}
		result = append(result, item.point)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].BucketStart.Before(result[j].BucketStart) })
	if len(result) > maxPerformancePoints {
		result = result[len(result)-maxPerformancePoints:]
	}
	if result == nil {
		result = []PerformanceTimelinePoint{}
	}
	if events == nil {
		events = []model.ContainerEvent{}
	}
	if deployments == nil {
		deployments = []model.Deploy{}
	}
	return PerformanceTimeline{
		Start: query.Start, End: query.End, Resolution: query.Resolution,
		Points: result, Events: events, Deployments: deployments,
	}, nil
}

// CollectorStatus returns known source states, including never-run sources.
func (p *Performance) CollectorStatus(ctx context.Context) ([]model.CollectorStatus, error) {
	now := p.now().UTC()
	result := make([]model.CollectorStatus, 0, len(collectorSources))
	for _, source := range collectorSources {
		cursor, err := p.store.Get(ctx, source)
		if errors.Is(err, store.ErrCollectorCursorNotFound) {
			result = append(result, model.CollectorStatus{Source: source, Stale: true})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("loading collector status: %w", err)
		}
		status := model.CollectorStatus{
			Source: source, LastSuccessAt: cursor.LastSuccessAt,
			LastRecordedAt: cursor.LastRecordedAt, LastError: cursor.LastError, DroppedRecords: cursor.DroppedRecords,
			CoverageComplete: cursor.CoverageComplete, EffectiveIntervalSecs: cursor.EffectiveIntervalSecs,
		}
		status.Stale = status.LastSuccessAt.IsZero() || now.Sub(status.LastSuccessAt) > collectorStaleAfter
		if source == dockerEventCursorSource && status.LastError == "" && !status.LastSuccessAt.IsZero() {
			status.Stale = false
		}
		result = append(result, status)
	}
	return result, nil
}

func (p *Performance) normalize(ctx context.Context, query model.PerformanceQuery) (model.PerformanceQuery, error) {
	now := p.now().UTC()
	if query.End.IsZero() {
		query.End = now
	}
	if query.Start.IsZero() {
		query.Start = query.End.Add(-defaultPerformanceRange)
	}
	query.Start, query.End = query.Start.UTC(), query.End.UTC()
	if !query.End.After(query.Start) {
		return query, fmt.Errorf("%w: end must be after start", ErrInvalidInput)
	}
	duration := query.End.Sub(query.Start)
	if query.Resolution == "" || query.Resolution == model.PerformanceResolutionAuto {
		if duration > maxPerformancePoints*time.Minute {
			query.Resolution = model.PerformanceResolutionHour
		} else {
			query.Resolution = model.PerformanceResolutionMinute
		}
	}
	switch query.Resolution {
	case model.PerformanceResolutionMinute:
		if duration > maxMinuteRange {
			return query, fmt.Errorf("%w: minute range exceeds 7 days", ErrInvalidInput)
		}
	case model.PerformanceResolutionHour:
		if duration > maxHourlyRange {
			return query, fmt.Errorf("%w: hourly range exceeds 30 days", ErrInvalidInput)
		}
	default:
		return query, fmt.Errorf("%w: invalid resolution", ErrInvalidInput)
	}
	if query.Sort == "" {
		query.Sort = "p95_ms"
	}
	if !performanceSorts[query.Sort] {
		return query, fmt.Errorf("%w: invalid sort", ErrInvalidInput)
	}
	if query.RouteFamily != "" && !performanceRouteFamilies[query.RouteFamily] {
		return query, fmt.Errorf("%w: invalid route family", ErrInvalidInput)
	}
	if query.Limit == 0 {
		query.Limit = maxPerformanceRows
	}
	if query.Limit < 1 || query.Limit > maxPerformanceRows {
		return query, fmt.Errorf("%w: limit must be 1-100", ErrInvalidInput)
	}
	if query.ServiceID != "" {
		if _, err := p.services.Get(ctx, query.ServiceID); err != nil {
			return query, err
		}
	}
	return query, nil
}

func (p *Performance) httpBuckets(ctx context.Context, query model.PerformanceQuery) ([]model.HTTPMetricBucket, error) {
	rangeQuery := performanceRange(query, 10000)
	var rows []model.HTTPMetricBucket
	var err error
	if query.Resolution == model.PerformanceResolutionHour {
		rows, err = p.store.ListHTTPHourly(ctx, rangeQuery)
	} else {
		rows, err = p.store.ListHTTP(ctx, rangeQuery)
	}
	if err != nil {
		return nil, fmt.Errorf("loading HTTP performance: %w", err)
	}
	if query.RouteFamily == "" {
		return rows, nil
	}
	filtered := make([]model.HTTPMetricBucket, 0, len(rows))
	for _, row := range rows {
		if row.RouteFamily == query.RouteFamily {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}

func performanceRange(query model.PerformanceQuery, limit int) model.PerformanceRange {
	return model.PerformanceRange{ServiceID: query.ServiceID, Start: query.Start, End: query.End, Limit: limit}
}

func summarizeHTTP(rows []model.HTTPMetricBucket) PerformanceSnapshot {
	var result PerformanceSnapshot
	var histogram model.HTTPHistogram
	for _, row := range rows {
		result.RequestCount += row.RequestCount
		result.ErrorCount += row.ErrorCount
		result.AverageMS += row.DurationTotalMS
		histogram.Merge(row.Histogram)
	}
	if result.RequestCount > 0 {
		result.AverageMS /= float64(result.RequestCount)
	}
	result.ErrorRate = percent(result.ErrorCount, result.RequestCount)
	result.P50MS = histogramPercentile(histogram, 0.50)
	result.P95MS = histogramPercentile(histogram, 0.95)
	result.P99MS = histogramPercentile(histogram, 0.99)
	return result
}

func histogramPercentile(histogram model.HTTPHistogram, quantile float64) float64 {
	var total int64
	for _, count := range histogram.Counts {
		total += count
	}
	if total == 0 {
		return 0
	}
	target := int64(math.Ceil(float64(total) * quantile))
	bounds := [...]float64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 5000}
	var seen int64
	for index, count := range histogram.Counts {
		seen += count
		if seen >= target {
			return bounds[index]
		}
	}
	return bounds[len(bounds)-1]
}

func percent(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}

func rankAnomalies(current, previous PerformanceSnapshot) []PerformanceAnomaly {
	result := []PerformanceAnomaly{}
	if previous.P95MS > 0 && current.P95MS > previous.P95MS {
		score := (current.P95MS - previous.P95MS) / previous.P95MS * 100
		result = append(result, PerformanceAnomaly{
			Kind: "latency", Score: score,
			Summary: "Higher p95 latency correlates with the selected window.",
		})
	}
	if current.ErrorRate > previous.ErrorRate && current.ErrorRate > 0 {
		result = append(result, PerformanceAnomaly{
			Kind: "errors", Score: current.ErrorRate - previous.ErrorRate,
			Summary: "Elevated HTTP errors correlate with the selected window.",
		})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Score > result[j].Score })
	return result
}

func groupRoutes(rows []model.HTTPMetricBucket) map[string][]model.HTTPMetricBucket {
	result := map[string][]model.HTTPMetricBucket{}
	for _, row := range rows {
		key := row.Method + "\x1f" + row.RouteFamily
		result[key] = append(result[key], row)
	}
	return result
}

func sortRoutes(rows []PerformanceRoute, sortField string) {
	sort.SliceStable(rows, func(i, j int) bool {
		switch sortField {
		case "request_count":
			return rows[i].RequestCount > rows[j].RequestCount
		case "error_rate":
			return rows[i].ErrorRate > rows[j].ErrorRate
		case "route_family":
			if rows[i].RouteFamily == rows[j].RouteFamily {
				return rows[i].Method < rows[j].Method
			}
			return rows[i].RouteFamily < rows[j].RouteFamily
		default:
			return rows[i].P95MS > rows[j].P95MS
		}
	})
}

func truncatePerformanceTime(value time.Time, resolution string) time.Time {
	if resolution == model.PerformanceResolutionHour {
		return value.UTC().Truncate(time.Hour)
	}
	return value.UTC().Truncate(time.Minute)
}
