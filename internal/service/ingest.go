package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// API key shape: "tg_" plus 32 hex chars from crypto/rand.
const (
	keyTag       = "tg_"
	keyRandBytes = 16
	keyLen       = 35
	// keyPrefixLen keeps "tg_" plus 8 hex chars, safe for logs.
	keyPrefixLen = 11
)

// Report caps applied before storage.
const (
	maxMessageLen   = 4096
	maxReleaseLen   = 128
	maxFieldLen     = 1024
	maxFrames       = 100
	maxCrumbs       = 100
	maxKeysListed   = 100
	maxIssueTitle   = 200
	maxIssuesListed = 100
)

// IngestorConfig wires an Ingestor.
type IngestorConfig struct {
	Services       *store.ServiceStore
	Keys           *store.KeyStore
	Occurrences    *store.OccurrenceStore
	Issues         *store.IssueStore
	IssueRules     *store.IssueRuleStore
	Logs           *store.LogStore
	MaxOccurrences int
	Logger         *slog.Logger
}

// Ingestor mints API keys, groups error reports into issues, and serves
// issue reads and alert rules.
type Ingestor struct {
	services       *store.ServiceStore
	keys           *store.KeyStore
	occurrences    *store.OccurrenceStore
	issues         *store.IssueStore
	issueRules     *store.IssueRuleStore
	logs           *store.LogStore
	maxOccurrences int
	logger         *slog.Logger
}

// NewIngestor builds an Ingestor.
func NewIngestor(cfg IngestorConfig) *Ingestor {
	return &Ingestor{
		services:       cfg.Services,
		keys:           cfg.Keys,
		occurrences:    cfg.Occurrences,
		issues:         cfg.Issues,
		issueRules:     cfg.IssueRules,
		logs:           cfg.Logs,
		maxOccurrences: cfg.MaxOccurrences,
		logger:         cfg.Logger,
	}
}

// CreatedKey pairs a stored key with its one-time plaintext.
type CreatedKey struct {
	Key       model.APIKey
	Plaintext string
}

// CreateKey mints one revocable key for a service. The plaintext is
// returned once and never stored.
func (in *Ingestor) CreateKey(ctx context.Context, serviceID string, rate float64) (CreatedKey, error) {
	if _, err := in.services.Get(ctx, serviceID); err != nil {
		return CreatedKey{}, err
	}

	if rate < 0 || rate > 1 {
		return CreatedKey{}, fmt.Errorf("%w: sample rate %v (want 0-1)", ErrInvalidInput, rate)
	}

	raw := make([]byte, keyRandBytes)

	if _, err := rand.Read(raw); err != nil {
		return CreatedKey{}, fmt.Errorf("minting api key: %w", err)
	}

	plaintext := keyTag + hex.EncodeToString(raw)

	stored, err := in.keys.Create(ctx, model.APIKey{
		ServiceID: serviceID, KeyHash: HashKey(plaintext),
		KeyPrefix: plaintext[:keyPrefixLen], SampleRate: rate,
	})
	if err != nil {
		return CreatedKey{}, fmt.Errorf("storing api key: %w", err)
	}

	return CreatedKey{Key: stored, Plaintext: plaintext}, nil
}

// Keys lists a service's keys newest first; hashes stay in the store.
func (in *Ingestor) Keys(ctx context.Context, serviceID string) ([]model.APIKey, error) {
	if _, err := in.services.Get(ctx, serviceID); err != nil {
		return nil, err
	}

	return in.keys.ListByService(ctx, serviceID, maxKeysListed)
}

// RevokeKey stamps a key revoked.
func (in *Ingestor) RevokeKey(ctx context.Context, id int64) error {
	return in.keys.Revoke(ctx, id)
}

// Ingest authenticates the presented key, samples, and stores the report.
// It returns false when the report is sampled out. Validation failures
// carry static messages so handlers never echo payload content.
func (in *Ingestor) Ingest(
	ctx context.Context,
	presented string,
	report model.IngestReport,
) (bool, error) {
	key, err := in.authenticate(ctx, presented)
	if err != nil {
		return false, err
	}

	occurrence, err := buildOccurrence(key.ServiceID, report)
	if err != nil {
		return false, err
	}

	keep, err := sampleLessThan(key.SampleRate)
	if err != nil {
		in.logger.Warn("sampling draw failed; keeping report", "service", key.ServiceID, "error", err)
	} else if !keep {
		return false, nil
	}

	fingerprint := Fingerprint(key.ServiceID, occurrence.Type, occurrence.Message, occurrence.Stack)

	if _, err := in.issues.GroupOccurrence(
		ctx, key.ServiceID, fingerprint, issueTitle(occurrence.Message), occurrence.Release, occurrence,
	); err != nil {
		return false, fmt.Errorf("storing occurrence: %w", err)
	}

	if trimmed, err := in.occurrences.TrimBeyondCap(ctx, key.ServiceID, in.maxOccurrences); err != nil {
		in.logger.Warn("occurrence cap trim failed", "service", key.ServiceID, "error", err)
	} else if trimmed > 0 {
		in.logger.Info("retention trimmed occurrences", "service", key.ServiceID, "rows", trimmed)
	}

	return true, nil
}

// authenticate resolves a presented key or rejects it without detail.
func (in *Ingestor) authenticate(ctx context.Context, presented string) (model.APIKey, error) {
	if len(presented) != keyLen || !strings.HasPrefix(presented, keyTag) {
		return model.APIKey{}, fmt.Errorf("%w: bad api key", ErrUnauthorized)
	}

	key, err := in.keys.ByHash(ctx, HashKey(presented))
	if err != nil {
		return model.APIKey{}, fmt.Errorf("%w: bad api key", ErrUnauthorized)
	}

	return key, nil
}

// buildOccurrence validates a report and normalizes it for storage.
func buildOccurrence(serviceID string, report model.IngestReport) (model.Occurrence, error) {
	switch report.Type {
	case model.OccurrenceException, model.OccurrenceMessage:
	default:
		return model.Occurrence{}, fmt.Errorf(
			"%w: report type (want exception or message)",
			ErrInvalidInput,
		)
	}

	if strings.TrimSpace(report.Message) == "" {
		return model.Occurrence{}, fmt.Errorf("%w: report message is required", ErrInvalidInput)
	}

	return model.Occurrence{
		ServiceID: serviceID, Type: report.Type,
		Message:     truncate(report.Message, maxMessageLen),
		Stack:       truncateFrames(report.Stack),
		Breadcrumbs: truncateCrumbs(report.Breadcrumbs),
		Release:     truncate(report.Release, maxReleaseLen),
	}, nil
}

// truncateFrames keeps the innermost frames with capped fields.
func truncateFrames(frames []model.StackFrame) []model.StackFrame {
	kept := make([]model.StackFrame, 0, min(len(frames), maxFrames))

	for _, frame := range frames[:min(len(frames), maxFrames)] {
		kept = append(kept, model.StackFrame{
			Function: truncate(frame.Function, maxFieldLen),
			File:     truncate(frame.File, maxFieldLen),
			Line:     frame.Line,
			Column:   frame.Column,
		})
	}

	return kept
}

// truncateCrumbs keeps the oldest crumbs with capped fields.
func truncateCrumbs(crumbs []model.Breadcrumb) []model.Breadcrumb {
	kept := make([]model.Breadcrumb, 0, min(len(crumbs), maxCrumbs))

	for _, crumb := range crumbs[:min(len(crumbs), maxCrumbs)] {
		kept = append(kept, model.Breadcrumb{
			At:       truncate(crumb.At, maxFieldLen),
			Category: truncate(crumb.Category, maxFieldLen),
			Message:  truncate(crumb.Message, maxFieldLen),
		})
	}

	return kept
}

// issueTitle reduces a message to its first line for issue display.
func issueTitle(message string) string {
	if line, _, _ := strings.Cut(message, "\n"); line != "" {
		return truncate(line, maxIssueTitle)
	}

	return truncate(message, maxIssueTitle)
}

// truncate caps a string at maximum bytes on a rune boundary.
func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}

	cut := 0

	for i := range value {
		if i > maximum {
			break
		}

		cut = i
	}

	return value[:cut]
}

// sampleLessThan draws a uniform float from crypto/rand and reports
// whether it falls below rate.
func sampleLessThan(rate float64) (bool, error) {
	var buf [8]byte

	if _, err := rand.Read(buf[:]); err != nil {
		return false, fmt.Errorf("drawing sample: %w", err)
	}

	draw := float64(binary.BigEndian.Uint64(buf[:])) / float64(math.MaxUint64)

	return draw < rate, nil
}

// HashKey hashes a plaintext key for storage and lookup.
func HashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))

	return hex.EncodeToString(sum[:])
}

// KeyPrefixForLog renders a presented key safe for logs: the public
// prefix, or a fixed token when malformed.
func KeyPrefixForLog(presented string) string {
	if len(presented) >= keyPrefixLen && strings.HasPrefix(presented, keyTag) {
		return presented[:keyPrefixLen]
	}

	return "malformed"
}
