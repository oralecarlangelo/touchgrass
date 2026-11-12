package http

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// Keys and placeholder values of the proposal suites. None of the
// values is real; testProposalSecretValue stands in for a secret.
const (
	testProposalPlainKey    = "LOG_LEVEL"
	testProposalPlainValue  = "info"
	testProposalSecretKey   = "SIGNING_TOKEN"
	testProposalSecretValue = "applied-placeholder"
	testProposalWrongValue  = "some-other-guess"
	testHashField           = "rendered_sha256"
	// testSecretRestricted is the one error code of a refused attempt
	// on a secret key.
	testSecretRestricted = "env_secret_restricted" //nolint:gosec // G101: an error code, not a credential.
	// Labels of the callers the suites loop over.
	testLabelScopedDev   = "scoped developer"
	testLabelFleetDev    = "all-services developer"
	testLabelFleetViewer = "all-services viewer"
)

// envBody renders a stage body.
func envBody(t *testing.T, value string, isSecret bool) string {
	t.Helper()

	raw, err := json.Marshal(envEntryRequest{Value: value, IsSecret: isSecret})
	if err != nil {
		t.Fatalf("encoding the stage body: %v", err)
	}

	return string(raw)
}

// envRoute builds /api/services/{id}/env{suffix}.
func envRoute(serviceID, suffix string) string {
	return servicePath(serviceID, "env") + suffix
}

// configureEnv gives a generic service an env block, as an admin would
// when registering it. No commands are configured, so an apply only
// renders and writes to the in-memory host.
func configureEnv(t *testing.T, db *store.DB, serviceID string) {
	t.Helper()

	services := store.NewServiceStore(db)

	def, err := services.Get(context.Background(), serviceID)
	if err != nil {
		t.Fatalf("Get(%s) error = %v, want nil", serviceID, err)
	}

	cfg := map[string]any{}

	if err := json.Unmarshal(def.Config, &cfg); err != nil {
		t.Fatalf("decoding the service config: %v", err)
	}

	cfg["env"] = model.EnvConfig{Target: model.EnvTarget{Path: "/srv/env/" + serviceID + ".env", Mode: "0600"}}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encoding the service config: %v", err)
	}

	if err := services.UpdateConfig(context.Background(), serviceID, raw); err != nil {
		t.Fatalf("UpdateConfig(%s) error = %v, want nil", serviceID, err)
	}
}

// applyEnvAs applies the pending changes of serviceID and waits for the
// run to finish.
func applyEnvAs(t *testing.T, fixture scopedFixture, cookie *nethttp.Cookie, serviceID string) string {
	t.Helper()

	def, err := store.NewServiceStore(fixture.db).Get(context.Background(), serviceID)
	if err != nil {
		t.Fatalf("Get(%s) error = %v, want nil", serviceID, err)
	}

	raw, err := json.Marshal(envApplyRequest{Confirm: def.Name})
	if err != nil {
		t.Fatalf("encoding the apply body: %v", err)
	}

	body := expect(t, fixture.server.handler, cookie, nethttp.MethodPost, envRoute(serviceID, "/apply"),
		string(raw), nethttp.StatusAccepted)
	runID := decodeBodyInto[envRunResponse](t, body).RunID

	select {
	case <-fixture.server.env.Done(runID):
	case <-time.After(10 * time.Second):
		t.Fatal("env run did not finish")
	}

	return runID
}

// newProposalFixture is the scoped fixture with env configured on both
// generic services and, on each, one applied non-secret key and one
// applied secret key staged by the admin.
func newProposalFixture(t *testing.T) (scopedFixture, *nethttp.Cookie) {
	t.Helper()

	fixture := newScopedFixture(t)
	admin := authCookie(t, fixture.server)

	for _, serviceID := range []string{testfixture.GenericGranted, testfixture.GenericOther} {
		configureEnv(t, fixture.db, serviceID)

		expect(t, fixture.server.handler, admin, nethttp.MethodPut,
			envRoute(serviceID, "/entries/"+testProposalPlainKey),
			envBody(t, testProposalPlainValue, false), nethttp.StatusOK)
		expect(t, fixture.server.handler, admin, nethttp.MethodPut,
			envRoute(serviceID, "/entries/"+testProposalSecretKey),
			envBody(t, testProposalSecretValue, true), nethttp.StatusOK)

		applyEnvAs(t, fixture, admin, serviceID)
	}

	return fixture, admin
}

// viewEntry loads one entry of a service's env view as cookie's account.
func viewEntry(t *testing.T, fixture scopedFixture, cookie *nethttp.Cookie, serviceID, key string) model.EnvEntryView {
	t.Helper()

	body := expect(t, fixture.server.handler, cookie, nethttp.MethodGet, envRoute(serviceID, ""), "", nethttp.StatusOK)

	for _, entry := range decodeBodyInto[model.EnvView](t, body).Entries {
		if entry.Key == key {
			return entry
		}
	}

	t.Fatalf("env view of %s has no %s", serviceID, key)

	return model.EnvEntryView{}
}

// envAuditTrail lists a service's env audit rows oldest first as
// "actor action result detail".
func envAuditTrail(t *testing.T, db *store.DB, serviceID string) []string {
	t.Helper()

	trail := []string{}

	for _, row := range auditOf(t, db, serviceID) {
		if strings.HasPrefix(row.Action, "env.") {
			trail = append(trail, strings.Join([]string{row.Actor, row.Action, row.Result, row.Detail}, " "))
		}
	}

	return trail
}

// envRouteAccess is what each kind of account gets from one env route:
// "reach" (past the guard) or the status the guard answered.
type envRouteAccess struct {
	route            string
	scopedViewer     string
	developerInScope string
	developerOutside string
	fleetDeveloper   string
}

// envRouteAccessTable names every env route and states who reaches it.
// Only the two stage routes differ from v0.6.0, and only for a
// developer; apply, discard, import, reveal and revision rollback stay
// closed to every non-admin.
var envRouteAccessTable = []envRouteAccess{
	{"GET /api/services/{id}/env", matrixReach, matrixReach, "403", matrixReach},
	{"PUT /api/services/{id}/env/entries/{key}", "403", matrixReach, "403", matrixReach},
	{"DELETE /api/services/{id}/env/entries/{key}", "403", matrixReach, "403", matrixReach},
	{"POST /api/services/{id}/env/entries/{key}/reveal", "403", "403", "403", "403"},
	{"GET /api/services/{id}/env/diff", matrixReach, matrixReach, "403", matrixReach},
	{"POST /api/services/{id}/env/discard", "403", "403", "403", "403"},
	{"POST /api/services/{id}/env/import", "403", "403", "403", "403"},
	{"POST /api/services/{id}/env/apply", "403", "403", "403", "403"},
	{"GET /api/services/{id}/env/runs/{run_id}", matrixReach, matrixReach, "403", matrixReach},
	{"GET /api/services/{id}/env/revisions", matrixReach, matrixReach, "403", matrixReach},
	{"POST /api/services/{id}/env/revisions/{rev}/rollback", "403", "403", "403", "403"},
}

// Every env route, called as a scoped viewer, a scoped developer in and
// out of scope, and an all-services developer. A new env route that is
// not named in the table fails here.
func TestEveryEnvRouteByRoleAndScope(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := guardHarness(fixture.server)
	granted, other := testfixture.GenericGranted, testfixture.GenericOther

	viewer := fixture.cookieFor(t, testScopedUsername)
	developer := fixture.cookieFor(t, testDevUsername)
	fleetDeveloper := fixture.cookieFor(t, testFleetDevUsername)

	want := map[string]envRouteAccess{}
	for _, row := range envRouteAccessTable {
		want[row.route] = row
	}

	seen := []string{}

	for _, rt := range fixture.server.routes() {
		if !strings.Contains(rt.pattern, "/env") {
			continue
		}

		name := routeName(rt)
		seen = append(seen, name)

		expected, listed := want[name]
		if !listed {
			t.Errorf("%s is an env route the access table does not name", name)

			continue
		}

		got := envRouteAccess{
			route:            name,
			scopedViewer:     observeRoute(t, handler, viewer, rt, granted, 0),
			developerInScope: observeRoute(t, handler, developer, rt, granted, 0),
			developerOutside: observeRoute(t, handler, developer, rt, other, 0),
			fleetDeveloper:   observeRoute(t, handler, fleetDeveloper, rt, other, 0),
		}

		if got != expected {
			t.Errorf("%s = %+v, want %+v", name, got, expected)
		}
	}

	if len(seen) != len(envRouteAccessTable) {
		t.Errorf("the route table has %d env routes %v, the access table names %d", len(seen), seen, len(envRouteAccessTable))
	}
}

// With the real handlers: a developer adds a key, changes and removes a
// non-secret key in scope, each attributed to them, and the admin sees
// who proposed what.
func TestDeveloperProposesNonSecretEnvChanges(t *testing.T) {
	t.Parallel()

	fixture, admin := newProposalFixture(t)
	handler := fixture.server.handler
	developer := fixture.cookieFor(t, testDevUsername)
	granted := testfixture.GenericGranted

	added := decodeBodyInto[model.EnvEntryView](t, expect(t, handler, developer, nethttp.MethodPut,
		envRoute(granted, "/entries/FEATURE_FLAG"), envBody(t, "on", false), nethttp.StatusOK))
	if added.State != model.EnvStateAdded || added.UpdatedBy != testDevUsername || added.IsSecret {
		t.Errorf("added entry = %+v, want a non-secret addition by %s", added, testDevUsername)
	}

	changed := decodeBodyInto[model.EnvEntryView](t, expect(t, handler, developer, nethttp.MethodPut,
		envRoute(granted, "/entries/"+testProposalPlainKey), envBody(t, "debug", false), nethttp.StatusOK))
	if changed.State != model.EnvStateChanged || changed.UpdatedBy != testDevUsername {
		t.Errorf("changed entry = %+v, want a change by %s", changed, testDevUsername)
	}

	for _, key := range []string{"FEATURE_FLAG", testProposalPlainKey} {
		if seen := viewEntry(t, fixture, admin, granted, key); seen.UpdatedBy != testDevUsername {
			t.Errorf("admin sees %s staged by %q, want %s", key, seen.UpdatedBy, testDevUsername)
		}
	}

	trail := envAuditTrail(t, fixture.db, granted)
	for _, want := range []string{
		testDevUsername + " env.stage success added FEATURE_FLAG",
		testDevUsername + " env.stage success changed " + testProposalPlainKey,
	} {
		if !slices.Contains(trail, want) {
			t.Errorf("audit trail %q lacks %q", trail, want)
		}
	}

	expect(t, handler, developer, nethttp.MethodDelete, envRoute(granted, "/entries/FEATURE_FLAG"), "", nethttp.StatusNoContent)
	expect(t, handler, developer, nethttp.MethodDelete, envRoute(granted, "/entries/"+testProposalPlainKey), "", nethttp.StatusNoContent)

	if removed := viewEntry(t, fixture, admin, granted, testProposalPlainKey); removed.State != model.EnvStateRemoved {
		t.Errorf("%s after the developer's removal = %s, want removed", testProposalPlainKey, removed.State)
	}
}

// Outside scope the guard answers before the handler, whatever the key,
// and nothing is recorded against the service.
func TestDeveloperCannotProposeOutsideScope(t *testing.T) {
	t.Parallel()

	fixture, _ := newProposalFixture(t)
	handler := fixture.server.handler
	developer := fixture.cookieFor(t, testDevUsername)
	other := testfixture.GenericOther
	baseline := envAuditTrail(t, fixture.db, other)

	expect(t, handler, developer, nethttp.MethodPut, envRoute(other, "/entries/FEATURE_FLAG"),
		envBody(t, "on", false), nethttp.StatusForbidden)
	expect(t, handler, developer, nethttp.MethodDelete, envRoute(other, "/entries/"+testProposalPlainKey), "", nethttp.StatusForbidden)

	if after := envAuditTrail(t, fixture.db, other); !slices.Equal(after, baseline) {
		t.Errorf("out-of-scope attempts wrote audit rows: %q", after[len(baseline):])
	}
}

// The admin applies what a developer proposed; apply itself is
// unchanged.
func TestAdminAppliesADevelopersProposal(t *testing.T) {
	t.Parallel()

	fixture, admin := newProposalFixture(t)
	handler := fixture.server.handler
	developer := fixture.cookieFor(t, testDevUsername)
	granted := testfixture.GenericGranted

	expect(t, handler, developer, nethttp.MethodPut, envRoute(granted, "/entries/FEATURE_FLAG"),
		envBody(t, "on", false), nethttp.StatusOK)
	expect(t, handler, developer, nethttp.MethodDelete, envRoute(granted, "/entries/"+testProposalPlainKey), "", nethttp.StatusNoContent)

	applyEnvAs(t, fixture, admin, granted)

	body := expect(t, handler, admin, nethttp.MethodGet, envRoute(granted, ""), "", nethttp.StatusOK)
	if view := decodeBodyInto[model.EnvView](t, body); view.PendingCount != 0 || len(view.Entries) != 2 {
		t.Errorf("env after the admin applied = %d pending, %d entries; want 0 and 2", view.PendingCount, len(view.Entries))
	}

	if applied := viewEntry(t, fixture, admin, granted, "FEATURE_FLAG"); applied.State != model.EnvStateApplied {
		t.Errorf("FEATURE_FLAG after the apply = %s, want applied", applied.State)
	}
}

// The equality oracle over HTTP. Every attempt a developer makes on a
// secret key gets the same status and the same bytes, writes the same
// audit row and changes nothing, whether the body holds the true value,
// a wrong one, a flag either way, or a value the validator would reject.
func TestSecretKeyRefusalDoesNotDependOnTheValueSent(t *testing.T) {
	t.Parallel()

	fixture, admin := newProposalFixture(t)
	handler := fixture.server.handler
	granted := testfixture.GenericGranted
	target := envRoute(granted, "/entries/"+testProposalSecretKey)

	callers := map[string]*nethttp.Cookie{
		testLabelScopedDev:      fixture.cookieFor(t, testDevUsername),
		testLabelFleetDev:        fixture.cookieFor(t, testFleetDevUsername),
	}

	attempts := []struct {
		name   string
		method string
		body   string
	}{
		{"the true value, flagged secret", nethttp.MethodPut, envBody(t, testProposalSecretValue, true)},
		{"the true value, not flagged", nethttp.MethodPut, envBody(t, testProposalSecretValue, false)},
		{"a wrong value, flagged secret", nethttp.MethodPut, envBody(t, testProposalWrongValue, true)},
		{"a wrong value, not flagged", nethttp.MethodPut, envBody(t, testProposalWrongValue, false)},
		{"an empty value", nethttp.MethodPut, envBody(t, "", true)},
		{"a value with a newline", nethttp.MethodPut, envBody(t, "two\nlines", true)},
		{"a removal", nethttp.MethodDelete, ""},
	}

	before := viewEntry(t, fixture, admin, granted, testProposalSecretKey)

	for label, cookie := range callers {
		username := testDevUsername
		if label == testLabelFleetDev {
			username = testFleetDevUsername
		}

		wantRow := username + " env.stage failure refused secret " + testProposalSecretKey

		var firstBody string

		for i, attempt := range attempts {
			trail := envAuditTrail(t, fixture.db, granted)

			status, _, body := call(t, handler, cookie, attempt.method, target, attempt.body)
			if status != nethttp.StatusForbidden {
				t.Errorf("%s, %s: status %d, want 403", label, attempt.name, status)
			}

			if i == 0 {
				firstBody = body
				expectCode(t, label, []byte(body), testSecretRestricted)
			}

			if body != firstBody {
				t.Errorf("%s, %s: body %s, want the same bytes as every other attempt (%s)", label, attempt.name, body, firstBody)
			}

			if strings.Contains(body, testProposalSecretValue) || strings.Contains(body, testProposalWrongValue) {
				t.Errorf("%s, %s: body %s carries a value", label, attempt.name, body)
			}

			after := envAuditTrail(t, fixture.db, granted)
			if len(after) != len(trail)+1 || after[len(after)-1] != wantRow {
				t.Errorf("%s, %s: audit rows written = %q, want exactly %q", label, attempt.name, after[len(trail):], wantRow)
			}
		}
	}

	if after := viewEntry(t, fixture, admin, granted, testProposalSecretKey); after != before {
		t.Errorf("secret entry after the refusals = %+v, want it untouched (%+v)", after, before)
	}

	diff := decodeBodyInto[model.EnvDiff](t, expect(t, handler, admin, nethttp.MethodGet, envRoute(granted, "/diff"), "", nethttp.StatusOK))
	if len(diff.Added)+len(diff.Changed)+len(diff.Removed) != 0 {
		t.Errorf("pending changes after the refusals = %+v, want none", diff)
	}
}

// A developer may not set the secret flag on anything, and is refused a
// key that a reviewer has pending as a secret.
func TestDeveloperCannotFlagOrTouchPendingSecrets(t *testing.T) {
	t.Parallel()

	fixture, admin := newProposalFixture(t)
	handler := fixture.server.handler
	developer := fixture.cookieFor(t, testDevUsername)
	granted := testfixture.GenericGranted

	expect(t, handler, admin, nethttp.MethodPut, envRoute(granted, "/entries/PENDING_TOKEN"),
		envBody(t, "pending-placeholder", true), nethttp.StatusOK)

	refused := []struct {
		name, method, key, body string
	}{
		{"new key flagged secret", nethttp.MethodPut, "NEW_TOKEN", envBody(t, "anything", true)},
		{"non-secret key re-flagged", nethttp.MethodPut, testProposalPlainKey, envBody(t, testProposalPlainValue, true)},
		{"pending secret changed", nethttp.MethodPut, "PENDING_TOKEN", envBody(t, "anything", false)},
		{"pending secret removed", nethttp.MethodDelete, "PENDING_TOKEN", ""},
	}

	for _, tt := range refused {
		body := expect(t, handler, developer, tt.method, envRoute(granted, "/entries/"+tt.key), tt.body, nethttp.StatusForbidden)
		expectCode(t, tt.name, []byte(body), testSecretRestricted)
	}

	if entry := viewEntry(t, fixture, admin, granted, testProposalPlainKey); entry.IsSecret || entry.State != model.EnvStateApplied {
		t.Errorf("%s after a refused re-flag = %+v, want it applied and not secret", testProposalPlainKey, entry)
	}
}

// One developer's pending change is not another's to replace or
// withdraw; the admin may replace it.
func TestPendingChangeBelongsToItsAuthor(t *testing.T) {
	t.Parallel()

	fixture, admin := newProposalFixture(t)
	handler := fixture.server.handler
	author := fixture.cookieFor(t, testDevUsername)
	another := fixture.cookieFor(t, testFleetDevUsername)
	target := envRoute(testfixture.GenericGranted, "/entries/"+testProposalPlainKey)

	expect(t, handler, author, nethttp.MethodPut, target, envBody(t, "debug", false), nethttp.StatusOK)

	body := expect(t, handler, another, nethttp.MethodPut, target, envBody(t, "warn", false), nethttp.StatusConflict)
	expectCode(t, "PUT over another's change", []byte(body), "env_pending_by_other")

	body = expect(t, handler, another, nethttp.MethodDelete, target, "", nethttp.StatusConflict)
	expectCode(t, "DELETE of another's change", []byte(body), "env_pending_by_other")

	// The author withdraws by staging the applied value again.
	withdrawn := decodeBodyInto[model.EnvEntryView](t, expect(t, handler, author, nethttp.MethodPut, target,
		envBody(t, testProposalPlainValue, false), nethttp.StatusOK))
	if withdrawn.State != model.EnvStateApplied {
		t.Errorf("entry after the author withdrew = %s, want applied", withdrawn.State)
	}

	expect(t, handler, author, nethttp.MethodPut, target, envBody(t, "debug", false), nethttp.StatusOK)

	replaced := decodeBodyInto[model.EnvEntryView](t, expect(t, handler, admin, nethttp.MethodPut, target,
		envBody(t, "error", false), nethttp.StatusOK))
	if replaced.UpdatedBy != defaultLoginUsername {
		t.Errorf("entry after the admin replaced it is by %q, want %s", replaced.UpdatedBy, defaultLoginUsername)
	}
}

// The digest of the rendered file reaches only a caller who may apply.
// Everyone else gets the same reads without the field.
func TestRenderedHashIsAbsentForViewersAndDevelopers(t *testing.T) {
	t.Parallel()

	fixture, admin := newProposalFixture(t)
	handler := fixture.server.handler
	granted := testfixture.GenericGranted

	expect(t, handler, fixture.cookieFor(t, testDevUsername), nethttp.MethodPut,
		envRoute(granted, "/entries/FEATURE_FLAG"), envBody(t, "on", false), nethttp.StatusOK)

	runID := applyEnvAs(t, fixture, admin, granted)

	expect(t, handler, admin, nethttp.MethodPut, envRoute(granted, "/entries/"+testProposalPlainKey),
		envBody(t, "debug", false), nethttp.StatusOK)

	reads := []string{"", "/diff", "/revisions", "/runs/" + runID}

	nonAppliers := map[string]*nethttp.Cookie{
		"scoped viewer":          fixture.cookieFor(t, testScopedUsername),
		testLabelFleetViewer:     viewerCookie(t, fixture.server),
		testLabelScopedDev:      fixture.cookieFor(t, testDevUsername),
		testLabelFleetDev:        fixture.cookieFor(t, testFleetDevUsername),
	}

	for label, cookie := range nonAppliers {
		for _, suffix := range reads {
			body := expect(t, handler, cookie, nethttp.MethodGet, envRoute(granted, suffix), "", nethttp.StatusOK)
			if strings.Contains(body, testHashField) || strings.Contains(body, "sha256") {
				t.Errorf("GET env%s as %s = %s, want no rendered hash", suffix, label, body)
			}

			if strings.Contains(body, testProposalSecretValue) {
				t.Errorf("GET env%s as %s carries the secret value", suffix, label)
			}
		}
	}

	for _, suffix := range []string{"/diff", "/revisions"} {
		body := expect(t, handler, admin, nethttp.MethodGet, envRoute(granted, suffix), "", nethttp.StatusOK)
		if !strings.Contains(body, `"`+testHashField+`":"`) {
			t.Errorf("GET env%s as admin = %s, want the rendered hash", suffix, body)
		}
	}
}

// An admin's apply may name the digest it reviewed; a proposal staged
// after the review then stops the apply instead of riding along.
func TestApplyOfAStaleReviewIsRefused(t *testing.T) {
	t.Parallel()

	fixture, admin := newProposalFixture(t)
	handler := fixture.server.handler
	developer := fixture.cookieFor(t, testDevUsername)
	granted := testfixture.GenericGranted

	expect(t, handler, developer, nethttp.MethodPut, envRoute(granted, "/entries/FEATURE_FLAG"),
		envBody(t, "on", false), nethttp.StatusOK)

	reviewed := decodeBodyInto[model.EnvDiff](t, expect(t, handler, admin, nethttp.MethodGet,
		envRoute(granted, "/diff"), "", nethttp.StatusOK))

	expect(t, handler, developer, nethttp.MethodPut, envRoute(granted, "/entries/LATE_KEY"),
		envBody(t, "slipped-in", false), nethttp.StatusOK)

	raw, err := json.Marshal(envApplyRequest{Confirm: "Billing worker", RenderedSHA256: reviewed.RenderedSHA256})
	if err != nil {
		t.Fatalf("encoding the apply body: %v", err)
	}

	body := expect(t, handler, admin, nethttp.MethodPost, envRoute(granted, "/apply"), string(raw), nethttp.StatusConflict)
	expectCode(t, "apply of a stale review", []byte(body), "env_pending_changed")

	if late := viewEntry(t, fixture, admin, granted, "LATE_KEY"); late.State != model.EnvStateAdded {
		t.Errorf("late key after the refused apply = %s, want still added", late.State)
	}
}
