package http

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

func TestHubBroadcast(t *testing.T) {
	t.Parallel()

	hub := NewHub(slog.New(slog.DiscardHandler))

	first, cancelFirst := hub.Subscribe(testAdminSubject())
	second, cancelSecond := hub.Subscribe(testAdminSubject())

	defer cancelFirst()
	defer cancelSecond()

	event := model.Event{Type: model.EventNginxChanged, ServiceID: testServiceAPI, Message: "flip", At: time.Now()}
	hub.Broadcast(event)

	if got := <-first; got != event {
		t.Errorf("first = %+v, want %+v", got, event)
	}

	if got := <-second; got != event {
		t.Errorf("second = %+v, want %+v", got, event)
	}

	cancelFirst()
	hub.Broadcast(event)

	if got := <-second; got != event {
		t.Errorf("second = %+v, want %+v", got, event)
	}

	if _, ok := <-first; ok {
		t.Error("cancelled subscriber received an event")
	}
}

func TestHandleEvents(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	ctx, cancel := context.WithCancel(t.Context())

	req := httptest.NewRequestWithContext(ctx, nethttp.MethodGet, "/api/events", nil)
	req.AddCookie(authCookie(t, server))

	rec := httptest.NewRecorder()

	done := make(chan struct{})

	go func() {
		defer close(done)

		server.handler.ServeHTTP(rec, req)
	}()

	time.Sleep(50 * time.Millisecond)
	server.events.Broadcast(model.Event{
		Type:      model.EventDeployStarted,
		ServiceID: testServiceAPI,
		Message:   "cutover started",
		At:        time.Now(),
	})
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("events handler did not stop after cancel")
	}

	body := rec.Body.String()

	if !strings.Contains(body, "data:") || !strings.Contains(body, "deploy_started") {
		t.Errorf("body = %q, want SSE event", body)
	}

	if content := rec.Header().Get("Content-Type"); content != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", content)
	}
}

// receive returns the next buffered event, or false when none waits.
func receive(events <-chan model.Event) (model.Event, bool) {
	select {
	case event, ok := <-events:
		return event, ok
	default:
		return model.Event{}, false
	}
}

func TestEveryEventTypeIsMapped(t *testing.T) {
	t.Parallel()

	// Every type the product emits. A new one must be added here and to
	// eventCapabilities, or it reaches nobody.
	types := []string{
		model.EventNginxChanged, model.EventDeployStarted, model.EventDeployProgress, model.EventDeployFinished,
		model.EventRollbackStarted, model.EventRollbackProgress, model.EventRollbackFinished,
		model.EventSecurityFinding, model.EventSecurityRecovery, model.EventEnvApply,
	}

	for _, eventType := range types {
		if _, ok := eventCapabilities[eventType]; !ok {
			t.Errorf("event type %q has no capability mapping", eventType)
		}
	}

	if len(eventCapabilities) != len(types) {
		t.Errorf("eventCapabilities has %d types, want the %d listed here", len(eventCapabilities), len(types))
	}
}

func TestHubDeliversOnlyWhatTheSubscriberMaySee(t *testing.T) {
	t.Parallel()

	granted, other := testfixture.GenericGranted, testfixture.GenericOther
	now := time.Now()

	const (
		deployInScope  = "deploy in scope"
		envInScope     = "env apply in scope"
		targetInScope  = "live target in scope"
		deployForeign  = "deploy out of scope"
		envForeign     = "env apply elsewhere"
		securityHost   = "security on a host"
		securityOwned  = "security on a service"
		deployNoOwner  = "deploy with no owner"
		unmappedType   = "unmapped type"
		scopedUsername = "scoped"
	)

	events := map[string]model.Event{
		deployInScope: {Type: model.EventDeployProgress, ServiceID: granted, Message: "step 1", At: now},
		deployForeign: {Type: model.EventDeployProgress, ServiceID: other, Message: "secret output", At: now},
		envInScope:    {Type: model.EventEnvApply, ServiceID: granted, RunID: "r1", Step: "probe", At: now},
		envForeign:    {Type: model.EventEnvApply, ServiceID: other, RunID: "r2", Step: "restart", At: now},
		targetInScope: {Type: model.EventNginxChanged, ServiceID: granted, Message: "flip", At: now},
		securityHost:  {Type: model.EventSecurityFinding, Message: "new finding", At: now},
		securityOwned: {Type: model.EventSecurityFinding, ServiceID: granted, Message: "new finding", At: now},
		deployNoOwner: {Type: model.EventDeployStarted, Message: "fleet-wide", At: now},
		unmappedType:  {Type: "brand_new_event", ServiceID: granted, Message: "?", At: now},
	}

	everything := []string{
		deployInScope, deployForeign, envInScope, envForeign, targetInScope, securityHost, securityOwned, deployNoOwner,
	}

	subscribers := []struct {
		name    string
		subject authz.Subject
		wants   []string
	}{
		{name: "an admin", subject: testAdminSubject(), wants: everything},
		{
			name:    "all-services viewer",
			subject: authz.Subject{Username: testViewerUsername, Role: model.RoleViewer, AllServices: true},
			wants:   everything,
		},
		{
			name:    "scoped viewer",
			subject: authz.Subject{Username: scopedUsername, Role: model.RoleViewer, Services: []string{granted}},
			wants:   []string{deployInScope, envInScope, targetInScope},
		},
		{
			name:    "viewer with no services",
			subject: authz.Subject{Username: testEmptyUsername, Role: model.RoleViewer, Services: []string{}},
			wants:   []string{},
		},
		{
			name:    "reserved role",
			subject: authz.Subject{Username: testReservedUsername, Role: model.RoleDeveloper, AllServices: true},
			wants:   []string{},
		},
	}

	for _, sub := range subscribers {
		t.Run(sub.name, func(t *testing.T) {
			t.Parallel()

			for label, event := range events {
				hub := NewHub(slog.New(slog.DiscardHandler))

				stream, cancel := hub.Subscribe(sub.subject)
				hub.Broadcast(event)

				got, delivered := receive(stream)

				cancel()

				want := slices.Contains(sub.wants, label)
				if delivered != want {
					t.Errorf("%s: delivered = %t, want %t", label, delivered, want)
				}

				if delivered && got != event {
					t.Errorf("%s: got %+v, want %+v", label, got, event)
				}
			}
		})
	}
}

func TestHubDisconnect(t *testing.T) {
	t.Parallel()

	hub := NewHub(slog.New(slog.DiscardHandler))
	scoped := authz.Subject{Username: testScopedUsername, Role: model.RoleViewer, Services: []string{testfixture.GenericGranted}}

	first, cancelFirst := hub.Subscribe(scoped)
	second, cancelSecond := hub.Subscribe(scoped)
	admin, cancelAdmin := hub.Subscribe(testAdminSubject())

	defer cancelFirst()
	defer cancelSecond()
	defer cancelAdmin()

	if closed := hub.Disconnect(testScopedUsername); closed != 2 {
		t.Errorf("Disconnect() = %d, want both of the user's streams", closed)
	}

	for label, stream := range map[string]<-chan model.Event{"first": first, "second": second} {
		if _, open := <-stream; open {
			t.Errorf("%s stream is still open after Disconnect", label)
		}
	}

	event := model.Event{Type: model.EventDeployStarted, ServiceID: testfixture.GenericGranted, At: time.Now()}
	hub.Broadcast(event)

	if got, delivered := receive(admin); !delivered || got != event {
		t.Errorf("another user's stream got %+v delivered=%t, want it untouched by the disconnect", got, delivered)
	}

	if closed := hub.Disconnect(testScopedUsername); closed != 0 {
		t.Errorf("second Disconnect() = %d, want 0", closed)
	}

	// Cancelling a stream the hub already closed must not panic.
	cancelFirst()
}

// openStream starts GET /api/events for cookie and returns the recorder
// and a channel closed when the handler returns.
func openStream(t *testing.T, server *Server, cookie *nethttp.Cookie) (*httptest.ResponseRecorder, <-chan struct{}) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/events", nil)
	req.AddCookie(cookie)

	rec := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		defer close(done)

		server.handler.ServeHTTP(rec, req)
	}()

	return rec, done
}

// waitForSubscribers blocks until the hub holds want streams.
func waitForSubscribers(t *testing.T, hub *Hub, want int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		hub.mutex.Lock()
		count := len(hub.subs)
		hub.mutex.Unlock()

		if count == want {
			return
		}

		time.Sleep(time.Millisecond)
	}

	t.Fatalf("hub never reached %d subscribers", want)
}

// A scoped viewer's stream carries only in-scope events, and it is
// closed the moment an admin changes that user's access.
func TestScopedStreamIsFilteredAndClosedOnAccessChange(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	server := fixture.server
	granted, other := testfixture.GenericGranted, testfixture.GenericOther

	rec, done := openStream(t, server, fixture.cookieFor(t, testScopedUsername))
	waitForSubscribers(t, server.events, 1)

	for _, event := range []model.Event{
		{Type: model.EventDeployProgress, ServiceID: other, Message: "FOREIGN-OUTPUT", At: time.Now()},
		{Type: model.EventDeployProgress, ServiceID: granted, Message: "OWN-OUTPUT", At: time.Now()},
		{Type: model.EventEnvApply, ServiceID: other, RunID: "FOREIGN-RUN", Step: "validate", At: time.Now()},
		{Type: model.EventSecurityFinding, Message: "HOST-FINDING", At: time.Now()},
	} {
		server.events.Broadcast(event)
	}

	scoped, err := server.users.Lookup(t.Context(), testScopedUsername)
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}

	// Give the stream time to write what it was sent before it closes.
	time.Sleep(50 * time.Millisecond)

	status, body := doRequest(t, server, nethttp.MethodPut,
		"/api/users/"+strconv.FormatInt(scoped.ID, 10)+"/access",
		`{"role":"viewer","all_services":false,"services":["`+other+`"]}`)
	if status != nethttp.StatusOK {
		t.Fatalf("set access = %d %s, want 200", status, body)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream stayed open after the user's access changed")
	}

	stream := rec.Body.String()

	if !strings.Contains(stream, "OWN-OUTPUT") {
		t.Errorf("stream = %q, want the in-scope event", stream)
	}

	for _, leaked := range []string{"FOREIGN-OUTPUT", "FOREIGN-RUN", "HOST-FINDING", other} {
		if strings.Contains(stream, leaked) {
			t.Errorf("stream leaked %q: %q", leaked, stream)
		}
	}
}

// Every change that alters who a user is closes their streams.
func TestStreamsCloseWhenTheAccountChanges(t *testing.T) {
	t.Parallel()

	changes := []struct {
		name   string
		method string
		suffix string
		body   string
	}{
		{name: "role", method: nethttp.MethodPost, suffix: "/role", body: testViewerRoleBody},
		{name: "disable", method: nethttp.MethodPost, suffix: "/disable", body: ""},
		{name: "password reset", method: nethttp.MethodPost, suffix: "/password", body: `{"password":"reset-by-admin-12"}`},
		{name: "delete", method: nethttp.MethodDelete, suffix: "", body: ""},
	}

	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			t.Parallel()

			fixture := newScopedFixture(t)
			server := fixture.server

			_, done := openStream(t, server, fixture.cookieFor(t, testScopedUsername))
			waitForSubscribers(t, server.events, 1)

			scoped, err := server.users.Lookup(t.Context(), testScopedUsername)
			if err != nil {
				t.Fatalf("Lookup() error = %v, want nil", err)
			}

			status, body := doRequest(t, server, change.method,
				"/api/users/"+strconv.FormatInt(scoped.ID, 10)+change.suffix, change.body)
			if status >= nethttp.StatusBadRequest {
				t.Fatalf("%s = %d %s, want success", change.name, status, body)
			}

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("the stream stayed open after %s", change.name)
			}
		})
	}
}

// Changing one's own password closes the streams of the sessions it
// ends; they would otherwise outlive the password that opened them.
func TestOwnPasswordChangeClosesStreams(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	server := fixture.server
	cookie := fixture.cookieFor(t, testScopedUsername)

	_, done := openStream(t, server, fixture.cookieFor(t, testScopedUsername))
	waitForSubscribers(t, server.events, 1)

	status, body := requestAs(t, server, cookie, nethttp.MethodPost, "/api/auth/password",
		changePasswordBody(testViewerPassword, "brand-new-horse-9"))
	if status != nethttp.StatusNoContent {
		t.Fatalf("change password = %d %s, want 204", status, body)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the other session's stream stayed open after the password changed")
	}
}
