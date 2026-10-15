// Package dbstats collects bounded PostgreSQL and Redis aggregate statistics.
package dbstats

import (
	"context"
	"fmt"
	"os/exec"
)

// Runner executes one binary with an argument vector. Implementations must not
// invoke a shell.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type commandRunner struct{}

// NewCommandRunner returns the production no-shell command runner.
func NewCommandRunner() Runner { return commandRunner{} }

func (commandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	//nolint:gosec // callers use the docker literal and fixed commands plus validated config.
	output, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("running %s: %w", name, err)
	}
	return output, nil
}

// CounterDelta returns a non-negative delta only when a prior baseline is
// valid. A lower current value denotes a reset and starts a new baseline.
func CounterDelta(current, previous float64, hasPrevious bool) (delta float64, reset, valid bool) {
	if !hasPrevious {
		return 0, false, false
	}
	if current < previous {
		return 0, true, false
	}
	return current - previous, false, true
}
