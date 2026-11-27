// Package http implements the touchgrass HTTP transport.
package http

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/objectstore"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

const (
	readTimeout     = 5 * time.Second
	writeTimeout    = 10 * time.Second
	idleTimeout     = 60 * time.Second
	shutdownTimeout = 5 * time.Second
)

// Config wires a Server.
type Config struct {
	Objects        *objectstore.Client
	Addr           string
	Version        string
	Logger         *slog.Logger
	Inventory      *service.Inventory
	Sampler        *service.Sampler
	Deploys        *service.Deploys
	Cutover        *service.Cutover
	Audit          *service.Audit
	Auth           *Authenticator
	Users          *service.Users
	Events         *Hub
	Ingestor       *service.Ingestor
	Logs           *service.LogCollector
	SDKLogs        *service.LogIngestor
	System         *service.System
	Database       *service.Database
	Onboarding     *service.Onboarding
	Performance    *service.Performance
	FleetInventory *service.FleetInventory
	Security       *service.Security
	Env            *service.Env
	Owners         *service.Owners
	Maintenance    MaintenanceReporter
	SelfUpdate     *service.SelfUpdate
	Dist           fs.FS
	Docs           fs.FS
	DevProxy       string
}

// Server is the touchgrass HTTP server.
type Server struct {
	objects        *objectstore.Client
	addr           string
	version        string
	logger         *slog.Logger
	inventory      *service.Inventory
	sampler        *service.Sampler
	deploys        *service.Deploys
	cutover        *service.Cutover
	audit          *service.Audit
	auth           *Authenticator
	users          *service.Users
	events         *Hub
	ingestor       *service.Ingestor
	logs           *service.LogCollector
	sdkLogs        *service.LogIngestor
	system         *service.System
	database       *service.Database
	onboarding     *service.Onboarding
	performance    *service.Performance
	fleetInventory *service.FleetInventory
	security       *service.Security
	env            *service.Env
	eventHeartbeat time.Duration
	owners         *service.Owners
	maintenance    MaintenanceReporter
	selfUpdate     *service.SelfUpdate
	handler        nethttp.Handler
}

// MaintenanceReporter is the SQLite maintenance pillar as /api/health reads it:
// the last sweep's outcome. The sweep is a WRITE, so its status is the cheapest
// honest answer to "can this instance still record anything" — a read probe
// cannot tell, because a locked database still serves every read.
type MaintenanceReporter interface {
	Status() model.MaintenanceStatus
}

// New builds a Server.
func New(cfg Config) *Server {
	mux := nethttp.NewServeMux()
	server := &Server{
		objects:        cfg.Objects,
		addr:           cfg.Addr,
		version:        cfg.Version,
		logger:         cfg.Logger,
		inventory:      cfg.Inventory,
		sampler:        cfg.Sampler,
		deploys:        cfg.Deploys,
		cutover:        cfg.Cutover,
		audit:          cfg.Audit,
		auth:           cfg.Auth,
		users:          cfg.Users,
		events:         cfg.Events,
		ingestor:       cfg.Ingestor,
		logs:           cfg.Logs,
		sdkLogs:        cfg.SDKLogs,
		system:         cfg.System,
		database:       cfg.Database,
		onboarding:     cfg.Onboarding,
		performance:    cfg.Performance,
		fleetInventory: cfg.FleetInventory,
		security:       cfg.Security,
		env:            cfg.Env,
		eventHeartbeat: eventHeartbeat,
		owners:         cfg.Owners,
		maintenance:    cfg.Maintenance,
		selfUpdate:     cfg.SelfUpdate,
	}

	// Every API route comes from the route table and is wrapped in its
	// guard. Nothing under /api/ is registered anywhere else.
	for _, rt := range server.routes() {
		mux.HandleFunc(rt.method+" "+rt.pattern, server.guard(rt))
	}

	mux.HandleFunc("GET /docs", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Redirect(w, r, "/docs/", nethttp.StatusFound)
	})
	mux.Handle("GET /docs/", Docs(cfg.Logger, cfg.Docs))

	if cfg.DevProxy != "" {
		mux.Handle("GET /", DevProxy(cfg.Logger, cfg.DevProxy))
	} else {
		mux.Handle("GET /", SPA(cfg.Logger, cfg.Dist))
	}

	var handler nethttp.Handler = mux

	handler = cfg.Auth.Middleware(cfg.Logger, handler)
	handler = WithSecurityHeaders(handler)
	handler = WithLogging(cfg.Logger, handler)
	server.handler = handler

	return server
}

// Run serves HTTP until ctx is cancelled, then drains gracefully.
func (s *Server) Run(ctx context.Context) error {
	srv := &nethttp.Server{
		Addr:              s.addr,
		Handler:           s.handler,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ReadHeaderTimeout: readTimeout,
	}

	errCh := make(chan error, 1)

	go func() {
		s.logger.Info("listening", "addr", s.addr)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			errCh <- err

			return
		}

		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		s.logger.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutting down: %w", err)
		}

		return <-errCh
	case err := <-errCh:
		return err
	}
}

// healthResponse is the /api/health payload. `checks` is additive: a caller
// that only reads `status` or the HTTP code keeps working.
type healthResponse struct {
	Status  string        `json:"status"`
	Version string        `json:"version"`
	Checks  []healthCheck `json:"checks,omitempty"`
}

// healthCheck is one named assertion behind the status.
type healthCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

const (
	checkPass      = "pass"
	checkWarn      = "warn"
	checkFail      = "fail"
	healthOK       = "ok"
	healthDegraded = "degraded"

	maintenanceCheckName = "sqlite_maintenance"
)

// handleHealth reports readiness, not merely liveness, and answers 503 once
// this instance can no longer record anything.
//
// It used to answer a flat `{"status":"ok"}` without touching the database. A
// GRID production host then ran from 10 to 26 Nov 2026 — sixteen days — with
// every insert failing on SQLITE_BUSY while this endpoint, the installer's
// probe and any uptime monitor all read "ok". An endpoint that asserts nothing
// cannot report anything, so it reported health it had never checked.
func (s *Server) handleHealth(w nethttp.ResponseWriter, _ *nethttp.Request) {
	response := healthResponse{Status: healthOK, Version: s.version}
	code := nethttp.StatusOK

	if check, ok := s.maintenanceCheck(); ok {
		response.Checks = append(response.Checks, check)
		if check.Status == checkFail {
			response.Status = healthDegraded
			code = nethttp.StatusServiceUnavailable
		}
	}

	writeJSON(w, s.logger, code, response)
}

// maintenanceCheck turns the last SQLite sweep into one assertion.
//
// The sweep writes, so its outcome answers "can this instance still record
// anything" — which a read probe cannot, because a write-locked database still
// serves every read, as it did for those sixteen days.
//
// A sweep that has not run yet PASSES: the first one is up to one maintenance
// interval after start-up, and a fresh install must not answer 503 to the
// installer's probe. A storage-budget overrun WARNS rather than fails — it
// already logs its own warning and the instance stays usable, so 503 keeps its
// single meaning of "do not trust what this instance records".
func (s *Server) maintenanceCheck() (healthCheck, bool) {
	if s.maintenance == nil {
		return healthCheck{}, false
	}

	status := s.maintenance.Status()
	check := healthCheck{Name: maintenanceCheckName, Status: checkPass}

	switch {
	case status.LastError != "":
		check.Status = checkFail
		check.Detail = status.LastError
	case status.LastSuccessAt.IsZero():
		check.Detail = "no sweep yet"
	case status.Stale:
		check.Status = checkFail
		check.Detail = "last successful sweep " + status.LastSuccessAt.UTC().Format(time.RFC3339)
	case status.LastResult.OverBudget:
		check.Status = checkWarn
		if status.LastResult.DatabaseBytes > status.LastResult.MaxBytes {
			check.Detail = fmt.Sprintf("database %d bytes over the %d byte budget",
				status.LastResult.DatabaseBytes-status.LastResult.MaxBytes, status.LastResult.MaxBytes)
		} else {
			check.Detail = "storage classes over budget: " + strings.Join(status.LastResult.OverBudgetClasses, ", ")
		}
	}

	return check, true
}
