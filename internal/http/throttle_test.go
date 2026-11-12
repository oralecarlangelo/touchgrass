package http

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	nethttp "net/http"
)

// testUnknownUsername is a login no harness account has.
const testUnknownUsername = "nobody-here"

// testClock is a clock a test moves by hand.
type testClock struct {
	mutex sync.Mutex
	at    time.Time
}

// newTestClock starts a clock at a fixed instant.
func newTestClock() *testClock {
	return &testClock{at: time.Date(2026, time.November, 13, 9, 0, 0, 0, time.UTC)}
}

func (c *testClock) now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return c.at
}

func (c *testClock) advance(by time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.at = c.at.Add(by)
}

// loginAttempt is one login request and what it answered.
type loginAttempt struct {
	status  int
	body    string
	cookies []*nethttp.Cookie
}

// device returns the device cookie a successful attempt was handed.
func (a loginAttempt) device(t *testing.T) *nethttp.Cookie {
	t.Helper()

	for _, cookie := range a.cookies {
		if cookie.Name == deviceCookie {
			return cookie
		}
	}

	t.Fatalf("login answered %d without a device cookie", a.status)

	return nil
}

// throttleHarness is a server on a hand-moved clock whose every login
// comes from a new client address, so the per-address limit never
// answers and the per-username limit is what a test observes.
type throttleHarness struct {
	server *Server
	clock  *testClock
	next   int
}

func newThrottleHarness(t *testing.T) *throttleHarness {
	t.Helper()

	server, _ := fullTestServer(t)
	clock := newTestClock()
	server.auth.now = clock.now

	return &throttleHarness{server: server, clock: clock}
}

// login posts one login from a fresh address.
func (h *throttleHarness) login(t *testing.T, username, password string, cookies ...*nethttp.Cookie) loginAttempt {
	t.Helper()

	h.next++

	return h.loginFrom(t, fmt.Sprintf("198.51.100.%d:40000", h.next%250+1), username, password, cookies...)
}

// loginFrom posts one login from addr.
func (h *throttleHarness) loginFrom(
	t *testing.T,
	addr, username, password string,
	cookies ...*nethttp.Cookie,
) loginAttempt {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodPost, "/api/auth/login",
		strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
	req.RemoteAddr = addr

	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	h.server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	return loginAttempt{status: res.StatusCode, body: rec.Body.String(), cookies: res.Cookies()}
}

// exhaust spends the username's shared lane with wrong passwords.
func (h *throttleHarness) exhaust(t *testing.T, username string) {
	t.Helper()

	for i := range maxUsernameFailures {
		if got := h.login(t, username, "wrong-password"); got.status != nethttp.StatusUnauthorized {
			t.Fatalf("failure %d for %s = %d, want 401", i+1, username, got.status)
		}
	}
}

func TestAttemptLimiterWindow(t *testing.T) {
	t.Parallel()

	start := newTestClock().now()
	limiter := newAttemptLimiter(3, time.Minute, 10)

	for i := range 3 {
		if limiter.exceeded("k", start) {
			t.Fatalf("exceeded after %d failures, want only after 3", i)
		}

		limiter.fail("k", start.Add(time.Duration(i)*time.Second))
	}

	if !limiter.exceeded("k", start.Add(3*time.Second)) {
		t.Fatal("not exceeded after 3 failures")
	}

	if limiter.exceeded("other", start) {
		t.Error("a key with no failures is exceeded")
	}

	// The first failure leaves the window a minute after it happened.
	if limiter.exceeded("k", start.Add(time.Minute)) {
		t.Error("still exceeded once the oldest failure left the window")
	}

	if limiter.exceeded("k", start.Add(time.Hour)) || len(limiter.failures) != 0 {
		t.Errorf("after the window: %d keys kept, want the key dropped", len(limiter.failures))
	}
}

func TestAttemptLimiterClear(t *testing.T) {
	t.Parallel()

	now := newTestClock().now()
	limiter := newAttemptLimiter(1, time.Minute, 10)
	limiter.fail("k", now)
	limiter.clear("k")

	if limiter.exceeded("k", now) || len(limiter.failures) != 0 {
		t.Errorf("cleared key still counted: %+v", limiter.failures)
	}
}

// However many keys and failures are thrown at it, the limiter keeps
// at most maxKeys keys and limit failures per key.
func TestAttemptLimiterMemoryIsBounded(t *testing.T) {
	t.Parallel()

	now := newTestClock().now()
	limiter := newAttemptLimiter(3, time.Minute, 50)

	for i := range 5000 {
		now = now.Add(time.Millisecond)
		limiter.fail(fmt.Sprintf("key-%d", i), now)
		limiter.fail("hammered", now)

		if len(limiter.failures) > 50 {
			t.Fatalf("after %d keys the limiter holds %d, want at most 50", i+1, len(limiter.failures))
		}
	}

	for key, failures := range limiter.failures {
		if len(failures) > 3 {
			t.Errorf("%s holds %d failures, want at most 3", key, len(failures))
		}
	}

	// The key that kept failing was never the oldest, so it survived.
	if !limiter.exceeded("hammered", now) {
		t.Error("the busiest key was evicted before idle ones")
	}
}

func TestAttemptLimiterEvictsExpiredKeysFirst(t *testing.T) {
	t.Parallel()

	start := newTestClock().now()
	limiter := newAttemptLimiter(3, time.Minute, 3)

	limiter.fail("stale-1", start)
	limiter.fail("stale-2", start)
	limiter.fail("fresh", start.Add(50*time.Second))
	limiter.fail("new", start.Add(90*time.Second))

	if len(limiter.failures) != 2 {
		t.Fatalf("keys = %d, want the two stale keys gone", len(limiter.failures))
	}

	for _, key := range []string{"fresh", "new"} {
		if _, ok := limiter.failures[key]; !ok {
			t.Errorf("%s was evicted while expired keys were available", key)
		}
	}
}

func TestUsernameLaneHasABoundedKey(t *testing.T) {
	t.Parallel()

	short := usernameLane("viewer")
	long := usernameLane(strings.Repeat("a", 5000))
	other := usernameLane(strings.Repeat("a", 5001))

	if short != usernameLanePrefix+"viewer" {
		t.Errorf("lane of a normal username = %q", short)
	}

	if len(long) > len(usernameLanePrefix)+maxKeyedUsernameLength || long == other {
		t.Errorf("long usernames: key of %d bytes, distinct = %t; want a bounded, distinct key", len(long), long != other)
	}
}

// The limit holds whatever address the attempts come from, and a known
// and an unknown username go through exactly the same answers.
func TestLoginThrottlePerUsernameAnswersKnownAndUnknownAlike(t *testing.T) {
	t.Parallel()

	harness := newThrottleHarness(t)
	sequences := map[string][]loginAttempt{}

	for _, username := range []string{testViewerUsername, testUnknownUsername} {
		for range maxUsernameFailures + 3 {
			sequences[username] = append(sequences[username], harness.login(t, username, "wrong-password"))
		}
	}

	known, unknown := sequences[testViewerUsername], sequences[testUnknownUsername]

	for i := range known {
		wantStatus := nethttp.StatusUnauthorized
		if i >= maxUsernameFailures {
			wantStatus = nethttp.StatusTooManyRequests
		}

		if known[i].status != wantStatus {
			t.Errorf("attempt %d for a known username = %d, want %d", i+1, known[i].status, wantStatus)
		}

		if known[i].status != unknown[i].status || known[i].body != unknown[i].body {
			t.Errorf("attempt %d differs: known %d %q, unknown %d %q",
				i+1, known[i].status, known[i].body, unknown[i].status, unknown[i].body)
		}

		if len(known[i].cookies) != 0 || len(unknown[i].cookies) != 0 {
			t.Errorf("attempt %d set cookies: known %d, unknown %d", i+1, len(known[i].cookies), len(unknown[i].cookies))
		}
	}

	// The right password is refused too while the lane is spent: the
	// password is not looked at.
	if got := harness.login(t, testViewerUsername, testViewerPassword); got.status != nethttp.StatusTooManyRequests {
		t.Errorf("correct password from a new address while limited = %d, want 429", got.status)
	}

	// Another username is not affected.
	if got := harness.login(t, defaultLoginUsername, testPassword); got.status != nethttp.StatusNoContent {
		t.Errorf("another user's login while one username is limited = %d, want 204", got.status)
	}
}

// The two limits answer the same bytes, so a client cannot tell which
// one it met.
func TestLoginThrottleAnswersLikeTheAddressLimit(t *testing.T) {
	t.Parallel()

	harness := newThrottleHarness(t)
	harness.exhaust(t, testViewerUsername)

	byUsername := harness.login(t, testViewerUsername, "wrong-password")

	var byAddress loginAttempt
	for i := range maxFailures + 1 {
		byAddress = harness.loginFrom(t, "203.0.113.9:40000", fmt.Sprintf("someone-%d", i), "wrong-password")
	}

	if byUsername.status != nethttp.StatusTooManyRequests || byAddress.status != nethttp.StatusTooManyRequests {
		t.Fatalf("statuses = %d and %d, want 429 for both", byUsername.status, byAddress.status)
	}

	if byUsername.body != byAddress.body {
		t.Errorf("bodies differ: %q by username, %q by address", byUsername.body, byAddress.body)
	}
}

// Refused attempts are not counted, so an attacker who keeps sending
// cannot stretch the limit: it lifts as the counted failures age out.
func TestLoginThrottleWindowExpires(t *testing.T) {
	t.Parallel()

	harness := newThrottleHarness(t)
	harness.exhaust(t, testViewerUsername)

	for range 5 {
		harness.clock.advance(time.Minute)

		for range 20 {
			if got := harness.login(t, testViewerUsername, "wrong-password"); got.status != nethttp.StatusTooManyRequests {
				t.Fatalf("attempt while limited = %d, want 429", got.status)
			}
		}
	}

	harness.clock.advance(usernameFailureWindow - 5*time.Minute)

	if got := harness.login(t, testViewerUsername, testViewerPassword); got.status != nethttp.StatusNoContent {
		t.Errorf("login a window after the last counted failure = %d, want 204", got.status)
	}
}

// A success proves the password, so the username starts again from
// zero.
func TestLoginThrottleSuccessClearsTheLane(t *testing.T) {
	t.Parallel()

	harness := newThrottleHarness(t)

	for range maxUsernameFailures - 1 {
		harness.login(t, testViewerUsername, "wrong-password")
	}

	if got := harness.login(t, testViewerUsername, testViewerPassword); got.status != nethttp.StatusNoContent {
		t.Fatalf("login before the limit = %d, want 204", got.status)
	}

	for i := range maxUsernameFailures {
		if got := harness.login(t, testViewerUsername, "wrong-password"); got.status != nethttp.StatusUnauthorized {
			t.Fatalf("failure %d after a success = %d, want 401", i+1, got.status)
		}
	}
}

// A browser the user has signed in from keeps working while strangers
// spend the username's limit: an attacker cannot lock the user out.
func TestLoginThrottleSparesARememberedBrowser(t *testing.T) {
	t.Parallel()

	harness := newThrottleHarness(t)

	first := harness.login(t, testViewerUsername, testViewerPassword)
	if first.status != nethttp.StatusNoContent {
		t.Fatalf("first login = %d, want 204", first.status)
	}

	remembered := first.device(t)

	admin := harness.login(t, defaultLoginUsername, testPassword)
	adminDevice := admin.device(t)

	harness.exhaust(t, testViewerUsername)

	if got := harness.login(t, testViewerUsername, testViewerPassword); got.status != nethttp.StatusTooManyRequests {
		t.Errorf("stranger with the right password = %d, want 429", got.status)
	}

	//nolint:gosec // G124: a request cookie a client made up; it has no attributes.
	forged := &nethttp.Cookie{Name: deviceCookie, Value: strings.Repeat("0", len(remembered.Value))}
	if got := harness.login(t, testViewerUsername, testViewerPassword, forged); got.status != nethttp.StatusTooManyRequests {
		t.Errorf("made-up device token = %d, want 429", got.status)
	}

	// Another account's device token is no pass for this username.
	if got := harness.login(t, testViewerUsername, testViewerPassword, adminDevice); got.status != nethttp.StatusTooManyRequests {
		t.Errorf("another user's device token = %d, want 429", got.status)
	}

	again := harness.login(t, testViewerUsername, testViewerPassword, remembered)
	if again.status != nethttp.StatusNoContent {
		t.Fatalf("remembered browser with the right password = %d, want 204", again.status)
	}

	if again.device(t).Value != remembered.Value {
		t.Errorf("a remembered browser was handed a different device token")
	}

	// Its success does not reopen the username for strangers.
	if got := harness.login(t, testViewerUsername, testViewerPassword); got.status != nethttp.StatusTooManyRequests {
		t.Errorf("stranger after the remembered browser signed in = %d, want 429", got.status)
	}
}

// A device token is not a pass around the limit: its holder gets a
// lane with the same budget, and wrong passwords there are refused.
func TestLoginThrottleLimitsARememberedBrowserToo(t *testing.T) {
	t.Parallel()

	harness := newThrottleHarness(t)
	remembered := harness.login(t, testViewerUsername, testViewerPassword).device(t)

	for i := range maxUsernameFailures {
		if got := harness.login(t, testViewerUsername, "wrong-password", remembered); got.status != nethttp.StatusUnauthorized {
			t.Fatalf("failure %d on a remembered browser = %d, want 401", i+1, got.status)
		}
	}

	if got := harness.login(t, testViewerUsername, testViewerPassword, remembered); got.status != nethttp.StatusTooManyRequests {
		t.Errorf("remembered browser after its own failures = %d, want 429", got.status)
	}

	// Its failures were its own: a stranger's lane is untouched.
	if got := harness.login(t, testViewerUsername, testViewerPassword); got.status != nethttp.StatusNoContent {
		t.Errorf("stranger while a remembered browser is limited = %d, want 204", got.status)
	}
}

func TestLoginThrottleForgetsABrowser(t *testing.T) {
	t.Parallel()

	t.Run("after it expires", func(t *testing.T) {
		t.Parallel()

		harness := newThrottleHarness(t)
		remembered := harness.login(t, testViewerUsername, testViewerPassword).device(t)

		harness.clock.advance(deviceExpiry)
		harness.exhaust(t, testViewerUsername)

		if got := harness.login(t, testViewerUsername, testViewerPassword, remembered); got.status != nethttp.StatusTooManyRequests {
			t.Errorf("expired device token = %d, want 429", got.status)
		}
	})

	t.Run("when an admin resets the password", func(t *testing.T) {
		t.Parallel()

		harness := newThrottleHarness(t)
		remembered := harness.login(t, testViewerUsername, testViewerPassword).device(t)

		harness.server.auth.DropSessions(testViewerUsername)
		harness.exhaust(t, testViewerUsername)

		if got := harness.login(t, testViewerUsername, testViewerPassword, remembered); got.status != nethttp.StatusTooManyRequests {
			t.Errorf("device token from before the reset = %d, want 429", got.status)
		}
	})
}

// The remembered browsers are bounded per user and in total, and one
// user signing in over and over pushes out only their own.
func TestRememberDeviceIsBounded(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	auth := server.auth
	clock := newTestClock()
	auth.now = clock.now

	kept := auth.RememberDevice("someone-else", "")

	tokens := []string{}

	for range maxDevicesPerUser * 3 {
		clock.advance(time.Second)

		tokens = append(tokens, auth.RememberDevice(testViewerUsername, ""))
	}

	if got := auth.countDevicesLocked(testViewerUsername); got != maxDevicesPerUser {
		t.Errorf("devices of one user = %d, want %d", got, maxDevicesPerUser)
	}

	newest, oldest := tokens[len(tokens)-1], tokens[0]

	if auth.LoginLane(testViewerUsername, newest) != deviceLanePrefix+newest {
		t.Error("the newest device was forgotten")
	}

	if auth.LoginLane(testViewerUsername, oldest) != usernameLane(testViewerUsername) {
		t.Error("the oldest device is still remembered past the per-user maximum")
	}

	if auth.LoginLane("someone-else", kept) != deviceLanePrefix+kept {
		t.Error("one user's logins pushed out another user's device")
	}

	for i := range maxDevices + 50 {
		clock.advance(time.Second)
		auth.RememberDevice(fmt.Sprintf("user-%d", i), "")

		if len(auth.devices) > maxDevices {
			t.Fatalf("devices = %d, want at most %d", len(auth.devices), maxDevices)
		}
	}
}

// The login path keeps no state for an address or a username once its
// failures have aged out.
func TestLoginThrottleStateDrains(t *testing.T) {
	t.Parallel()

	harness := newThrottleHarness(t)

	for i := range 40 {
		harness.login(t, fmt.Sprintf("guess-%d", i), "wrong-password")
	}

	auth := harness.server.auth

	if len(auth.lanes.failures) != 40 {
		t.Fatalf("tracked usernames = %d, want 40", len(auth.lanes.failures))
	}

	harness.clock.advance(usernameFailureWindow)

	for i := range 40 {
		auth.LaneExceeded(usernameLane(fmt.Sprintf("guess-%d", i)))
	}

	for i := range 250 {
		auth.AttemptsExceeded(fmt.Sprintf("198.51.100.%d", i+1))
	}

	if len(auth.lanes.failures) != 0 || len(auth.failures.failures) != 0 {
		t.Errorf("after the windows: %d usernames and %d addresses still tracked, want none",
			len(auth.lanes.failures), len(auth.failures.failures))
	}
}
