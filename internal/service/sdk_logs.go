package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// SDK log batch bounds from the S20 wire contract.
const (
	maxSDKLogItems      = 1000
	maxSDKLogBody       = 8192
	maxSDKLogRelease    = 128
	maxSDKLogAttributes = 64
	maxSDKLogAttrKey    = 128
	maxSDKLogAttrString = 4096
	maxSDKSeverity      = 24
	minSDKSeverity      = 1
)

// unixSecondBounds reject missing (zero), pre-epoch, and absurd
// timestamps; the ceiling also catches millisecond-unit mistakes.
const (
	minUnixSeconds = 0
	maxUnixSeconds = 1e11
)

// severityByLevel infers the OTel severity number when an item omits
// it, following the Sentry Log protocol mapping.
var severityByLevel = map[string]int{
	model.SDKLogTrace: 1,
	model.SDKLogDebug: 5,
	model.SDKLogInfo:  9,
	model.SDKLogWarn:  13,
	model.SDKLogError: 17,
	model.SDKLogFatal: 21,
}

// sdkLogLevels is the valid level set for ingest and SDK queries:
// container levels plus trace and fatal.
var sdkLogLevels = map[string]bool{
	model.SDKLogTrace: true,
	model.SDKLogDebug: true,
	model.SDKLogInfo:  true,
	model.SDKLogWarn:  true,
	model.SDKLogError: true,
	model.SDKLogFatal: true,
}

// LogIngestorConfig wires a LogIngestor.
type LogIngestorConfig struct {
	Services *store.ServiceStore
	Keys     *store.KeyStore
	SDKLogs  *store.SDKLogStore
}

// LogIngestor ingests batched SDK logs under the same per-service keys
// as error reports, and serves SDK log reads.
type LogIngestor struct {
	services *store.ServiceStore
	keys     *store.KeyStore
	sdkLogs  *store.SDKLogStore
}

// NewLogIngestor builds a LogIngestor.
func NewLogIngestor(cfg LogIngestorConfig) *LogIngestor {
	return &LogIngestor{
		services: cfg.Services,
		keys:     cfg.Keys,
		sdkLogs:  cfg.SDKLogs,
	}
}

// authenticateKey resolves a presented tg_ key or rejects it without
// detail. Error ingest and log ingest share this one path.
func authenticateKey(
	ctx context.Context,
	keys *store.KeyStore,
	presented string,
) (model.APIKey, error) {
	if len(presented) != keyLen || !strings.HasPrefix(presented, keyTag) {
		return model.APIKey{}, fmt.Errorf("%w: bad api key", ErrUnauthorized)
	}

	key, err := keys.ByHash(ctx, HashKey(presented))
	if err != nil {
		return model.APIKey{}, fmt.Errorf("%w: bad api key", ErrUnauthorized)
	}

	return key, nil
}

// SDKLogItemError reports which batch item failed validation. Message
// is static so handlers never echo payload content.
type SDKLogItemError struct {
	Index   int
	Message string
}

// Error renders the item failure.
func (e *SDKLogItemError) Error() string {
	return fmt.Sprintf("%s: item %d: %s", ErrInvalidInput.Error(), e.Index, e.Message)
}

// Unwrap exposes ErrInvalidInput for status mapping.
func (e *SDKLogItemError) Unwrap() error {
	return ErrInvalidInput
}

// IngestLogs authenticates the presented key, validates every item,
// and stores the batch. Validation is all-or-nothing: any invalid
// item rejects the batch with its index, storing nothing.
func (l *LogIngestor) IngestLogs(
	ctx context.Context,
	presented string,
	batch model.SDKLogBatch,
) (int, error) {
	key, err := authenticateKey(ctx, l.keys, presented)
	if err != nil {
		return 0, err
	}

	rows, err := buildSDKLogs(key.ServiceID, batch)
	if err != nil {
		return 0, err
	}

	if _, err := l.sdkLogs.InsertBatch(ctx, rows); err != nil {
		return 0, fmt.Errorf("storing sdk logs: %w", err)
	}

	return len(rows), nil
}

// buildSDKLogs validates a batch and normalizes its rows for storage.
func buildSDKLogs(serviceID string, batch model.SDKLogBatch) ([]model.SDKLog, error) {
	if len(batch.Items) == 0 || len(batch.Items) > maxSDKLogItems {
		return nil, fmt.Errorf("%w: items (want 1-1000)", ErrInvalidInput)
	}

	release := truncate(batch.Release, maxSDKLogRelease)
	rows := make([]model.SDKLog, 0, len(batch.Items))

	for index, item := range batch.Items {
		row, err := buildSDKLog(serviceID, release, item)
		if err != nil {
			return nil, &SDKLogItemError{Index: index, Message: err.Error()}
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// buildSDKLog validates one item and normalizes it for storage.
func buildSDKLog(serviceID, release string, item model.SDKLogItem) (model.SDKLog, error) {
	if item.Timestamp <= minUnixSeconds || item.Timestamp >= maxUnixSeconds {
		return model.SDKLog{}, errors.New("timestamp (want positive unix seconds)")
	}

	severity, ok := severityByLevel[item.Level]
	if !ok {
		return model.SDKLog{}, errors.New("level (want trace, debug, info, warn, error, or fatal)")
	}

	if len(item.Body) == 0 || len(item.Body) > maxSDKLogBody {
		return model.SDKLog{}, errors.New("body (want 1-8192 bytes)")
	}

	if item.SeverityNumber != nil {
		if *item.SeverityNumber < minSDKSeverity || *item.SeverityNumber > maxSDKSeverity {
			return model.SDKLog{}, errors.New("severity_number (want 1-24)")
		}

		severity = *item.SeverityNumber
	}

	if err := checkHexID(item.TraceID, 32, "trace_id"); err != nil {
		return model.SDKLog{}, err
	}

	if err := checkHexID(item.SpanID, 16, "span_id"); err != nil {
		return model.SDKLog{}, err
	}

	attributes, err := buildSDKAttributes(item.Attributes)
	if err != nil {
		return model.SDKLog{}, err
	}

	whole, frac := math.Modf(item.Timestamp)

	return model.SDKLog{
		ServiceID:  serviceID,
		Ts:         time.Unix(int64(whole), int64(frac*1e9)).UTC(),
		Level:      item.Level,
		Severity:   severity,
		Message:    item.Body,
		Attributes: attributes,
		TraceID:    item.TraceID,
		SpanID:     item.SpanID,
		Release:    release,
	}, nil
}

// checkHexID validates an optional lowercase-hex correlation id.
func checkHexID(value string, length int, name string) error {
	if value == "" {
		return nil
	}

	if len(value) != length {
		return fmt.Errorf("%s (want %d lowercase hex chars)", name, length)
	}

	for _, digit := range value {
		isDigit := digit >= '0' && digit <= '9'
		isLowerHex := digit >= 'a' && digit <= 'f'

		if !isDigit && !isLowerHex {
			return fmt.Errorf("%s (want %d lowercase hex chars)", name, length)
		}
	}

	return nil
}

// buildSDKAttributes validates attribute keys and typed values.
func buildSDKAttributes(
	attributes map[string]model.SDKLogAttribute,
) (map[string]model.SDKLogAttribute, error) {
	if len(attributes) > maxSDKLogAttributes {
		return nil, errors.New("attributes (want at most 64)")
	}

	kept := make(map[string]model.SDKLogAttribute, len(attributes))

	for key, attribute := range attributes {
		if len(key) == 0 || len(key) > maxSDKLogAttrKey {
			return nil, errors.New("attribute key (want 1-128 bytes)")
		}

		if err := checkAttributeValue(attribute); err != nil {
			return nil, err
		}

		kept[key] = attribute
	}

	return kept, nil
}

// checkAttributeValue validates one typed attribute value against its
// declared type.
func checkAttributeValue(attribute model.SDKLogAttribute) error {
	switch attribute.Type {
	case model.SDKAttrString:
		text, ok := attribute.Value.(string)
		if !ok {
			return errors.New("attribute type string with non-string value")
		}

		if len(text) > maxSDKLogAttrString {
			return errors.New("attribute string (want at most 4096 bytes)")
		}
	case model.SDKAttrInteger:
		number, ok := attribute.Value.(float64)
		if !ok || number != math.Trunc(number) {
			return errors.New("attribute type integer with non-integer value")
		}
	case model.SDKAttrDouble:
		if _, ok := attribute.Value.(float64); !ok {
			return errors.New("attribute type double with non-number value")
		}
	case model.SDKAttrBoolean:
		if _, ok := attribute.Value.(bool); !ok {
			return errors.New("attribute type boolean with non-boolean value")
		}
	default:
		return fmt.Errorf(
			"attribute type %q (want string, integer, double, or boolean)",
			attribute.Type,
		)
	}

	return nil
}

// SDKLogSearch carries SDK log search options. Level is a
// comma-separated set over trace, debug, info, warn, error, fatal.
type SDKLogSearch struct {
	ServiceID string
	Query     string
	Level     string
	TraceID   string
	Since     *time.Time
	Until     *time.Time
	Limit     int
}

// SearchLogs validates the service and searches its SDK rows newest
// first. TraceID, when set, must be a full 32-char lowercase hex id.
func (l *LogIngestor) SearchLogs(ctx context.Context, search SDKLogSearch) ([]model.SDKLog, error) {
	if _, err := l.services.Get(ctx, search.ServiceID); err != nil {
		return nil, err
	}

	if search.TraceID != "" {
		if err := checkHexID(search.TraceID, 32, "trace_id"); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}
	}

	levels, err := normalizeSDKLogLevels(search.Level)
	if err != nil {
		return nil, err
	}

	return l.sdkLogs.Search(ctx, store.SDKLogFilter{
		ServiceID: search.ServiceID, Query: search.Query, Levels: levels,
		TraceID: search.TraceID, Since: search.Since, Until: search.Until,
		Limit: clampSearchLimit(search.Limit),
	})
}

// normalizeSDKLogLevels parses a comma-separated level filter into a
// sorted list, rejecting unknown levels. Empty input selects all.
func normalizeSDKLogLevels(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	seen := map[string]bool{}
	levels := []string{}

	for part := range strings.SplitSeq(raw, ",") {
		level := strings.ToLower(strings.TrimSpace(part))

		if level == "" {
			continue
		}

		if !sdkLogLevels[level] {
			return nil, fmt.Errorf(
				"%w: level %q (want trace, debug, info, warn, error, or fatal)",
				ErrInvalidInput, part,
			)
		}

		if !seen[level] {
			seen[level] = true
			levels = append(levels, level)
		}
	}

	return levels, nil
}
