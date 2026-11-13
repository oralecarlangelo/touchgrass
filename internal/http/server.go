// Package http implements the touchgrass HTTP transport.
package http

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
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
	Dist           fs.FS
	Docs           fs.FS
	DevProxy       string
}

// Server is the touchgrass HTTP server.
type Server struct {
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
	handler        nethttp.Handler
}

// New builds a Server.
func New(cfg Config) *Server {
	mux := nethttp.NewServeMux()
	server := &Server{
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
	}

	// Every API route comes from the route table and is wrapped in its
	// guard. Nothing under /api/ is registered anywhere else.
	for _, rt := range server.routes() {
		mux.HandleFunc(rt.method+" "+rt.pattern, server.routeHandler(rt))
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

// routeHandler wraps a route in its guard. A read is also marked as
// having someone waiting on it, so its container-engine calls answer or
// fail with "engine busy" before the write timeout cuts the response
// (ADR-0017). Writes are left alone: several hand their context to a
// job that outlives the request and must keep waiting its turn for the
// engine.
func (s *Server) routeHandler(rt route) nethttp.HandlerFunc {
	guarded := s.guard(rt)
	if rt.method != nethttp.MethodGet {
		return guarded
	}

	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		guarded(w, r.WithContext(docker.WithInteractive(r.Context())))
	}
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

// healthResponse is the /api/health payload.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// handleHealth reports process liveness.
func (s *Server) handleHealth(w nethttp.ResponseWriter, _ *nethttp.Request) {
	writeJSON(w, s.logger, nethttp.StatusOK, healthResponse{Status: "ok", Version: s.version})
}
