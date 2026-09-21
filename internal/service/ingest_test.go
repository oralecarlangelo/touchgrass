package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// testIngestor builds an Ingestor on the seeded store.
func testIngestor(t *testing.T, maxOccurrences int) (*Ingestor, *store.DB) {
	t.Helper()

	db := openInventoryDB(t)
	services := store.NewServiceStore(db)

	ingestor := NewIngestor(IngestorConfig{
		Services:       services,
		Keys:           store.NewKeyStore(db),
		Occurrences:    store.NewOccurrenceStore(db),
		Issues:         store.NewIssueStore(db),
		IssueRules:     store.NewIssueRuleStore(db),
		Logs:           store.NewLogStore(db),
		MaxOccurrences: maxOccurrences,
		Logger:         slog.New(slog.DiscardHandler),
	})

	return ingestor, db
}

// testReport builds one valid exception report.
func testReport() model.IngestReport {
	return model.IngestReport{
		Type:    model.OccurrenceException,
		Message: "boom",
		Stack: []model.StackFrame{
			{Function: "handler", File: "app.js", Line: 10, Column: 3},
		},
		Breadcrumbs: []model.Breadcrumb{
			{At: "2026-09-21T00:00:00Z", Category: "nav", Message: "route"},
		},
		Release: "v1.2.3",
	}
}

// mintKey creates a key or fails the test, returning its plaintext.
func mintKey(t *testing.T, ingestor *Ingestor, serviceID string, rate float64) string {
	t.Helper()

	created, err := ingestor.CreateKey(context.Background(), serviceID, rate)
	if err != nil {
		t.Fatalf("CreateKey() error = %v, want nil", err)
	}

	return created.Plaintext
}

func TestCreateKey(t *testing.T) {
	t.Parallel()

	ingestor, _ := testIngestor(t, 10)
	ctx := context.Background()

	created, err := ingestor.CreateKey(ctx, testServiceAPI, 0.5)
	if err != nil {
		t.Fatalf("CreateKey() error = %v, want nil", err)
	}

	if len(created.Plaintext) != keyLen || !strings.HasPrefix(created.Plaintext, keyTag) {
		t.Errorf("plaintext = %q, want tg_ plus 32 hex chars", created.Plaintext)
	}

	if created.Key.KeyPrefix != created.Plaintext[:keyPrefixLen] {
		t.Errorf("prefix = %q, want leading plaintext", created.Key.KeyPrefix)
	}

	if created.Key.KeyHash == "" || created.Key.KeyHash == created.Plaintext {
		t.Error("hash missing or plaintext, want sha256 hex")
	}

	other, err := ingestor.CreateKey(ctx, testServiceAPI, 0.5)
	if err != nil {
		t.Fatalf("second CreateKey() error = %v, want nil", err)
	}

	if other.Plaintext == created.Plaintext {
		t.Error("plaintexts match, want unique keys")
	}

	if _, err := ingestor.CreateKey(ctx, testUnknownServiceID, 1); err == nil {
		t.Error("CreateKey() error = nil, want unknown service error")
	}

	for _, rate := range []float64{-0.1, 1.1} {
		if _, err := ingestor.CreateKey(ctx, testServiceAPI, rate); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CreateKey(%v) error = %v, want ErrInvalidInput", rate, err)
		}
	}
}

func TestIngestorKeys(t *testing.T) {
	t.Parallel()

	ingestor, _ := testIngestor(t, 10)
	ctx := context.Background()

	if _, err := ingestor.Keys(ctx, testUnknownServiceID); err == nil {
		t.Error("Keys() error = nil, want unknown service error")
	}

	mintKey(t, ingestor, testServiceAPI, 1)

	keys, err := ingestor.Keys(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("Keys() error = %v, want nil", err)
	}

	if len(keys) != 1 || keys[0].KeyHash != "" {
		t.Errorf("Keys() = %+v, want one key without hash", keys)
	}
}

func TestRevokeKey(t *testing.T) {
	t.Parallel()

	ingestor, _ := testIngestor(t, 10)
	ctx := context.Background()

	plaintext := mintKey(t, ingestor, testServiceAPI, 1)

	keys, err := ingestor.Keys(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("Keys() error = %v, want nil", err)
	}

	if err := ingestor.RevokeKey(ctx, keys[0].ID); err != nil {
		t.Fatalf("RevokeKey() error = %v, want nil", err)
	}

	if _, err := ingestor.Ingest(ctx, plaintext, testReport()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Ingest() error = %v, want ErrUnauthorized after revoke", err)
	}

	if err := ingestor.RevokeKey(ctx, 9999); !errors.Is(err, store.ErrKeyNotFound) {
		t.Errorf("RevokeKey() error = %v, want ErrKeyNotFound", err)
	}
}

func TestIngest(t *testing.T) {
	t.Parallel()

	ingestor, db := testIngestor(t, 10)
	ctx := context.Background()
	plaintext := mintKey(t, ingestor, testServiceAPI, 1)

	sampled, err := ingestor.Ingest(ctx, plaintext, testReport())
	if err != nil {
		t.Fatalf("Ingest() error = %v, want nil", err)
	}

	if !sampled {
		t.Error("Ingest() sampled = false, want true at rate 1")
	}

	count, err := store.NewOccurrenceStore(db).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 1 {
		t.Errorf("CountByService() = %d, want 1", count)
	}
}

func TestIngestSampledOut(t *testing.T) {
	t.Parallel()

	ingestor, db := testIngestor(t, 10)
	ctx := context.Background()
	plaintext := mintKey(t, ingestor, testServiceAPI, 0)

	sampled, err := ingestor.Ingest(ctx, plaintext, testReport())
	if err != nil {
		t.Fatalf("Ingest() error = %v, want nil", err)
	}

	if sampled {
		t.Error("Ingest() sampled = true, want false at rate 0")
	}

	count, err := store.NewOccurrenceStore(db).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 0 {
		t.Errorf("CountByService() = %d, want 0 after sample-out", count)
	}
}

func TestIngestRejects(t *testing.T) {
	t.Parallel()

	ingestor, _ := testIngestor(t, 10)
	ctx := context.Background()
	plaintext := mintKey(t, ingestor, testServiceAPI, 1)
	unknown := keyTag + strings.Repeat("a", 32)

	t.Run("empty key", func(t *testing.T) {
		t.Parallel()

		if _, err := ingestor.Ingest(ctx, "", testReport()); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("Ingest() error = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("malformed key", func(t *testing.T) {
		t.Parallel()

		if _, err := ingestor.Ingest(ctx, "bogus", testReport()); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("Ingest() error = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("unknown key", func(t *testing.T) {
		t.Parallel()

		if _, err := ingestor.Ingest(ctx, unknown, testReport()); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("Ingest() error = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("bad key error hides payload", func(t *testing.T) {
		t.Parallel()

		report := testReport()
		report.Message = "payload-marker-xyz"

		_, err := ingestor.Ingest(ctx, unknown, report)
		if err == nil {
			t.Fatal("Ingest() error = nil, want ErrUnauthorized")
		}

		if strings.Contains(err.Error(), "payload-marker-xyz") {
			t.Errorf("Ingest() error = %q, want no payload content", err)
		}
	})

	t.Run("bad type", func(t *testing.T) {
		t.Parallel()

		report := testReport()
		report.Type = "trace"

		if _, err := ingestor.Ingest(ctx, plaintext, report); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Ingest() error = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("empty message", func(t *testing.T) {
		t.Parallel()

		report := testReport()
		report.Message = "   "

		if _, err := ingestor.Ingest(ctx, plaintext, report); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Ingest() error = %v, want ErrInvalidInput", err)
		}
	})
}

func TestIngestTruncates(t *testing.T) {
	t.Parallel()

	ingestor, db := testIngestor(t, 1000)
	ctx := context.Background()
	plaintext := mintKey(t, ingestor, testServiceAPI, 1)

	frames := make([]model.StackFrame, 0, maxFrames+50)
	for range maxFrames + 50 {
		frames = append(frames, model.StackFrame{Function: "f", File: "app.js", Line: 1})
	}

	report := testReport()
	report.Message = strings.Repeat("m", maxMessageLen+100)
	report.Release = strings.Repeat("r", maxReleaseLen+10)
	report.Stack = frames

	sampled, err := ingestor.Ingest(ctx, plaintext, report)
	if err != nil {
		t.Fatalf("Ingest() error = %v, want nil", err)
	}

	if !sampled {
		t.Fatal("Ingest() sampled = false, want true at rate 1")
	}

	listed, err := store.NewOccurrenceStore(db).ListByService(ctx, testServiceAPI, 1)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	got := listed[0]
	if len(got.Message) != maxMessageLen {
		t.Errorf("message len = %d, want %d", len(got.Message), maxMessageLen)
	}

	if len(got.Release) != maxReleaseLen {
		t.Errorf("release len = %d, want %d", len(got.Release), maxReleaseLen)
	}

	if len(got.Stack) != maxFrames {
		t.Errorf("stack len = %d, want %d", len(got.Stack), maxFrames)
	}
}

func TestIngestEnforcesCap(t *testing.T) {
	t.Parallel()

	ingestor, db := testIngestor(t, 2)
	ctx := context.Background()
	plaintext := mintKey(t, ingestor, testServiceAPI, 1)

	for range 3 {
		if _, err := ingestor.Ingest(ctx, plaintext, testReport()); err != nil {
			t.Fatalf("Ingest() error = %v, want nil", err)
		}
	}

	listed, err := store.NewOccurrenceStore(db).ListByService(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(listed) != 2 {
		t.Fatalf("ListByService() = %d rows, want 2 newest", len(listed))
	}

	if listed[0].ID < listed[1].ID {
		t.Error("ListByService() keeps oldest, want newest kept")
	}
}

func TestHashKey(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for range 2 {
		seen[HashKey("tg_test")] = true
	}

	if len(seen) != 1 {
		t.Error("HashKey() unstable, want deterministic")
	}

	if HashKey("tg_one") == HashKey("tg_two") {
		t.Error("HashKey() collision, want distinct hashes")
	}
}

func TestKeyPrefixForLog(t *testing.T) {
	t.Parallel()

	plaintext := keyTag + strings.Repeat("b", 32)

	if got := KeyPrefixForLog(plaintext); got != plaintext[:keyPrefixLen] {
		t.Errorf("KeyPrefixForLog() = %q, want leading prefix", got)
	}

	for _, presented := range []string{"", "short", "xx_" + strings.Repeat("c", 32)} {
		if got := KeyPrefixForLog(presented); got != "malformed" {
			t.Errorf("KeyPrefixForLog(%q) = %q, want malformed", presented, got)
		}
	}
}
