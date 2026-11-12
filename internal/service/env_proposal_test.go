package service

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Keys and placeholder values of the proposal fixture. None of the
// values is real; "applied-token" stands in for a secret.
const (
	testPlainKey     = "LOG_LEVEL"
	testPlainValue   = "info"
	testSecretKey    = "SIGNING_TOKEN"
	testSecretValue  = "applied-token"
	testWrongValue   = "some-other-guess"
	testProposerName = "dev"
	testOtherName    = "dev-two"
)

// testProposer is a caller who may stage but not apply.
var testProposer = EnvActor{Name: testProposerName}

// proposal builds a staged change by a proposer.
func proposal(key, value string, isSecret bool) EnvChange {
	return EnvChange{ServiceID: testEnvServiceID, Key: key, Value: value, IsSecret: isSecret, By: testProposer}
}

// newProposalFixture applies one non-secret and one secret key as the
// reviewer, the state a developer meets.
func newProposalFixture(t *testing.T) *envFixture {
	t.Helper()

	fixture := newEnvFixture(t, healthyProber())
	fixture.stage(t, testPlainKey, testPlainValue, false)
	fixture.stage(t, testSecretKey, testSecretValue, true)

	run := fixture.apply(t, "seed")
	requireEqual(t, "seed run state", run.State, model.EnvRunSucceeded)

	return fixture
}

// storedEntry loads one working entry.
func (f *envFixture) storedEntry(t *testing.T, key string) model.EnvEntry {
	t.Helper()

	entry, err := store.NewEnvStore(f.db).GetEntry(context.Background(), testEnvServiceID, key)
	requireNoError(t, err, "GetEntry("+key+")")

	return entry
}

// auditTrail lists the fixture service's audit rows oldest first as
// "actor action result detail", the parts that must not vary.
func (f *envFixture) auditTrail(t *testing.T) []string {
	t.Helper()

	rows := f.auditActions(t)
	trail := make([]string, 0, len(rows))

	for _, row := range slices.Backward(rows) {
		trail = append(trail, strings.Join([]string{row.Actor, row.Action, row.Result, row.Detail}, " "))
	}

	return trail
}

func TestProposerStagesNewAndNonSecretKeys(t *testing.T) {
	t.Parallel()

	fixture := newProposalFixture(t)
	ctx := context.Background()

	added, err := fixture.env.Stage(ctx, proposal("FEATURE_FLAG", "on", false))
	requireNoError(t, err, "Stage(new key)")
	requireEqual(t, "new key state", added.State, model.EnvStateAdded)
	requireEqual(t, "new key author", added.UpdatedBy, testProposerName)

	changed, err := fixture.env.Stage(ctx, proposal(testPlainKey, "debug", false))
	requireNoError(t, err, "Stage(non-secret change)")
	requireEqual(t, "changed key state", changed.State, model.EnvStateChanged)
	requireEqual(t, "changed key author", changed.UpdatedBy, testProposerName)

	// Staging the applied value again withdraws the proposer's own change.
	withdrawn, err := fixture.env.Stage(ctx, proposal(testPlainKey, testPlainValue, false))
	requireNoError(t, err, "Stage(withdraw own change)")
	requireEqual(t, "withdrawn key state", withdrawn.State, model.EnvStateApplied)

	requireNoError(t, fixture.env.Remove(ctx, testEnvServiceID, testPlainKey, testProposer), "Remove(non-secret)")
	requireEqual(t, "removed key state", fixture.storedEntry(t, testPlainKey).State, model.EnvStateRemoved)
	requireEqual(t, "removed key author", fixture.storedEntry(t, testPlainKey).UpdatedBy, testProposerName)

	requireNoError(t, fixture.env.Remove(ctx, testEnvServiceID, "FEATURE_FLAG", testProposer), "Remove(own added key)")

	diff := fixture.diff(t)
	if len(diff.Added) != 0 || len(diff.Changed) != 0 || len(diff.Removed) != 1 || diff.Removed[0] != testPlainKey {
		t.Errorf("diff = %+v, want only %s removed", diff, testPlainKey)
	}
}

// A proposer is refused everything that touches a secret: a key whose
// applied or pending entry is flagged secret, and the flag itself.
func TestProposerIsRefusedEverySecretChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// arrange stages reviewer changes on top of the applied set.
		arrange func(t *testing.T, fixture *envFixture)
		key     string
		value   string
		flag    bool
	}{
		{name: "change an applied secret", key: testSecretKey, value: testWrongValue, flag: true},
		{name: "un-flag an applied secret", key: testSecretKey, value: testWrongValue, flag: false},
		{name: "add a new key flagged secret", key: "NEW_TOKEN", value: "anything", flag: true},
		{name: "flag a non-secret key secret", key: testPlainKey, value: testPlainValue, flag: true},
		{
			name: "a key a reviewer has pending as a new secret",
			arrange: func(t *testing.T, fixture *envFixture) {
				t.Helper()
				fixture.stage(t, "PENDING_TOKEN", "pending-placeholder", true)
			},
			key: "PENDING_TOKEN", value: testWrongValue, flag: false,
		},
		{
			name: "a non-secret key a reviewer is re-flagging secret",
			arrange: func(t *testing.T, fixture *envFixture) {
				t.Helper()
				fixture.stage(t, testPlainKey, "pending-placeholder", true)
			},
			key: testPlainKey, value: testWrongValue, flag: false,
		},
		{
			name: "an applied secret a reviewer is un-flagging",
			arrange: func(t *testing.T, fixture *envFixture) {
				t.Helper()
				fixture.stage(t, testSecretKey, "pending-placeholder", false)
			},
			key: testSecretKey, value: testWrongValue, flag: false,
		},
		{
			name: "an applied secret a reviewer is removing",
			arrange: func(t *testing.T, fixture *envFixture) {
				t.Helper()
				fixture.remove(t, testSecretKey)
			},
			key: testSecretKey, value: testWrongValue, flag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fixture := newProposalFixture(t)
			if tt.arrange != nil {
				tt.arrange(t, fixture)
			}

			ctx := context.Background()
			_, before := fixture.env.store.GetEntry(ctx, testEnvServiceID, tt.key)
			existed := before == nil

			var snapshot model.EnvEntry
			if existed {
				snapshot = fixture.storedEntry(t, tt.key)
			}

			_, err := fixture.env.Stage(ctx, proposal(tt.key, tt.value, tt.flag))
			requireIs(t, "Stage()", err, ErrEnvSecretRestricted)

			if !existed {
				_, err := fixture.env.store.GetEntry(ctx, testEnvServiceID, tt.key)
				requireIs(t, "GetEntry(after a refused add)", err, store.ErrEnvEntryNotFound)

				return
			}

			// Only a key that is itself secret refuses removal; a plain
			// key the proposer merely tried to flag may still be removed.
			if tt.flag && tt.key == testPlainKey {
				return
			}

			requireIs(t, "Remove()", fixture.env.Remove(ctx, testEnvServiceID, tt.key, testProposer), ErrEnvSecretRestricted)

			after := fixture.storedEntry(t, tt.key)
			if after.State != snapshot.State || after.UpdatedBy != snapshot.UpdatedBy ||
				after.IsSecret != snapshot.IsSecret || !bytes.Equal(after.Ciphertext, snapshot.Ciphertext) {
				t.Errorf("entry after refusals = %v, want it untouched (%v)", after, snapshot)
			}
		})
	}
}

// The equality oracle: on develop, staging the applied value answered
// "applied" with no audit row and any other value answered "changed".
// For a proposer every attempt on a secret key must be indistinguishable:
// same error, same audit rows, same stored entry, whatever was sent.
func TestProposerCannotCompareAGuessWithASecret(t *testing.T) {
	t.Parallel()

	attempts := []struct {
		name  string
		value string
		flag  bool
	}{
		{name: "the true value, flagged", value: testSecretValue, flag: true},
		{name: "the true value, unflagged", value: testSecretValue, flag: false},
		{name: "a wrong value, flagged", value: testWrongValue, flag: true},
		{name: "a wrong value, unflagged", value: testWrongValue, flag: false},
		{name: "an empty value", value: "", flag: true},
		{name: "a value the validator rejects", value: "two\nlines", flag: true},
		{name: "an oversized value", value: strings.Repeat("x", envMaxValueBytes+1), flag: true},
	}

	type outcome struct {
		err   string
		trail []string
		entry string
	}

	outcomes := make([]outcome, 0, len(attempts))

	for _, attempt := range attempts {
		// A fresh fixture per attempt: each starts from the same applied
		// state, so the trails are comparable row for row.
		fixture := newProposalFixture(t)
		baseline := len(fixture.auditTrail(t))

		_, err := fixture.env.Stage(context.Background(), proposal(testSecretKey, attempt.value, attempt.flag))
		requireIs(t, attempt.name, err, ErrEnvSecretRestricted)

		entry := fixture.storedEntry(t, testSecretKey)

		outcomes = append(outcomes, outcome{
			err: err.Error(), trail: fixture.auditTrail(t)[baseline:],
			entry: entry.String() + " by " + entry.UpdatedBy,
		})
	}

	first := outcomes[0]

	wantTrail := []string{testProposerName + " " + model.AuditEnvStage + " " + model.AuditFailure + " refused secret " + testSecretKey}
	if strings.Join(first.trail, "\n") != strings.Join(wantTrail, "\n") {
		t.Errorf("audit rows of a refused attempt = %q, want %q", first.trail, wantTrail)
	}

	wantEntry := "env entry " + testEnvServiceID + "/" + testSecretKey + " (applied, secret=true) by " + testEnvActor
	requireEqual(t, "secret entry after a refused attempt", first.entry, wantEntry)

	for i, got := range outcomes {
		if got.err != first.err {
			t.Errorf("%s: error %q, want the same as every other attempt (%q)", attempts[i].name, got.err, first.err)
		}

		if strings.Join(got.trail, "\n") != strings.Join(first.trail, "\n") {
			t.Errorf("%s: audit rows %q, want %q", attempts[i].name, got.trail, first.trail)
		}

		if got.entry != first.entry {
			t.Errorf("%s: stored entry %v, want %v", attempts[i].name, got.entry, first.entry)
		}

		for _, row := range got.trail {
			if strings.Contains(row, testSecretValue) || strings.Contains(row, testWrongValue) {
				t.Errorf("%s: audit row %q carries a value", attempts[i].name, row)
			}
		}

		if strings.Contains(got.err, testSecretValue) || strings.Contains(got.err, testWrongValue) {
			t.Errorf("%s: error %q carries a value", attempts[i].name, got.err)
		}
	}
}

// A proposer replaces or withdraws only their own pending changes. A
// reviewer may replace anyone's.
func TestProposerLeavesOtherPeoplesPendingChangesAlone(t *testing.T) {
	t.Parallel()

	fixture := newProposalFixture(t)
	ctx := context.Background()
	other := EnvActor{Name: testOtherName}

	_, err := fixture.env.Stage(ctx, proposal(testPlainKey, "debug", false))
	requireNoError(t, err, "Stage(first proposer)")

	_, err = fixture.env.Stage(ctx, proposal("FEATURE_FLAG", "on", false))
	requireNoError(t, err, "Stage(first proposer adds)")

	fixture.stage(t, "REVIEWER_KEY", "set-by-reviewer", false)

	for _, key := range []string{testPlainKey, "FEATURE_FLAG", "REVIEWER_KEY"} {
		_, err := fixture.env.Stage(ctx, EnvChange{ServiceID: testEnvServiceID, Key: key, Value: "taken-over", By: other})
		requireIs(t, "Stage("+key+") by another proposer", err, ErrEnvPendingByOther)
		requireIs(t, "Remove("+key+") by another proposer",
			fixture.env.Remove(ctx, testEnvServiceID, key, other), ErrEnvPendingByOther)
	}

	requireEqual(t, "author after the refusals", fixture.storedEntry(t, testPlainKey).UpdatedBy, testProposerName)

	_, err = fixture.env.Stage(ctx, proposal(testPlainKey, "warn", false))
	requireNoError(t, err, "Stage(own pending change again)")

	replaced := fixture.stage(t, testPlainKey, "error", false)
	requireEqual(t, "author after a reviewer replaces it", replaced.UpdatedBy, testEnvActor)

	_, err = fixture.env.Stage(ctx, proposal(testPlainKey, "debug", false))
	requireIs(t, "Stage() over the reviewer's change", err, ErrEnvPendingByOther)
}

func TestRenderedHashIsOnlyForThoseWhoMayApply(t *testing.T) {
	t.Parallel()

	fixture := newProposalFixture(t)
	ctx := context.Background()

	_, err := fixture.env.Stage(ctx, proposal(testPlainKey, "debug", false))
	requireNoError(t, err, "Stage()")

	withHash, err := fixture.env.Diff(ctx, testEnvServiceID, true)
	requireNoError(t, err, "Diff(with hash)")

	if len(withHash.RenderedSHA256) != 64 {
		t.Errorf("Diff(with hash).RenderedSHA256 = %q, want a sha256", withHash.RenderedSHA256)
	}

	plain, err := fixture.env.Diff(ctx, testEnvServiceID, false)
	requireNoError(t, err, "Diff(without hash)")
	requireEqual(t, "Diff(without hash).RenderedSHA256", plain.RenderedSHA256, "")
	requireEqual(t, "Diff(without hash) changed keys", strings.Join(plain.Changed, ","), testPlainKey)

	revisions, err := fixture.env.Revisions(ctx, testEnvServiceID, false)
	requireNoError(t, err, "Revisions(without hash)")

	for _, revision := range revisions {
		requireEqual(t, "Revisions(without hash).RenderedSHA256", revision.RenderedSHA256, "")
	}

	full, err := fixture.env.Revisions(ctx, testEnvServiceID, true)
	requireNoError(t, err, "Revisions(with hash)")

	if len(full) != 1 || len(full[0].RenderedSHA256) != 64 {
		t.Errorf("Revisions(with hash) = %+v, want one revision carrying its sha256", full)
	}
}

// Runs recorded before this change carry the digest in their render
// step, and new runs must not put it there (or on the event stream).
func TestRunStepsDoNotCarryTheRenderedHash(t *testing.T) {
	t.Parallel()

	fixture := newProposalFixture(t)
	ctx := context.Background()
	digest := strings.Repeat("ab", 32)

	for _, event := range fixture.events.all() {
		if strings.Contains(event.Message, "sha256") {
			t.Errorf("apply event %q carries the rendered hash", event.Message)
		}
	}

	legacy := model.EnvRun{
		ID: "legacy-run", ServiceID: testEnvServiceID, State: model.EnvRunSucceeded, StartedAt: time.Now(),
		Steps: []model.EnvRunStep{{
			Name: model.EnvStepRender, Status: model.EnvRunSucceeded,
			Message: "rendered 2 keys (sha256 " + digest + ")", At: time.Now(),
		}},
	}
	requireNoError(t, fixture.env.store.InsertRun(ctx, legacy), "InsertRun(legacy)")
	requireNoError(t, fixture.env.store.UpdateRun(ctx, legacy), "UpdateRun(legacy)")

	reader, err := fixture.env.Run(ctx, testEnvServiceID, legacy.ID, false)
	requireNoError(t, err, "Run(without hash)")
	requireEqual(t, "legacy render step for a reader", reader.Steps[0].Message, "rendered 2 keys")

	applier, err := fixture.env.Run(ctx, testEnvServiceID, legacy.ID, true)
	requireNoError(t, err, "Run(with hash)")

	if !strings.Contains(applier.Steps[0].Message, digest) {
		t.Errorf("legacy render step for an applier = %q, want it unchanged", applier.Steps[0].Message)
	}
}

// An apply that names the digest it reviewed is refused once anything
// else has been staged, so a late proposal cannot ride along unseen.
func TestApplyRefusesAPendingSetThatChangedAfterReview(t *testing.T) {
	t.Parallel()

	fixture := newProposalFixture(t)
	ctx := context.Background()

	_, err := fixture.env.Stage(ctx, proposal(testPlainKey, "debug", false))
	requireNoError(t, err, "Stage(reviewed proposal)")

	reviewed := fixture.diff(t).RenderedSHA256

	_, err = fixture.env.Stage(ctx, proposal("LATE_KEY", "slipped-in", false))
	requireNoError(t, err, "Stage(late proposal)")

	request := applyRequest(testEnvServiceID, "reviewed")
	request.ReviewedSHA256 = reviewed

	_, err = fixture.env.Apply(ctx, request)
	requireIs(t, "Apply(stale review)", err, ErrEnvPendingChanged)
	requireEqual(t, "late key state after the refused apply", fixture.storedEntry(t, "LATE_KEY").State, model.EnvStateAdded)

	request.ReviewedSHA256 = fixture.diff(t).RenderedSHA256

	runID, err := fixture.env.Apply(ctx, request)
	requireNoError(t, err, "Apply(current review)")
	requireEqual(t, "run state", fixture.await(t, runID).State, model.EnvRunSucceeded)
}
