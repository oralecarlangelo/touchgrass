package http

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// Paths and bodies the developer suites share.
const (
	testAlertRulesPath = "/api/alerts/rules"
	testMarkAllPath    = "/api/notifications/read"
	testForgedActor    = "admin"
)

// servicePath builds /api/services/{id}/{leaf}.
func servicePath(serviceID, leaf string) string {
	return "/api/services/" + serviceID + "/" + leaf
}

// auditOf returns the audit rows of one service oldest first, as
// "actor action detail".
func auditOf(t *testing.T, db *store.DB, serviceID string) []model.Audit {
	t.Helper()

	rows, err := store.NewAuditStore(db).List(context.Background(), serviceID, 100)
	if err != nil {
		t.Fatalf("List(audit) error = %v, want nil", err)
	}

	slices.Reverse(rows)

	return rows
}

// lastAudit returns the newest audit row of one service.
func lastAudit(t *testing.T, db *store.DB, serviceID string) model.Audit {
	t.Helper()

	rows := auditOf(t, db, serviceID)
	if len(rows) == 0 {
		t.Fatalf("service %s has no audit rows, want one", serviceID)
	}

	return rows[len(rows)-1]
}

// configureDeployScript gives a generic recreate service a deploy and a
// rollback script, as an admin would when registering it.
func configureDeployScript(t *testing.T, db *store.DB, serviceID string) {
	t.Helper()

	script := filepath.Join(t.TempDir(), "deploy.sh")

	//nolint:gosec // G306: a deploy script has to be executable.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("writing the deploy script: %v", err)
	}

	services := store.NewServiceStore(db)

	def, err := services.Get(context.Background(), serviceID)
	if err != nil {
		t.Fatalf("Get(%s) error = %v, want nil", serviceID, err)
	}

	var cfg model.RecreateConfig

	if err := json.Unmarshal(def.Config, &cfg); err != nil {
		t.Fatalf("decoding the service config: %v", err)
	}

	cfg.DeployScript, cfg.RollbackScript = script, script

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encoding the service config: %v", err)
	}

	if err := services.UpdateConfig(context.Background(), serviceID, raw); err != nil {
		t.Fatalf("UpdateConfig(%s) error = %v, want nil", serviceID, err)
	}
}

// waitForAudit polls until the service has an audit row with action
// that the fixture did not seed, and returns it.
func waitForAudit(t *testing.T, db *store.DB, serviceID, action string) model.Audit {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		for _, row := range auditOf(t, db, serviceID) {
			if row.Action == action && row.Detail != "deployed "+serviceID {
				return row
			}
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("no %s audit row for %s within 5s", action, serviceID)

	return model.Audit{}
}

// expect performs one request and fails unless it answers want. It
// returns the response body.
func expect(
	t *testing.T,
	handler nethttp.Handler,
	cookie *nethttp.Cookie,
	method, target, body string,
	want int,
) string {
	t.Helper()

	status, _, response := call(t, handler, cookie, method, target, body)
	if status != want {
		t.Errorf("%s %s = %d %s, want %d", method, target, status, response, want)
	}

	return response
}

// expectLastAudit fails unless the service's newest audit row is the
// given actor and action, and, when detail is not empty, that detail.
func expectLastAudit(t *testing.T, db *store.DB, serviceID, actor, action, detail string) {
	t.Helper()

	row := lastAudit(t, db, serviceID)
	if row.Actor != actor || row.Action != action || (detail != "" && row.Detail != detail) {
		t.Errorf("newest audit row of %s = %s %s %q, want %s %s %q",
			serviceID, row.Actor, row.Action, row.Detail, actor, action, detail)
	}
}

// expectOnlySeededAudit fails if the service gained an audit row beyond
// the one the fixture seeded.
func expectOnlySeededAudit(t *testing.T, db *store.DB, serviceID string) {
	t.Helper()

	if rows := auditOf(t, db, serviceID); len(rows) != 1 {
		t.Errorf("audit rows of %s = %+v, want only its seeded row", serviceID, rows)
	}
}

// expectSameNotFound fails unless both targets answer the same 404
// bytes: a foreign id and a missing id must be indistinguishable.
func expectSameNotFound(
	t *testing.T,
	handler nethttp.Handler,
	cookie *nethttp.Cookie,
	method, foreign, missing string,
) {
	t.Helper()

	foreignBody := expect(t, handler, cookie, method, foreign, "", nethttp.StatusNotFound)
	missingBody := expect(t, handler, cookie, method, missing, "", nethttp.StatusNotFound)

	if foreignBody != missingBody {
		t.Errorf("a foreign id answered %s, a missing id %s; want the same bytes", foreignBody, missingBody)
	}
}

// waitUntilIdle waits for a service's deploy run to finish.
func waitUntilIdle(t *testing.T, fixture scopedFixture, serviceID string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for fixture.server.cutover.Active(serviceID) {
		if time.Now().After(deadline) {
			t.Fatalf("deploy of %s still running after 5s", serviceID)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

// deployFixture is the scoped fixture with a deploy script configured
// on both generic services and a runner that reports what it ran.
func deployFixture(t *testing.T) (scopedFixture, <-chan []string) {
	t.Helper()

	ran := make(chan []string, 8)
	fixture := newScopedFixtureWith(t, testServerOptions{
		deployRunner: func(_ context.Context, _, script string, args []string, _ func(string)) error {
			ran <- append([]string{script}, args...)

			return nil
		},
	})

	configureDeployScript(t, fixture.db, testfixture.GenericGranted)
	configureDeployScript(t, fixture.db, testfixture.GenericOther)

	return fixture, ran
}

// A developer's deploy and rollback of a service in scope run what the
// admin configured and are audited under the developer's own name.
func TestDeveloperDeploysItsServiceUnderItsOwnName(t *testing.T) {
	t.Parallel()

	fixture, ran := deployFixture(t)
	granted := testfixture.GenericGranted
	handler := fixture.server.handler
	developer := fixture.cookieFor(t, testDevUsername)

	expect(t, handler, developer, nethttp.MethodPost, servicePath(granted, "deploy"), "", nethttp.StatusAccepted)

	select {
	case invocation := <-ran:
		// The script and its arguments come from the service row; the
		// request carried no body at all.
		want := []string{"--service", "worker", "--project", "billing", "--compose-dir", "/srv/billing"}
		if !slices.Equal(invocation[1:], want) {
			t.Errorf("deploy ran with %v, want the configured %v", invocation[1:], want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the deploy script never ran")
	}

	if row := waitForAudit(t, fixture.db, granted, model.AuditDeploy); row.Actor != testDevUsername {
		t.Errorf("deploy audit actor = %q, want %q", row.Actor, testDevUsername)
	}

	history := decodeBodyInto[deploysResponse](t,
		expect(t, handler, developer, nethttp.MethodGet, servicePath(granted, "deploys"), "", nethttp.StatusOK))
	if len(history.Deploys) != 1 || history.Deploys[0].Actor != testDevUsername || history.Deploys[0].Type != model.DeployDeploy {
		t.Errorf("deploy history = %+v, want one deploy by %s", history.Deploys, testDevUsername)
	}

	// The engine refuses a second run while one is in flight, so wait
	// for the first to finish before rolling back.
	waitUntilIdle(t, fixture, granted)
	expect(t, handler, developer, nethttp.MethodPost, servicePath(granted, "rollback"), "", nethttp.StatusAccepted)

	if row := waitForAudit(t, fixture.db, granted, model.AuditRollback); row.Actor != testDevUsername {
		t.Errorf("rollback audit actor = %q, want %q", row.Actor, testDevUsername)
	}

	waitUntilIdle(t, fixture, granted)

	// A cutover of a recreate service is the engine's to refuse, which
	// shows the developer got past the guard and no further.
	expect(t, handler, developer, nethttp.MethodPost, servicePath(granted, "cutover"), `{"target":"green"}`,
		nethttp.StatusBadRequest)
}

// Out of scope, and for a viewer in scope, every deploy route is
// refused before anything runs or is recorded.
func TestDeployRoutesAreRefusedOutsideTheRoleAndScope(t *testing.T) {
	t.Parallel()

	fixture, ran := deployFixture(t)
	granted, other := testfixture.GenericGranted, testfixture.GenericOther
	handler := fixture.server.handler

	refused := []struct {
		name     string
		username string
		service  string
	}{
		{name: "scoped developer, other service", username: testDevUsername, service: other},
		{name: "scoped developer, unknown service", username: testDevUsername, service: "no-such-service"},
		{name: "developer with no services", username: testEmptyDevUsername, service: granted},
		{name: "scoped viewer, own service", username: testScopedUsername, service: granted},
		{name: "all-services viewer", username: testViewerUsername, service: granted},
	}

	for _, tt := range refused {
		cookie := fixture.cookieFor(t, tt.username)

		for _, leaf := range []string{"deploy", "rollback", "cutover", "deploys"} {
			body := expect(t, handler, cookie, nethttp.MethodPost, servicePath(tt.service, leaf),
				`{"sha":"abc1234","outcome":"success"}`, nethttp.StatusForbidden)
			if errorCode(t, []byte(body)) != codeForbidden {
				t.Errorf("%s: POST %s body = %s, want code forbidden", tt.name, leaf, body)
			}
		}
	}

	select {
	case invocation := <-ran:
		t.Errorf("a refused request ran %v", invocation)
	case <-time.After(50 * time.Millisecond):
	}

	expectOnlySeededAudit(t, fixture.db, granted)
	expectOnlySeededAudit(t, fixture.db, other)
}

// A manual deploy record made by a non-admin always carries the session
// user: a body actor is ignored. An admin may still name someone else.
func TestManualDeployRecordActorIsForcedForNonAdmins(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := fixture.server.handler
	granted, other := testfixture.GenericGranted, testfixture.GenericOther
	forged := `{"sha":"abc1234","actor":"` + testForgedActor + `","outcome":"success"}`
	recordPath := servicePath(granted, "deploys")

	tests := []struct {
		name       string
		username   string
		cookie     *nethttp.Cookie
		body       string
		wantActor  string
		wantDetail string
	}{
		{
			name: "scoped developer naming the admin", username: testDevUsername,
			cookie: fixture.cookieFor(t, testDevUsername), body: forged, wantActor: testDevUsername,
			wantDetail: "recorded manual success of abc1234 (deploy 1)",
		},
		{
			name: "scoped developer naming nobody", username: testDevUsername,
			cookie: fixture.cookieFor(t, testDevUsername), body: `{"sha":"abc1234","outcome":"success"}`,
			wantActor: testDevUsername, wantDetail: "recorded manual success of abc1234 (deploy 2)",
		},
		{
			name: "all-services developer naming the admin", username: testFleetDevUsername,
			cookie: fixture.cookieFor(t, testFleetDevUsername), body: forged, wantActor: testFleetDevUsername,
			wantDetail: "recorded manual success of abc1234 (deploy 3)",
		},
		{
			name: "admin naming a release bot", username: defaultLoginUsername,
			cookie: authCookie(t, fixture.server), body: `{"sha":"abc1234","actor":"release-bot","outcome":"success"}`,
			wantActor: "release-bot", wantDetail: "recorded manual success of abc1234 (deploy 4), attributed to release-bot",
		},
	}

	// Sequential on purpose: each case reads the row the one before left.
	for _, tt := range tests {
		expect(t, handler, tt.cookie, nethttp.MethodPost, recordPath, tt.body, nethttp.StatusCreated)

		history := decodeBodyInto[deploysResponse](t,
			expect(t, handler, tt.cookie, nethttp.MethodGet, recordPath, "", nethttp.StatusOK))
		if len(history.Deploys) == 0 || history.Deploys[0].Actor != tt.wantActor {
			t.Errorf("%s: newest deploy = %+v, want actor %q", tt.name, history.Deploys, tt.wantActor)
		}

		expectLastAudit(t, fixture.db, granted, tt.username, model.AuditDeployRecord, tt.wantDetail)
	}

	// An admin who names nobody is still refused, as in v0.6.0.
	admin := authCookie(t, fixture.server)
	expect(t, handler, admin, nethttp.MethodPost, recordPath, `{"sha":"abc1234","outcome":"success"}`, nethttp.StatusBadRequest)

	expect(t, handler, fixture.cookieFor(t, testDevUsername), nethttp.MethodPost, servicePath(other, "deploys"), forged,
		nethttp.StatusForbidden)

	history := decodeBodyInto[deploysResponse](t,
		expect(t, handler, admin, nethttp.MethodGet, servicePath(other, "deploys"), "", nethttp.StatusOK))
	if len(history.Deploys) != 0 {
		t.Errorf("the other service's history = %+v, want it untouched", history.Deploys)
	}
}

// ruleCase drives one kind of rule through the same checks.
type ruleCase struct {
	name         string
	path         string
	body         func(serviceID string) string
	resource     model.ResourceKind
	createAction string
	deleteAction string
}

// ruleCases are the two kinds of rule a developer may manage.
func ruleCases() []ruleCase {
	return []ruleCase{
		{
			name: "alert rule", path: testAlertRulesPath, resource: model.ResourceAlertRule,
			body: func(serviceID string) string {
				return `{"service_id":"` + serviceID + `","metric":"cpu","threshold":90,"duration_secs":120}`
			},
			createAction: model.AuditAlertRuleCreate, deleteAction: model.AuditAlertRuleDelete,
		},
		{
			name: "issue rule", path: testIssueRulesPath, resource: model.ResourceIssueRule,
			body: func(serviceID string) string {
				return `{"service_id":"` + serviceID + `","kind":"spike","threshold":5,"window_secs":300}`
			},
			createAction: model.AuditIssueRuleCreate, deleteAction: model.AuditIssueRuleDelete,
		},
	}
}

func TestDeveloperCreatesAndDeletesRulesInScope(t *testing.T) {
	t.Parallel()

	for _, tt := range ruleCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fixture := newScopedFixture(t)
			handler := fixture.server.handler
			developer := fixture.cookieFor(t, testDevUsername)
			granted := testfixture.GenericGranted

			created := decodeBodyInto[struct {
				ID int64 `json:"id"`
			}](t, expect(t, handler, developer, nethttp.MethodPost, tt.path, tt.body(granted), nethttp.StatusCreated))

			expectLastAudit(t, fixture.db, granted, testDevUsername, tt.createAction, "")

			own := tt.path + "/" + strconv.FormatInt(created.ID, 10)
			expect(t, handler, developer, nethttp.MethodDelete, own, "", nethttp.StatusNoContent)
			expectLastAudit(t, fixture.db, granted, testDevUsername, tt.deleteAction, "")

			// Deleted means gone: the same id is now a 404 for everyone.
			expect(t, handler, developer, nethttp.MethodDelete, own, "", nethttp.StatusNotFound)
			expect(t, handler, authCookie(t, fixture.server), nethttp.MethodDelete, own, "", nethttp.StatusNotFound)
		})
	}
}

func TestRulesOutOfScopeAreRefused(t *testing.T) {
	t.Parallel()

	for _, tt := range ruleCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fixture := newScopedFixture(t)
			handler := fixture.server.handler
			developer := fixture.cookieFor(t, testDevUsername)
			viewer := fixture.cookieFor(t, testScopedUsername)
			granted, other := testfixture.GenericGranted, testfixture.GenericOther

			// The other service: a body that names it, a body that names
			// nothing, and its rule's id.
			expect(t, handler, developer, nethttp.MethodPost, tt.path, tt.body(other), nethttp.StatusForbidden)
			expect(t, handler, developer, nethttp.MethodPost, tt.path, `{"metric":"cpu"}`, nethttp.StatusForbidden)

			foreign := tt.path + "/" + strconv.FormatInt(fixture.other[tt.resource], 10)
			missing := tt.path + "/" + strconv.FormatInt(testMissingID, 10)

			expectSameNotFound(t, handler, developer, nethttp.MethodDelete, foreign, missing)
			expectOnlySeededAudit(t, fixture.db, other)

			// A scoped viewer can do none of it, in scope or not.
			seeded := tt.path + "/" + strconv.FormatInt(fixture.granted[tt.resource], 10)
			expect(t, handler, viewer, nethttp.MethodPost, tt.path, tt.body(granted), nethttp.StatusForbidden)
			expect(t, handler, viewer, nethttp.MethodDelete, seeded, "", nethttp.StatusForbidden)
			expectOnlySeededAudit(t, fixture.db, granted)

			// The foreign rule survived all of that: an admin can still
			// delete it, and that is audited under the admin.
			expect(t, handler, authCookie(t, fixture.server), nethttp.MethodDelete, foreign, "", nethttp.StatusNoContent)
			expectLastAudit(t, fixture.db, other, defaultLoginUsername, tt.deleteAction, "")
		})
	}
}

// notificationReadPath builds /api/notifications/{id}/read.
func notificationReadPath(id int64) string {
	return "/api/notifications/" + strconv.FormatInt(id, 10) + "/read"
}

// expectUnread fails unless serviceID ("" for every row) has want
// unread notifications.
func expectUnread(t *testing.T, db *store.DB, serviceID string, want int64) {
	t.Helper()

	unread, err := store.NewNotificationStore(db).CountUnread(context.Background(), serviceID)
	if err != nil || unread != want {
		t.Errorf("unread notifications of %q = %d, %v, want %d", serviceID, unread, err, want)
	}
}

func TestDeveloperMarksNotificationsReadInScopeOnly(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := fixture.server.handler
	developer := fixture.cookieFor(t, testDevUsername)
	granted, other := testfixture.GenericGranted, testfixture.GenericOther

	seedGlobalNotification(t, fixture.db)

	second, err := store.NewNotificationStore(fixture.db).Insert(context.Background(), granted, "alert", "second alert", "body")
	if err != nil {
		t.Fatalf("Insert(notification) error = %v, want nil", err)
	}

	// One notification in scope.
	expect(t, handler, developer, nethttp.MethodPost, notificationReadPath(second.ID), "", nethttp.StatusNoContent)
	expectLastAudit(t, fixture.db, granted, testDevUsername, model.AuditNotificationRead,
		"marked notification "+strconv.FormatInt(second.ID, 10)+" read")

	// A foreign id and a missing id answer the same 404, and the other
	// service named outright is a 403.
	expectSameNotFound(t, handler, developer, nethttp.MethodPost,
		notificationReadPath(fixture.other[model.ResourceNotification]), notificationReadPath(testMissingID))
	expect(t, handler, developer, nethttp.MethodPost, testMarkAllPath+"?service_id="+other, "", nethttp.StatusForbidden)

	// Mark-all with no service touches the granted service only: its
	// one remaining row. The other service's row and the row that
	// belongs to no service stay unread.
	marked := decodeBodyInto[markedResponse](t,
		expect(t, handler, developer, nethttp.MethodPost, testMarkAllPath, "", nethttp.StatusOK))
	if marked.Marked != 1 {
		t.Errorf("mark all marked %d, want 1", marked.Marked)
	}

	expectLastAudit(t, fixture.db, granted, testDevUsername, model.AuditNotificationRead, "marked 1 notifications read")
	expectUnread(t, fixture.db, granted, 0)
	expectUnread(t, fixture.db, other, 1)
	expectUnread(t, fixture.db, "", 2)
	expectOnlySeededAudit(t, fixture.db, other)
}

// What v0.6.0 did for a viewer and an admin on the mark-read routes
// still holds: a viewer is refused, an admin's mark-all covers every
// row, and an unknown id is a no-op 204 for an admin.
func TestMarkReadIsUnchangedForViewerAndAdmin(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := fixture.server.handler
	granted := testfixture.GenericGranted

	seedGlobalNotification(t, fixture.db)

	viewer := fixture.cookieFor(t, testScopedUsername)

	for _, target := range []string{
		testMarkAllPath, testMarkAllPath + "?service_id=" + granted,
		notificationReadPath(fixture.granted[model.ResourceNotification]),
	} {
		expect(t, handler, viewer, nethttp.MethodPost, target, "", nethttp.StatusForbidden)
	}

	expectUnread(t, fixture.db, "", 3)

	admin := authCookie(t, fixture.server)

	marked := decodeBodyInto[markedResponse](t,
		expect(t, handler, admin, nethttp.MethodPost, testMarkAllPath, "", nethttp.StatusOK))
	if marked.Marked != 3 {
		t.Errorf("admin mark all marked %d, want all 3", marked.Marked)
	}

	expect(t, handler, admin, nethttp.MethodPost, notificationReadPath(testMissingID), "", nethttp.StatusNoContent)
	expectLastAudit(t, fixture.db, granted, defaultLoginUsername, model.AuditNotificationRead, "marked 1 notifications read")
}

// An account gains the developer's powers only when an admin changes
// its role, and loses them on the next request after it is changed back.
func TestDeveloperPowersFollowTheRole(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := fixture.server.handler
	admin := authCookie(t, fixture.server)
	cookie := fixture.cookieFor(t, testScopedUsername)
	granted := testfixture.GenericGranted
	record := `{"sha":"abc1234","outcome":"success"}`

	scoped, err := store.NewUserStore(fixture.db).ByUsername(context.Background(), testScopedUsername)
	if err != nil {
		t.Fatalf("ByUsername() error = %v, want nil", err)
	}

	accessPath := usersRoute + "/" + strconv.FormatInt(scoped.ID, 10) + "/access"

	steps := []struct {
		name   string
		access string
		want   int
	}{
		{name: "as the viewer it was created as", want: nethttp.StatusForbidden},
		{
			name: "after an admin makes it a developer", want: nethttp.StatusCreated,
			access: `{"role":"developer","all_services":false,"services":["` + granted + `"]}`,
		},
		{
			name: "after the grant moves to another service", want: nethttp.StatusForbidden,
			access: `{"role":"developer","all_services":false,"services":["` + testfixture.GenericOther + `"]}`,
		},
		{
			name: "after it is a viewer again", want: nethttp.StatusForbidden,
			access: `{"role":"viewer","all_services":false,"services":["` + granted + `"]}`,
		},
	}

	for _, step := range steps {
		if step.access != "" {
			expect(t, handler, admin, nethttp.MethodPut, accessPath, step.access, nethttp.StatusOK)
		}

		// The same session cookie throughout: the role is read per request.
		t.Log(step.name)
		expect(t, handler, cookie, nethttp.MethodPost, servicePath(granted, "deploys"), record, step.want)
	}

	// A developer cannot give itself, or anyone, anything.
	expect(t, handler, fixture.cookieFor(t, testFleetDevUsername), nethttp.MethodPut, accessPath,
		`{"role":"admin","all_services":true}`, nethttp.StatusForbidden)
}

func TestAdminCreatesADeveloper(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := fixture.server.handler
	admin := authCookie(t, fixture.server)
	granted := testfixture.GenericGranted

	created := decodeBodyInto[userResponse](t, expect(t, handler, admin, nethttp.MethodPost, usersRoute,
		`{"username":"new-dev","password":"correct-horse-1","role":"developer","all_services":false,"services":["`+granted+`"]}`,
		nethttp.StatusCreated))
	if created.Role != string(model.RoleDeveloper) || created.AllServices || len(created.Services) != 1 || !created.MustChangePassword {
		t.Errorf("created = %+v, want a scoped developer on a temporary password", created)
	}

	// The older role-only route accepts the role too.
	expect(t, handler, admin, nethttp.MethodPost, usersRoute+"/"+strconv.FormatInt(created.ID, 10)+"/role",
		`{"role":"developer"}`, nethttp.StatusOK)
}
