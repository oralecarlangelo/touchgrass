package docker

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	defaultDockerMaxConcurrent = 2
	defaultDockerFailureLimit  = 3
	defaultDockerCooldown      = time.Minute
	defaultDockerCallTimeout   = 10 * time.Second
	defaultDockerDiffTimeout   = 30 * time.Second
	defaultDockerPruneTimeout  = 2 * time.Minute
)

// ErrDaemonBackoff reports that Docker calls are paused after repeated
// failures. Callers should keep their last known state and retry later.
var ErrDaemonBackoff = errors.New("docker daemon requests temporarily paused")

type requestGuardConfig struct {
	MaxConcurrent int
	FailureLimit  int
	Cooldown      time.Duration
	Now           func() time.Time
}

// requestGuard bounds one-shot Docker calls and pauses them after repeated
// daemon failures. Streaming event subscriptions do not use this guard.
type requestGuard struct {
	permits      chan struct{}
	failureLimit int
	cooldown     time.Duration
	now          func() time.Time

	mu                 sync.Mutex
	failures           int
	failureWindowStart time.Time
	openUntil          time.Time
}

func newRequestGuard(cfg requestGuardConfig) *requestGuard {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultDockerMaxConcurrent
	}

	if cfg.FailureLimit <= 0 {
		cfg.FailureLimit = defaultDockerFailureLimit
	}

	if cfg.Cooldown <= 0 {
		cfg.Cooldown = defaultDockerCooldown
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &requestGuard{
		permits:      make(chan struct{}, cfg.MaxConcurrent),
		failureLimit: cfg.FailureLimit,
		cooldown:     cfg.Cooldown,
		now:          cfg.Now,
	}
}

func (g *requestGuard) acquire(ctx context.Context) (func(error), error) {
	if g.isOpen() {
		return nil, ErrDaemonBackoff
	}

	select {
	case g.permits <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	if g.isOpen() {
		<-g.permits

		return nil, ErrDaemonBackoff
	}

	var once sync.Once

	return func(err error) {
		once.Do(func() {
			g.record(ctx, err)
			<-g.permits
		})
	}, nil
}

func (g *requestGuard) isOpen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := g.now()
	if g.openUntil.IsZero() {
		if !g.failureWindowStart.IsZero() && !now.Before(g.failureWindowStart.Add(g.cooldown)) {
			g.failures = 0
			g.failureWindowStart = time.Time{}
		}

		return false
	}

	if now.Before(g.openUntil) {
		return true
	}

	g.openUntil = time.Time{}
	g.failures = 0
	g.failureWindowStart = time.Time{}

	return false
}

func (g *requestGuard) record(ctx context.Context, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if err == nil {
		return
	}

	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}

	now := g.now()
	if g.failureWindowStart.IsZero() || !now.Before(g.failureWindowStart.Add(g.cooldown)) {
		g.failures = 0
		g.failureWindowStart = now
	}

	g.failures++
	if g.failures >= g.failureLimit {
		g.openUntil = now.Add(g.cooldown)
	}
}

func guardedCall[T any](
	ctx context.Context,
	guard *requestGuard,
	timeout time.Duration,
	call func(context.Context) (T, error),
) (T, error) {
	var zero T

	done, err := guard.acquire(ctx)
	if err != nil {
		return zero, err
	}

	callCtx := ctx

	cancel := func() {}
	if timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	value, callErr := call(callCtx)
	done(callErr)

	return value, callErr
}
