package http

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"strconv"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// errInvalidRunID reports a run id that is not a positive integer.
var errInvalidRunID = errors.New("http: invalid migration run id")

// migrationConfigResponse is the migration setup of a service. The
// database URL is never part of it.
type migrationConfigResponse struct {
	Configured bool                   `json:"configured"`
	Tools      []model.MigrationTool  `json:"tools"`
	Config     *model.MigrationConfig `json:"config"`
}

// migrationCommandRequest is the body of deploy and seed.
type migrationCommandRequest struct {
	Confirm string `json:"confirm"`
}

// migrationResolveRequest is the body of resolve.
type migrationResolveRequest struct {
	Confirm    string `json:"confirm"`
	Migration  string `json:"migration"`
	Resolution string `json:"resolution"`
}

// handleMigrations returns the migration status of one service.
func (s *Server) handleMigrations(w nethttp.ResponseWriter, r *nethttp.Request) {
	status, err := s.migrations.Status(r.Context(), r.PathValue("id"))
	if err != nil {
		writeMigrationError(w, s.logger, err, "failed to read migration status")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, status)
}

// handleMigrationRuns lists the commands run for one service.
func (s *Server) handleMigrationRuns(w nethttp.ResponseWriter, r *nethttp.Request) {
	runs, err := s.migrations.Runs(r.Context(), r.PathValue("id"))
	if err != nil {
		writeMigrationError(w, s.logger, err, "failed to list migration runs")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, runs)
}

// handleMigrationRun returns one command with its output.
func (s *Server) handleMigrationRun(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(r.PathValue("run_id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, s.logger, errInvalidRunID, "invalid run id", invalidRequestCode, nethttp.StatusBadRequest)

		return
	}

	run, err := s.migrations.Run(r.Context(), r.PathValue("id"), id)
	if err != nil {
		writeMigrationError(w, s.logger, err, "failed to load migration run")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, run)
}

// handleMigrationConfig returns the migration setup of one service.
func (s *Server) handleMigrationConfig(w nethttp.ResponseWriter, r *nethttp.Request) {
	config, found, err := s.migrations.Config(r.Context(), r.PathValue("id"))
	if err != nil {
		writeMigrationError(w, s.logger, err, "failed to load migration config")

		return
	}

	response := migrationConfigResponse{Configured: found, Tools: s.migrations.Tools()}
	if found {
		response.Config = &config
	}

	writeJSON(w, s.logger, nethttp.StatusOK, response)
}

// handlePutMigrationConfig creates or replaces the migration setup.
func (s *Server) handlePutMigrationConfig(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[model.MigrationConfigInput](w, s.logger, r)
	if !ok {
		return
	}

	config, err := s.migrations.SaveConfig(r.Context(), r.PathValue("id"), body, requestActor(r))
	if err != nil {
		writeMigrationError(w, s.logger, err, "failed to save migration config")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, migrationConfigResponse{
		Configured: true,
		Tools:      s.migrations.Tools(),
		Config:     &config,
	})
}

// handleDeleteMigrationConfig removes the migration setup.
func (s *Server) handleDeleteMigrationConfig(w nethttp.ResponseWriter, r *nethttp.Request) {
	if err := s.migrations.DeleteConfig(r.Context(), r.PathValue("id"), requestActor(r)); err != nil {
		writeMigrationError(w, s.logger, err, "failed to delete migration config")

		return
	}

	w.WriteHeader(nethttp.StatusNoContent)
}

// handleMigrationDeploy applies the pending migrations.
func (s *Server) handleMigrationDeploy(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[migrationCommandRequest](w, s.logger, r)
	if !ok {
		return
	}

	s.startMigration(w, r, service.MigrationCommand{Kind: model.MigrationRunDeploy, Confirm: body.Confirm})
}

// handleMigrationSeed runs the seed the build declares.
func (s *Server) handleMigrationSeed(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[migrationCommandRequest](w, s.logger, r)
	if !ok {
		return
	}

	s.startMigration(w, r, service.MigrationCommand{Kind: model.MigrationRunSeed, Confirm: body.Confirm})
}

// handleMigrationResolve marks a failed migration applied or rolled back.
func (s *Server) handleMigrationResolve(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[migrationResolveRequest](w, s.logger, r)
	if !ok {
		return
	}

	s.startMigration(w, r, service.MigrationCommand{
		Kind:       model.MigrationRunResolve,
		Migration:  body.Migration,
		Resolution: body.Resolution,
		Confirm:    body.Confirm,
	})
}

// startMigration starts a command and answers 202 with its run. The
// command outlives the request, so it runs under a detached context.
func (s *Server) startMigration(w nethttp.ResponseWriter, r *nethttp.Request, command service.MigrationCommand) {
	run, err := s.migrations.Start(
		context.WithoutCancel(r.Context()),
		r.PathValue("id"),
		command,
		requestActor(r),
	)
	if err != nil {
		writeMigrationError(w, s.logger, err, "failed to start migration command")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, run)
}

// writeMigrationError maps migration errors to contract codes before
// the shared service mapping.
func writeMigrationError(w nethttp.ResponseWriter, logger *slog.Logger, err error, message string) {
	switch {
	case errors.Is(err, service.ErrMigrationsDisabled):
		writeError(w, logger, err, "the env encryption key is not configured, so a database url cannot be stored",
			"env_key_missing", nethttp.StatusServiceUnavailable)
	case errors.Is(err, service.ErrMigrationsNotConfigured):
		writeError(w, logger, err, "migrations are not configured for this service",
			"migrations_not_configured", nethttp.StatusBadRequest)
	case errors.Is(err, service.ErrMigrationConfirmation):
		writeError(w, logger, err, "confirmation must equal the service name",
			"confirmation_mismatch", nethttp.StatusBadRequest)
	case errors.Is(err, service.ErrMigrationBusy):
		writeError(w, logger, err, "a migration command is already running for this service",
			"migration_in_progress", nethttp.StatusConflict)
	case errors.Is(err, service.ErrMigrationContainer):
		writeError(w, logger, err, "no running container was found for this service",
			"container_not_found", nethttp.StatusConflict)
	case errors.Is(err, docker.ErrDaemonBackoff):
		writeError(w, logger, err, "the container engine is not answering; try again in a minute",
			"engine_unavailable", nethttp.StatusServiceUnavailable)
	case errors.Is(err, store.ErrMigrationRunNotFound):
		writeError(w, logger, err, "unknown migration run", "not_found", nethttp.StatusNotFound)
	default:
		writeServiceError(w, logger, err, message)
	}
}
