package http

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Login throttle policy (ADR-0017). The per-address limit is the older
// one and is unchanged; the per-username limit holds whatever address
// the attempts come from.
const (
	// maxUsernameFailures and usernameFailureWindow bound guesses at
	// one username from browsers that have never signed in as it.
	maxUsernameFailures   = 10
	usernameFailureWindow = 15 * time.Minute
	// maxTrackedKeys bounds each failure map. Usernames in it are
	// whatever a client typed, so the map must not grow with them.
	maxTrackedKeys = 10000
	// maxKeyedUsernameLength is the longest username kept as typed; a
	// longer one, which no account can have, is tracked by its digest.
	maxKeyedUsernameLength = 64

	// deviceCookie names the cookie that remembers a browser a user
	// has signed in from. It proves nothing about who is asking; it
	// only moves that browser's attempts to a lane of their own.
	deviceCookie = "touchgrass_device"
	// deviceCookiePath limits the cookie to the one route that reads it.
	deviceCookiePath = "/api/auth/login"
	deviceExpiry     = 30 * 24 * time.Hour
	// maxDevices and maxDevicesPerUser bound the remembered browsers.
	maxDevices        = 4096
	maxDevicesPerUser = 8

	usernameLanePrefix = "user:"
	deviceLanePrefix   = "device:"
)

// attemptLimiter counts failures per key inside a sliding window. It
// holds at most maxKeys keys and at most limit failures per key, so its
// memory is bounded whatever the keys are. It is not safe for
// concurrent use: the Authenticator's mutex guards it.
type attemptLimiter struct {
	limit    int
	window   time.Duration
	maxKeys  int
	failures map[string][]time.Time
}

// newAttemptLimiter builds an empty limiter.
func newAttemptLimiter(limit int, window time.Duration, maxKeys int) *attemptLimiter {
	return &attemptLimiter{limit: limit, window: window, maxKeys: maxKeys, failures: map[string][]time.Time{}}
}

// exceeded reports whether key has used up its failures. Failures
// older than the window are forgotten, and a key left with none is
// dropped.
func (l *attemptLimiter) exceeded(key string, now time.Time) bool {
	return len(l.prune(key, now)) >= l.limit
}

// fail records one failure for key.
func (l *attemptLimiter) fail(key string, now time.Time) {
	recent := l.prune(key, now)

	if len(recent) == 0 && len(l.failures) >= l.maxKeys {
		l.evict(now)
	}

	recent = append(recent, now)

	// Two requests that passed the check together may both fail; keep
	// only the newest failures so a key never holds more than limit.
	if len(recent) > l.limit {
		recent = recent[len(recent)-l.limit:]
	}

	l.failures[key] = recent
}

// clear forgets key.
func (l *attemptLimiter) clear(key string) {
	delete(l.failures, key)
}

// prune drops key's failures that left the window and returns the rest.
func (l *attemptLimiter) prune(key string, now time.Time) []time.Time {
	stored, ok := l.failures[key]
	if !ok {
		return nil
	}

	cutoff := now.Add(-l.window)
	recent := stored[:0]

	for _, failure := range stored {
		if failure.After(cutoff) {
			recent = append(recent, failure)
		}
	}

	if len(recent) == 0 {
		delete(l.failures, key)

		return nil
	}

	l.failures[key] = recent

	return recent
}

// evict makes room for one more key: every key whose failures all left
// the window goes, and if the map is still full, so does the key whose
// latest failure is the oldest.
func (l *attemptLimiter) evict(now time.Time) {
	cutoff := now.Add(-l.window)
	oldestKey, oldest := "", time.Time{}

	for key, stored := range l.failures {
		latest := stored[len(stored)-1]

		if !latest.After(cutoff) {
			delete(l.failures, key)

			continue
		}

		if oldestKey == "" || latest.Before(oldest) {
			oldestKey, oldest = key, latest
		}
	}

	if len(l.failures) >= l.maxKeys {
		delete(l.failures, oldestKey)
	}
}

// device is a browser remembered for one username.
type device struct {
	username string
	expires  time.Time
}

// usernameLane is the throttle key for attempts at username from a
// browser that is not remembered for it. A name longer than any
// account can have is keyed by its digest, so a key has a fixed
// largest size.
func usernameLane(username string) string {
	if len(username) > maxKeyedUsernameLength {
		sum := sha256.Sum256([]byte(username))

		return usernameLanePrefix + hex.EncodeToString(sum[:])
	}

	return usernameLanePrefix + username
}
