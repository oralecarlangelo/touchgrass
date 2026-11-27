package store

import (
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestEnvFleetCounts(t *testing.T) {
	t.Parallel()

	env := NewEnvStore(openTestDB(t))
	mustUpsert(t, env, envEntry("VISIBLE_TEST_KEY", model.EnvStateApplied, false))
	mustUpsert(t, env, envEntry("STAGED_TEST_SECRET", model.EnvStateChanged, true))
	mustRevision(t, env, "fixture", "abc123", []model.EnvRevisionEntry{})

	counts, err := env.EnvFleetCounts(t.Context(), []string{testServiceAPI})
	if err != nil || len(counts) != 1 {
		t.Fatalf("summaries = %v, %v, want one scoped result", counts, err)
	}

	row := counts[0]
	if row.ServiceID != testServiceAPI || row.KeyCount != 2 || row.SecretCount != 1 || row.PendingCount != 1 {
		t.Errorf("summary = %+v, want two keys, one secret, and one pending change", row)
	}

	if row.LastAppliedAt == nil {
		t.Error("the last applied revision timestamp is missing")
	}
}
