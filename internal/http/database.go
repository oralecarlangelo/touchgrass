package http

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// databasesResponse is the databases payload.
type databasesResponse struct {
	Databases []model.Database `json:"databases"`
}

// handleDatabases lists postgres, redis, and sqlite health.
func (s *Server) handleDatabases(w nethttp.ResponseWriter, r *nethttp.Request) {
	databases, err := s.database.Databases(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load databases")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, databasesResponse{Databases: databases})
}

// backupsResponse is the backup list payload.
type backupsResponse struct {
	Backups []model.BackupInfo `json:"backups"`
}

// handleBackups lists pg_dump artifacts newest first.
func (s *Server) handleBackups(w nethttp.ResponseWriter, _ *nethttp.Request) {
	backups, err := s.database.Backups()
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load backups")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, backupsResponse{Backups: backups})
}

// jobAcceptedResponse reports an async job start.
type jobAcceptedResponse struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

// handleStartBackup begins an async pg_dump backup.
func (s *Server) handleStartBackup(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := s.database.StartBackup(context.WithoutCancel(r.Context()), actorAdmin)
	if err != nil {
		writeDatabaseError(w, s.logger, err, "failed to start backup")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, jobAcceptedResponse{ID: id, Status: model.DBJobRunning})
}

// jobsResponse is the job history payload.
type jobsResponse struct {
	Jobs []model.DBJob `json:"jobs"`
}

// handleJobs lists database jobs newest first.
func (s *Server) handleJobs(w nethttp.ResponseWriter, r *nethttp.Request) {
	jobs, err := s.database.Jobs(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load database jobs")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, jobsResponse{Jobs: jobs})
}

// handleJob returns one database job by id.
func (s *Server) handleJob(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, s.logger, err, "invalid job id", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	job, err := s.database.Job(r.Context(), id)
	if err != nil {
		writeDatabaseError(w, s.logger, err, "failed to load database job")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, job)
}

// handleVerifyBackup checks one backup archive.
func (s *Server) handleVerifyBackup(w nethttp.ResponseWriter, r *nethttp.Request) {
	verified, err := s.database.Verify(r.Context(), r.PathValue("name"))
	if err != nil {
		writeDatabaseError(w, s.logger, err, "failed to verify backup")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, verified)
}

// restoreRequest is the restore body.
type restoreRequest struct {
	Name string `json:"name"`
}

// handleRestore begins an async restore of one backup.
func (s *Server) handleRestore(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[restoreRequest](w, s.logger, r)
	if !ok {
		return
	}

	id, err := s.database.StartRestore(context.WithoutCancel(r.Context()), body.Name, actorAdmin)
	if err != nil {
		writeDatabaseError(w, s.logger, err, "failed to start restore")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, jobAcceptedResponse{ID: id, Status: model.DBJobRunning})
}

// writeDatabaseError maps database errors to status codes before the
// shared service mapping: 503 unconfigured, 409 busy, 404 unknown job.
func writeDatabaseError(
	w nethttp.ResponseWriter,
	logger *slog.Logger,
	err error,
	message string,
) {
	switch {
	case errors.Is(err, service.ErrDatabaseUnconfigured):
		writeError(w, logger, err, "postgres is not configured", "unconfigured", nethttp.StatusServiceUnavailable)
	case errors.Is(err, service.ErrDatabaseBusy):
		writeError(w, logger, err, "a database job is already running", "busy", nethttp.StatusConflict)
	case errors.Is(err, store.ErrDBJobNotFound):
		writeError(w, logger, err, "unknown database job", "not_found", nethttp.StatusNotFound)
	default:
		writeServiceError(w, logger, err, message)
	}
}
