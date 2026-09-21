package probe

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const (
	// probeTimeout bounds one direct health check.
	probeTimeout = 5 * time.Second
	// healthyStatus is the only status that counts, matching
	// bluegreen-lib.sh wait_http.
	healthyStatus = 200
)

// Result is one health probe outcome.
type Result struct {
	Healthy    bool
	StatusCode int
}

// Prober performs direct HTTP health checks.
type Prober struct {
	client *http.Client
}

// New builds a Prober with the standard timeout.
func New() *Prober {
	return &Prober{client: &http.Client{Timeout: probeTimeout}}
}

// Check probes target and reports health. A performed check never errors;
// only an unperformable one (bad URL, network failure) does.
func (p *Prober) Check(ctx context.Context, target string) (Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Result{}, fmt.Errorf("building probe request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("probing %s: %w", target, err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	return Result{Healthy: resp.StatusCode == healthyStatus, StatusCode: resp.StatusCode}, nil
}
