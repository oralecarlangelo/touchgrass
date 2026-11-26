package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/objectstore"
)

const (
	objectDownload = "download"
	objectUpload   = "upload"
	objectFolder   = "folder"
	objectDelete   = "delete"
)

func (s *Server) handleBuckets(w nethttp.ResponseWriter, _ *nethttp.Request) {
	buckets := []string{}
	writable := false

	if s.objects != nil {
		buckets = s.objects.Buckets()
		writable = s.objects.Writable()
	}

	writeJSON(w, s.logger, nethttp.StatusOK, map[string]any{"configured": s.objects != nil, "buckets": buckets, "writable": writable})
}

func (s *Server) objectConnection(w nethttp.ResponseWriter, bucket string) bool {
	if s.objects == nil {
		nethttp.Error(w, "object storage is not configured", nethttp.StatusServiceUnavailable)
		return false
	}

	if !s.objects.Allowed(bucket) {
		nethttp.Error(w, "bucket is not allowed", nethttp.StatusForbidden)
		return false
	}

	return true
}

func (s *Server) handleObjects(w nethttp.ResponseWriter, r *nethttp.Request) {
	q := r.URL.Query()
	if !s.objectConnection(w, q.Get("bucket")) {
		return
	}

	page, err := s.objects.List(r.Context(), q.Get("bucket"), q.Get("prefix"), q.Get("after"))
	if err != nil {
		writeServiceError(w, s.logger, err, "could not list bucket objects")
		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, page)
}

type objectAction struct {
	Action       string `json:"action"`
	Bucket       string `json:"bucket"`
	Key          string `json:"key"`
	Filename     string `json:"filename"`
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleObjectAction(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body objectAction

	r.Body = nethttp.MaxBytesReader(w, r.Body, 16*1024)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		nethttp.Error(w, "invalid request", nethttp.StatusBadRequest)
		return
	}

	if !s.objectConnection(w, body.Bucket) {
		return
	}

	if !s.validObjectAction(w, body) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	// Fail closed if the audit intent cannot be persisted before issuing a grant or mutation.
	if err := s.recordObjectAction(ctx, r, body, "requested", model.AuditSuccess); err != nil {
		writeServiceError(w, s.logger, err, "could not audit storage action")
		return
	}

	result, err := s.executeObjectAction(ctx, body)
	phase := "completed"

	if grant, ok := result.(objectstore.Upload); ok {
		body.Key = grant.Key
		phase = "grant_issued"
	}

	if body.Action == objectDownload {
		phase = "grant_issued"
	}

	outcome := model.AuditSuccess
	if err != nil {
		outcome = model.AuditFailure
	}

	if auditErr := s.recordObjectAction(ctx, r, body, phase, outcome); auditErr != nil {
		s.logger.Error("storage result audit failed", "error", auditErr)
	}

	if err != nil {
		writeServiceError(w, s.logger, err, "storage action failed")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, s.logger, nethttp.StatusOK, result)
}

func (s *Server) validObjectAction(w nethttp.ResponseWriter, body objectAction) bool {
	valid := body.Action == objectDownload || body.Action == objectUpload || body.Action == objectFolder || body.Action == objectDelete
	if !valid {
		nethttp.Error(w, "unknown action", nethttp.StatusBadRequest)
		return false
	}

	if body.Action != objectDownload && !s.objects.Writable() {
		nethttp.Error(w, "object writes disabled", nethttp.StatusForbidden)
		return false
	}

	if body.Action == objectDelete && (body.Key == "" || body.Confirmation != body.Key) {
		nethttp.Error(w, "confirm the exact object key", nethttp.StatusBadRequest)
		return false
	}

	return true
}

func (s *Server) executeObjectAction(ctx context.Context, body objectAction) (any, error) {
	switch body.Action {
	case objectDownload:
		link, err := s.objects.Download(ctx, body.Bucket, body.Key)
		return map[string]string{"url": link}, err
	case objectUpload:
		return s.objects.PrepareUpload(ctx, body.Bucket, body.Key, body.Filename)
	case objectFolder:
		return map[string]bool{"ok": true}, s.objects.CreateFolder(ctx, body.Bucket, body.Key)
	case objectDelete:
		return map[string]bool{"ok": true}, s.objects.Delete(ctx, body.Bucket, body.Key)
	default:
		return nil, errors.New("unknown storage action")
	}
}

func (s *Server) recordObjectAction(ctx context.Context, r *nethttp.Request, body objectAction, phase, result string) error {
	identity, ok := identityFrom(r)
	if !ok {
		return errors.New("storage actor missing")
	}

	detail, err := json.Marshal(map[string]string{"action": body.Action, "bucket": body.Bucket, "key": body.Key, "phase": phase})
	if err != nil {
		return fmt.Errorf("encoding storage audit: %w", err)
	}

	if _, err := s.audit.Record(ctx, model.AuditRecord{Actor: identity.Username, Action: model.AuditStorageAction, Result: result, Detail: string(detail)}); err != nil {
		return fmt.Errorf("recording storage audit: %w", err)
	}

	return nil
}
