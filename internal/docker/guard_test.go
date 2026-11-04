package docker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRequestGuardBoundsConcurrency(t *testing.T) {
	t.Parallel()

	guard := newRequestGuard(requestGuardConfig{
		MaxConcurrent: 1,
		FailureLimit:  3,
		Cooldown:      time.Minute,
	})

	done, err := guard.acquire(t.Context())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	acquired := make(chan func(error), 1)

	go func() {
		release, acquireErr := guard.acquire(t.Context())
		if acquireErr == nil {
			acquired <- release
		}
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire completed before the first released")
	case <-time.After(20 * time.Millisecond):
	}

	done(nil)

	select {
	case release := <-acquired:
		release(nil)
	case <-time.After(time.Second):
		t.Fatal("second acquire did not proceed after release")
	}
}

func TestRequestGuardOpensAndRecovers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.November, 5, 0, 0, 0, 0, time.UTC)
	guard := newRequestGuard(requestGuardConfig{
		MaxConcurrent: 1,
		FailureLimit:  3,
		Cooldown:      time.Minute,
		Now:           func() time.Time { return now },
	})

	for range 3 {
		done, err := guard.acquire(t.Context())
		if err != nil {
			t.Fatalf("acquire before threshold: %v", err)
		}

		done(errors.New("daemon unavailable"))
	}

	if _, err := guard.acquire(t.Context()); !errors.Is(err, ErrDaemonBackoff) {
		t.Fatalf("open circuit error = %v, want %v", err, ErrDaemonBackoff)
	}

	now = now.Add(time.Minute)

	done, err := guard.acquire(t.Context())
	if err != nil {
		t.Fatalf("half-open acquire: %v", err)
	}

	done(nil)

	done, err = guard.acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire after recovery: %v", err)
	}

	done(nil)
}

func TestRequestGuardSuccessDoesNotMaskFailureBurst(t *testing.T) {
	t.Parallel()

	guard := newRequestGuard(requestGuardConfig{
		MaxConcurrent: 1,
		FailureLimit:  3,
		Cooldown:      time.Minute,
	})

	for attempt := range 3 {
		done, err := guard.acquire(t.Context())
		if err != nil {
			t.Fatalf("failure acquire: %v", err)
		}

		done(errors.New("daemon unavailable"))

		if attempt == 2 {
			continue
		}

		done, err = guard.acquire(t.Context())
		if err != nil {
			t.Fatalf("success acquire: %v", err)
		}

		done(nil)
	}

	if _, err := guard.acquire(t.Context()); !errors.Is(err, ErrDaemonBackoff) {
		t.Fatalf("open circuit error = %v, want %v", err, ErrDaemonBackoff)
	}
}

func TestRequestGuardHonorsCancellation(t *testing.T) {
	t.Parallel()

	guard := newRequestGuard(requestGuardConfig{
		MaxConcurrent: 1,
		FailureLimit:  3,
		Cooldown:      time.Minute,
	})

	done, err := guard.acquire(t.Context())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer done(nil)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := guard.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled acquire error = %v, want %v", err, context.Canceled)
	}
}
