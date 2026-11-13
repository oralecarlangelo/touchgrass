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

// An interactive call must not queue behind background collection: with
// every shared permit held, it takes the reserved one at once.
func TestRequestGuardInteractiveCallTakesTheReservedPermit(t *testing.T) {
	t.Parallel()

	guard := newRequestGuard(requestGuardConfig{MaxConcurrent: 1, InteractiveWait: time.Minute})

	collector, err := guard.acquire(t.Context())
	if err != nil {
		t.Fatalf("background acquire: %v", err)
	}
	defer collector(nil)

	acquired := make(chan func(error), 1)

	go func() {
		release, acquireErr := guard.acquire(WithInteractive(t.Context()))
		if acquireErr == nil {
			acquired <- release
		}
	}()

	select {
	case release := <-acquired:
		release(nil)
	case <-time.After(time.Second):
		t.Fatal("interactive acquire queued behind a background call")
	}
}

// The reserved permit is for interactive calls only: background work
// stays bounded by the shared permits.
func TestRequestGuardBackgroundCallNeverTakesTheReservedPermit(t *testing.T) {
	t.Parallel()

	guard := newRequestGuard(requestGuardConfig{MaxConcurrent: 1})

	collector, err := guard.acquire(t.Context())
	if err != nil {
		t.Fatalf("background acquire: %v", err)
	}
	defer collector(nil)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	if _, err := guard.acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second background acquire error = %v, want %v", err, context.DeadlineExceeded)
	}
}

// With every permit taken, an interactive call gives up after a bounded
// wait instead of queueing, and giving up says nothing about the
// daemon's health.
func TestRequestGuardInteractiveCallFailsBusyAfterBoundedWait(t *testing.T) {
	t.Parallel()

	guard := newRequestGuard(requestGuardConfig{
		MaxConcurrent:   1,
		FailureLimit:    3,
		InteractiveWait: 10 * time.Millisecond,
	})

	collector, err := guard.acquire(t.Context())
	if err != nil {
		t.Fatalf("background acquire: %v", err)
	}

	reader, err := guard.acquire(WithInteractive(t.Context()))
	if err != nil {
		t.Fatalf("first interactive acquire: %v", err)
	}

	for attempt := range 5 {
		started := time.Now()

		_, err := guard.acquire(WithInteractive(t.Context()))
		if !errors.Is(err, ErrEngineBusy) {
			t.Fatalf("attempt %d error = %v, want %v", attempt, err, ErrEngineBusy)
		}

		if waited := time.Since(started); waited > time.Second {
			t.Fatalf("attempt %d waited %s, want the bounded wait", attempt, waited)
		}
	}

	collector(nil)
	reader(nil)

	done, err := guard.acquire(t.Context())
	if err != nil {
		t.Fatalf("acquire after busy refusals: %v, want the guard still closed", err)
	}

	done(nil)
}

// An engine that is slow to answer an interactive call is reported as
// busy before the full timeout, and is not counted as failing.
func TestGuardedCallCutsASlowInteractiveCallShort(t *testing.T) {
	t.Parallel()

	guard := newRequestGuard(requestGuardConfig{
		MaxConcurrent:          1,
		FailureLimit:           3,
		InteractiveCallTimeout: 10 * time.Millisecond,
	})

	slow := func(ctx context.Context) (int, error) {
		<-ctx.Done()

		return 0, ctx.Err()
	}

	for attempt := range 5 {
		_, err := guardedCall(WithInteractive(t.Context()), guard, time.Minute, slow)
		if !errors.Is(err, ErrEngineBusy) {
			t.Fatalf("attempt %d error = %v, want %v", attempt, err, ErrEngineBusy)
		}
	}

	value, err := guardedCall(t.Context(), guard, time.Minute, func(context.Context) (int, error) {
		return 7, nil
	})
	if err != nil || value != 7 {
		t.Fatalf("guardedCall() after slow interactive calls = %d, %v, want 7, nil", value, err)
	}
}

// A background call that runs out its full timeout is still a failure.
func TestGuardedCallCountsABackgroundTimeout(t *testing.T) {
	t.Parallel()

	guard := newRequestGuard(requestGuardConfig{MaxConcurrent: 1, FailureLimit: 3})

	var last error

	for range 4 {
		_, last = guardedCall(t.Context(), guard, 10*time.Millisecond, func(ctx context.Context) (int, error) {
			<-ctx.Done()

			return 0, ctx.Err()
		})
	}

	if !errors.Is(last, ErrDaemonBackoff) {
		t.Fatalf("guardedCall() after repeated timeouts error = %v, want %v", last, ErrDaemonBackoff)
	}
}
