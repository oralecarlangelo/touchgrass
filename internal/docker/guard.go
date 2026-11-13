package docker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
)

const (
	defaultDockerMaxConcurrent = 2
	defaultDockerFailureLimit  = 3
	defaultDockerCooldown      = time.Minute
	defaultDockerCallTimeout   = 10 * time.Second
	defaultDockerDiffTimeout   = 30 * time.Second
	defaultDockerPruneTimeout  = 2 * time.Minute

	// An interactive call may wait this long for a permit and this long
	// for the engine's answer. Together they stay under the HTTP
	// server's write timeout, so a slow engine is reported as
	// ErrEngineBusy instead of a response cut off mid-flight.
	defaultDockerInteractiveWait        = 2 * time.Second
	defaultDockerInteractiveCallTimeout = 7 * time.Second
)

// ErrDaemonBackoff reports that Docker calls are paused after repeated
// failures. Callers should keep their last known state and retry later.
var ErrDaemonBackoff = errors.New("docker daemon requests temporarily paused")

// ErrEngineBusy reports that an interactive call got no permit, or no
// answer, within its bounds. The engine may be healthy; it is busy.
// Callers should report it and let the client retry.
var ErrEngineBusy = errors.New("docker engine busy")

type interactiveKey struct{}

// WithInteractive marks ctx as belonging to someone waiting for an
// answer. Engine calls made with it never queue behind background
// collection for long: they may take the permit reserved for them, and
// they fail with ErrEngineBusy rather than wait past their bounds.
func WithInteractive(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveKey{}, true)
}

// IsInteractive reports whether ctx was marked by WithInteractive.
func IsInteractive(ctx context.Context) bool {
	interactive, ok := ctx.Value(interactiveKey{}).(bool)

	return ok && interactive
}

type requestGuardConfig struct {
	MaxConcurrent          int
	FailureLimit           int
	Cooldown               time.Duration
	InteractiveWait        time.Duration
	InteractiveCallTimeout time.Duration
	Now                    func() time.Time
}

// requestGuard bounds one-shot Docker calls and pauses them after repeated
// daemon failures. Streaming event subscriptions do not use this guard.
//
// MaxConcurrent permits are shared by every caller. One more is reserved
// for interactive calls (see WithInteractive), so the engine sees at most
// MaxConcurrent+1 calls at once.
type requestGuard struct {
	permits                chan struct{}
	reserved               chan struct{}
	failureLimit           int
	cooldown               time.Duration
	interactiveWait        time.Duration
	interactiveCallTimeout time.Duration
	now                    func() time.Time

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

	if cfg.InteractiveWait <= 0 {
		cfg.InteractiveWait = defaultDockerInteractiveWait
	}

	if cfg.InteractiveCallTimeout <= 0 {
		cfg.InteractiveCallTimeout = defaultDockerInteractiveCallTimeout
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &requestGuard{
		permits:                make(chan struct{}, cfg.MaxConcurrent),
		reserved:               make(chan struct{}, 1),
		failureLimit:           cfg.FailureLimit,
		cooldown:               cfg.Cooldown,
		interactiveWait:        cfg.InteractiveWait,
		interactiveCallTimeout: cfg.InteractiveCallTimeout,
		now:                    cfg.Now,
	}
}

func (g *requestGuard) acquire(ctx context.Context) (func(error), error) {
	if g.isOpen() {
		return nil, ErrDaemonBackoff
	}

	held, err := g.takePermit(ctx)
	if err != nil {
		return nil, err
	}

	if g.isOpen() {
		<-held

		return nil, ErrDaemonBackoff
	}

	var once sync.Once

	return func(err error) {
		once.Do(func() {
			g.record(ctx, err)
			<-held
		})
	}, nil
}

// takePermit blocks until a permit is free and returns the pool it came
// from. A background call waits as long as ctx allows. An interactive
// call may also take the reserved permit, and gives up with
// ErrEngineBusy after the interactive wait.
func (g *requestGuard) takePermit(ctx context.Context) (chan struct{}, error) {
	if !IsInteractive(ctx) {
		select {
		case g.permits <- struct{}{}:
			return g.permits, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	timer := time.NewTimer(g.interactiveWait)
	defer timer.Stop()

	select {
	case g.reserved <- struct{}{}:
		return g.reserved, nil
	case g.permits <- struct{}{}:
		return g.permits, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("%w: no permit within %s", ErrEngineBusy, g.interactiveWait)
	}
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

	if isEngineAnswer(err) {
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

// isEngineAnswer reports whether err is the engine refusing a request
// it understood: the container, image or path is gone, or is in the
// wrong state. The daemon answered, so its health is not in question.
func isEngineAnswer(err error) bool {
	return cerrdefs.IsNotFound(err) || cerrdefs.IsConflict(err)
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

	// An interactive call is cut short of the full timeout. Running out
	// of that shorter time says the engine is slow for this caller, not
	// that it is failing, so it is reported as busy and not counted.
	isCutShort := IsInteractive(ctx) && (timeout <= 0 || timeout > guard.interactiveCallTimeout)
	if isCutShort {
		timeout = guard.interactiveCallTimeout
	}

	callCtx := ctx

	cancel := func() {}
	if timeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	value, callErr := call(callCtx)

	if isCutShort && callErr != nil && ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		done(nil)

		return zero, fmt.Errorf("%w: no answer within %s", ErrEngineBusy, timeout)
	}

	done(callErr)

	return value, callErr
}
