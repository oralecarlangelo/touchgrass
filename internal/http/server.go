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
	}
	mux.HandleFunc("GET /api/health", server.handleHealth)
	mux.HandleFunc("GET /api/services", server.handleServices)
	mux.HandleFunc("POST /api/services", server.requireAdmin(server.handleCreateService))
	mux.HandleFunc("DELETE /api/services/{id}", server.requireAdmin(server.handleDeleteService))
	mux.HandleFunc("GET /api/onboarding/suggest", server.handleOnboardingSuggest)
	mux.HandleFunc("GET /api/services/{id}/metrics", server.handleMetrics)
	mux.HandleFunc("GET /api/services/{id}/deploys", server.handleDeploys)
	mux.HandleFunc("POST /api/services/{id}/deploys", server.requireAdmin(server.handleRecordDeploy))
	mux.HandleFunc("POST /api/services/{id}/cutover", server.requireAdmin(server.handleCutover))
	mux.HandleFunc("POST /api/services/{id}/rollback", server.requireAdmin(server.handleRollback))
	mux.HandleFunc("POST /api/services/{id}/deploy", server.requireAdmin(server.handleDeploy))
	mux.HandleFunc("GET /api/alerts/rules", server.handleRules)
	mux.HandleFunc("POST /api/alerts/rules", server.requireAdmin(server.handleCreateRule))
	mux.HandleFunc("DELETE /api/alerts/rules/{id}", server.requireAdmin(server.handleDeleteRule))
	mux.HandleFunc("GET /api/notifications", server.handleNotifications)
	mux.HandleFunc("POST /api/notifications/{id}/read", server.requireAdmin(server.handleMarkNotificationRead))
	mux.HandleFunc("POST /api/notifications/read", server.requireAdmin(server.handleMarkNotificationsRead))
	mux.HandleFunc("GET /api/audit", server.handleAudit)
	mux.HandleFunc("GET /api/events", server.handleEvents)
	mux.HandleFunc("POST /api/auth/login", server.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", server.handleLogout)
	mux.HandleFunc("GET /api/auth/me", server.handleMe)
	mux.HandleFunc("GET /api/users", server.requireAdmin(server.handleUsers))
	mux.HandleFunc("POST /api/users", server.requireAdmin(server.handleCreateUser))
	mux.HandleFunc("POST /api/users/{id}/password", server.requireAdmin(server.handleUserPassword))
	mux.HandleFunc("POST /api/users/{id}/role", server.requireAdmin(server.handleUserRole))
	mux.HandleFunc("POST /api/users/{id}/disable", server.requireAdmin(server.handleUserDisable))
	mux.HandleFunc("POST /api/users/{id}/enable", server.requireAdmin(server.handleUserEnable))
	mux.HandleFunc("DELETE /api/users/{id}", server.requireAdmin(server.handleDeleteUser))
	mux.HandleFunc("POST /api/ingest", server.handleIngest)
	mux.HandleFunc("POST /api/ingest/logs", server.handleIngestLogs)
	mux.HandleFunc("POST /api/keys", server.requireAdmin(server.handleCreateKey))
	mux.HandleFunc("GET /api/keys", server.handleKeys)
	mux.HandleFunc("POST /api/keys/{id}/revoke", server.requireAdmin(server.handleRevokeKey))
	mux.HandleFunc("GET /api/issues", server.handleIssues)
	mux.HandleFunc("GET /api/issues/occurrences", server.handleOccurrences)
	mux.HandleFunc("GET /api/issues/{id}", server.handleIssue)
	mux.HandleFunc("GET /api/issues/{id}/logs", server.handleIssueLogs)
	mux.HandleFunc("POST /api/issues/rules", server.requireAdmin(server.handleCreateIssueRule))
	mux.HandleFunc("GET /api/issues/rules", server.handleIssueRules)
	mux.HandleFunc("DELETE /api/issues/rules/{id}", server.requireAdmin(server.handleDeleteIssueRule))
	mux.HandleFunc("GET /api/logs", server.handleLogs)
	mux.HandleFunc("GET /api/logs/stats", server.handleLogStats)
	mux.HandleFunc("GET /api/logs/{id}", server.handleLogContext)
	mux.HandleFunc("GET /api/docker/images", server.handleDockerImages)
	mux.HandleFunc("POST /api/docker/images/prune", server.requireAdmin(server.handlePruneImages))
	mux.HandleFunc("GET /api/system", server.handleSystem)
	mux.HandleFunc("GET /api/system/containers", server.handleContainers)
	mux.HandleFunc("GET /api/system/containers/{id}", server.handleContainer)
	mux.HandleFunc("POST /api/system/containers/{id}/approve", server.requireAdmin(server.handleApproveContainer))
	mux.HandleFunc("POST /api/system/containers/{id}/ignore", server.requireAdmin(server.handleIgnoreContainer))
	mux.HandleFunc("GET /api/security/summary", server.handleSecuritySummary)
	mux.HandleFunc("GET /api/security/findings", server.handleSecurityFindings)
	mux.HandleFunc("GET /api/security/findings/{id}", server.handleSecurityFinding)
	mux.HandleFunc("GET /api/security/policies", server.handleSecurityPolicies)
	mux.HandleFunc("POST /api/security/findings/{id}/acknowledge", server.requireAdmin(server.handleSecurityAcknowledge))
	mux.HandleFunc("POST /api/security/findings/{id}/resolve", server.requireAdmin(server.handleSecurityResolve))
	mux.HandleFunc("POST /api/security/findings/{id}/suppress", server.requireAdmin(server.handleSecuritySuppress))
	mux.HandleFunc("POST /api/security/policies", server.requireAdmin(server.handleCreateSecurityPolicy))
	mux.HandleFunc("PATCH /api/security/policies/{id}", server.requireAdmin(server.handleUpdateSecurityPolicy))
	mux.HandleFunc("DELETE /api/security/policies/{id}", server.requireAdmin(server.handleDeleteSecurityPolicy))
	mux.HandleFunc("GET /api/system/history", server.handleSystemHistory)
	mux.HandleFunc("GET /api/databases", server.handleDatabases)
	mux.HandleFunc("GET /api/databases/backups", server.handleBackups)
	mux.HandleFunc("POST /api/databases/backups", server.requireAdmin(server.handleStartBackup))
	mux.HandleFunc("GET /api/databases/jobs", server.handleJobs)
	mux.HandleFunc("GET /api/databases/jobs/{id}", server.handleJob)
	mux.HandleFunc("POST /api/databases/backups/{name}/verify", server.requireAdmin(server.handleVerifyBackup))
	mux.HandleFunc("POST /api/databases/restore", server.requireAdmin(server.handleRestore))
	mux.HandleFunc("GET /api/performance/summary", server.handlePerformanceSummary)
	mux.HandleFunc("GET /api/performance/timeline", server.handlePerformanceTimeline)
	mux.HandleFunc("GET /api/performance/routes", server.handlePerformanceRoutes)
	mux.HandleFunc("GET /api/performance/postgres", server.handlePerformancePostgres)
	mux.HandleFunc("GET /api/performance/redis", server.handlePerformanceRedis)
	mux.HandleFunc("GET /api/collectors/status", server.handleCollectorStatus)
	mux.HandleFunc("GET /api/services/{id}/performance/summary", server.handlePerformanceSummary)
	mux.HandleFunc("GET /api/services/{id}/performance/timeline", server.handlePerformanceTimeline)
	mux.HandleFunc("GET /api/services/{id}/performance/routes", server.handlePerformanceRoutes)
	mux.HandleFunc("GET /api/services/{id}/performance/postgres", server.handlePerformancePostgres)
	mux.HandleFunc("GET /api/services/{id}/performance/redis", server.handlePerformanceRedis)

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

// healthResponse is the /api/health payload.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// handleHealth reports process liveness.
func (s *Server) handleHealth(w nethttp.ResponseWriter, _ *nethttp.Request) {
	writeJSON(w, s.logger, nethttp.StatusOK, healthResponse{Status: "ok", Version: s.version})
}
