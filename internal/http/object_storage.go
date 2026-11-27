package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"slices"
	"strconv"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/objectstore"
)

const (
	storageBucketsKey = "buckets"
	objectDownload    = "download"
	objectUpload      = "upload"
	objectFolder      = "folder"
	objectDelete      = "delete"
)

type objectConnectionRequest struct {
	Endpoint  string   `json:"endpoint"`
	Region    string   `json:"region"`
	AccessKey string   `json:"access_key"`
	SecretKey string   `json:"secret_key"`
	Buckets   []string `json:"buckets"`
	Writable  bool     `json:"writable"`
}

func (request objectConnectionRequest) config() objectstore.Config {
	return objectstore.Config{
		Endpoint: request.Endpoint, Region: request.Region, AccessKey: request.AccessKey,
		SecretKey: request.SecretKey, Buckets: request.Buckets, Writable: request.Writable,
	}
}

func decodeObjectConnection(w nethttp.ResponseWriter, r *nethttp.Request) (objectConnectionRequest, bool) {
	var request objectConnectionRequest

	r.Body = nethttp.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&request); err != nil {
		nethttp.Error(w, "invalid request", nethttp.StatusBadRequest)
		return request, false
	}

	return request, true
}

func (s *Server) handleDiscoverBuckets(w nethttp.ResponseWriter, r *nethttp.Request) {
	request, ok := decodeObjectConnection(w, r)
	if !ok {
		return
	}

	if err := objectstore.ValidateConfig(request.config(), false); err != nil {
		nethttp.Error(w, "invalid object storage connection", nethttp.StatusBadRequest)
		return
	}

	buckets, err := objectstore.DiscoverBuckets(r.Context(), request.config())
	if err != nil {
		nethttp.Error(w, "object storage is unavailable", nethttp.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, s.logger, nethttp.StatusOK, map[string]any{storageBucketsKey: buckets})
}

func (s *Server) handleSaveObjectConnection(w nethttp.ResponseWriter, r *nethttp.Request) {
	request, ok := decodeObjectConnection(w, r)
	if !ok {
		return
	}

	if s.objectStorePath == "" {
		nethttp.Error(w, "object storage setup is unavailable", nethttp.StatusServiceUnavailable)
		return
	}

	if err := objectstore.ValidateConfig(request.config(), true); err != nil {
		nethttp.Error(w, "invalid object storage connection", nethttp.StatusBadRequest)
		return
	}

	setupCtx, cancelSetup := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancelSetup()

	discovered, err := objectstore.DiscoverBuckets(setupCtx, request.config())
	if err != nil {
		if checkErr := objectstore.CheckBuckets(setupCtx, request.config()); checkErr != nil {
			nethttp.Error(w, "object storage is unavailable", nethttp.StatusServiceUnavailable)
			return
		}

		discovered = request.Buckets
	}

	if len(request.Buckets) == 0 || !bucketSubset(request.Buckets, discovered) {
		nethttp.Error(w, "select buckets returned by discovery", nethttp.StatusBadRequest)
		return
	}

	if err := r.Context().Err(); err != nil {
		nethttp.Error(w, "request cancelled", nethttp.StatusRequestTimeout)
		return
	}

	s.objectSetupMu.Lock()
	if err := r.Context().Err(); err != nil {
		s.objectSetupMu.Unlock()
		nethttp.Error(w, "request cancelled", nethttp.StatusRequestTimeout)

		return
	}

	if err := objectstore.Save(s.objectStorePath, request.config()); err != nil {
		s.objectSetupMu.Unlock()
		writeServiceError(w, s.logger, err, "could not save object storage connection")

		return
	}

	objects, err := objectstore.Open(s.objectStorePath)
	if err != nil {
		s.objectSetupMu.Unlock()
		writeServiceError(w, s.logger, err, "could not load object storage connection")

		return
	}

	s.objectsMu.Lock()
	s.objects = objects
	s.objectsMu.Unlock()
	s.objectSetupMu.Unlock()

	s.auditObjectConnection(r, objects)

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, s.logger, nethttp.StatusOK, map[string]any{
		"configured": true, storageBucketsKey: objects.Buckets(), "writable": objects.Writable(),
	})
}

func (s *Server) auditObjectConnection(r *nethttp.Request, objects *objectstore.Client) {
	identity, identityOK := identityFrom(r)
	if identityOK {
		detail, marshalErr := json.Marshal(map[string]any{
			"action": "configure", storageBucketsKey: objects.Buckets(), "writable": objects.Writable(),
		})
		if marshalErr == nil {
			_, auditErr := s.audit.Record(r.Context(), model.AuditRecord{
				Actor: identity.Username, Action: model.AuditStorageAction,
				Result: model.AuditSuccess, Detail: string(detail),
			})
			if auditErr != nil {
				s.logger.Error("storage connection audit failed", "error", auditErr)
			}
		}
	}
}

func bucketSubset(selected, discovered []string) bool {
	seen := make(map[string]struct{}, len(selected))
	for _, bucket := range selected {
		if bucket == "" {
			return false
		}

		if _, duplicate := seen[bucket]; duplicate {
			return false
		}

		seen[bucket] = struct{}{}
		if !slices.Contains(discovered, bucket) {
			return false
		}
	}

	return true
}

func (s *Server) handleBuckets(w nethttp.ResponseWriter, _ *nethttp.Request) {
	buckets := []string{}
	writable := false
	objects := s.objectClient()

	if objects != nil {
		buckets = objects.Buckets()
		writable = objects.Writable()
	}

	writeJSON(w, s.logger, nethttp.StatusOK, map[string]any{"configured": objects != nil, storageBucketsKey: buckets, "writable": writable})
}

func (s *Server) objectClient() *objectstore.Client {
	s.objectsMu.RLock()
	defer s.objectsMu.RUnlock()

	return s.objects
}

func (s *Server) objectConnection(w nethttp.ResponseWriter, bucket string) *objectstore.Client {
	objects := s.objectClient()
	if objects == nil {
		nethttp.Error(w, "object storage is not configured", nethttp.StatusServiceUnavailable)
		return nil
	}

	if !objects.Allowed(bucket) {
		nethttp.Error(w, "bucket is not allowed", nethttp.StatusForbidden)
		return nil
	}

	return objects
}

func (s *Server) handleObjects(w nethttp.ResponseWriter, r *nethttp.Request) {
	q := r.URL.Query()

	objects := s.objectConnection(w, q.Get("bucket"))
	if objects == nil {
		return
	}

	page, err := objects.List(r.Context(), q.Get("bucket"), q.Get("prefix"), q.Get("after"))
	if err != nil {
		writeServiceError(w, s.logger, err, "could not list bucket objects")
		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, page)
}

func (s *Server) handleObjectContent(w nethttp.ResponseWriter, r *nethttp.Request) {
	bucket, key := r.URL.Query().Get("bucket"), r.URL.Query().Get("key")
	if key == "" {
		nethttp.Error(w, "object key is required", nethttp.StatusBadRequest)
		return
	}

	objects := s.objectConnection(w, bucket)
	if objects == nil || !s.acquireObjectTransfer(w) {
		return
	}

	defer func() { <-s.objectTransfers }()

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	if err := s.recordObjectAction(ctx, r, objectAction{Action: objectDownload, Bucket: bucket, Key: key}, "requested", model.AuditSuccess); err != nil {
		writeServiceError(w, s.logger, err, "could not audit storage action")
		return
	}

	controller := nethttp.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(2 * time.Minute)); err != nil {
		s.logger.Debug("object download deadline unavailable", "error", err)
	}

	stream, size, err := objects.Read(ctx, bucket, key)
	if err != nil {
		if auditErr := s.recordObjectAction(ctx, r, objectAction{Action: objectDownload, Bucket: bucket, Key: key}, "failed", model.AuditFailure); auditErr != nil {
			s.logger.Error("storage result audit failed", "error", auditErr)
		}

		nethttp.Error(w, "object unavailable", nethttp.StatusServiceUnavailable)

		return
	}

	defer func() { _ = stream.Close() }()

	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(key)}))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-store")

	if _, err := io.Copy(w, stream); err != nil {
		s.logger.Warn("object download interrupted", "error", err)

		if auditErr := s.recordObjectAction(ctx, r, objectAction{Action: objectDownload, Bucket: bucket, Key: key}, "failed", model.AuditFailure); auditErr != nil {
			s.logger.Error("storage result audit failed", "error", auditErr)
		}

		return
	}

	if err := s.recordObjectAction(ctx, r, objectAction{Action: objectDownload, Bucket: bucket, Key: key}, "completed", model.AuditSuccess); err != nil {
		s.logger.Error("storage result audit failed", "error", err)
	}
}

func (s *Server) handleObjectUpload(w nethttp.ResponseWriter, r *nethttp.Request) {
	query := r.URL.Query()
	bucket := query.Get("bucket")

	objects := s.objectConnection(w, bucket)
	if objects == nil || !s.acquireObjectTransfer(w) {
		return
	}

	defer func() { <-s.objectTransfers }()

	if !objects.Writable() {
		nethttp.Error(w, "object writes disabled", nethttp.StatusForbidden)
		return
	}

	if r.ContentLength < 1 || r.ContentLength > 100*1024*1024 {
		nethttp.Error(w, "invalid upload", nethttp.StatusBadRequest)
		return
	}

	controller := nethttp.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(2 * time.Minute)); err != nil {
		s.logger.Debug("object upload deadline unavailable", "error", err)
	}

	if err := controller.SetWriteDeadline(time.Now().Add(2 * time.Minute)); err != nil {
		s.logger.Debug("object upload response deadline unavailable", "error", err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	intent := objectAction{Action: objectUpload, Bucket: bucket, Key: query.Get("prefix"), Filename: query.Get("filename")}
	if err := s.recordObjectAction(ctx, r, intent, "requested", model.AuditSuccess); err != nil {
		writeServiceError(w, s.logger, err, "could not audit storage action")
		return
	}

	r.Body = nethttp.MaxBytesReader(w, r.Body, 100*1024*1024)

	key, err := objects.Write(ctx, bucket, query.Get("prefix"), query.Get("filename"), r.Body, r.ContentLength)
	if err != nil {
		if auditErr := s.recordObjectAction(ctx, r, intent, "failed", model.AuditFailure); auditErr != nil {
			s.logger.Error("storage result audit failed", "error", auditErr)
		}

		nethttp.Error(w, "upload unavailable", nethttp.StatusServiceUnavailable)

		return
	}

	intent.Key = key
	if err := s.recordObjectAction(ctx, r, intent, "completed", model.AuditSuccess); err != nil {
		s.logger.Error("storage result audit failed", "error", err)
	}

	writeJSON(w, s.logger, nethttp.StatusCreated, map[string]string{"key": key})
}

func (s *Server) acquireObjectTransfer(w nethttp.ResponseWriter) bool {
	select {
	case s.objectTransfers <- struct{}{}:
		return true
	default:
		nethttp.Error(w, "storage transfers are busy", nethttp.StatusServiceUnavailable)
		return false
	}
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

	objects := s.objectConnection(w, body.Bucket)
	if objects == nil {
		return
	}

	if !s.validObjectAction(w, objects, body) {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	// Fail closed if the audit intent cannot be persisted before issuing a grant or mutation.
	if err := s.recordObjectAction(ctx, r, body, "requested", model.AuditSuccess); err != nil {
		writeServiceError(w, s.logger, err, "could not audit storage action")
		return
	}

	result, err := s.executeObjectAction(ctx, objects, body)
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

func (s *Server) validObjectAction(w nethttp.ResponseWriter, objects *objectstore.Client, body objectAction) bool {
	valid := body.Action == objectDownload || body.Action == objectUpload || body.Action == objectFolder || body.Action == objectDelete
	if !valid {
		nethttp.Error(w, "unknown action", nethttp.StatusBadRequest)
		return false
	}

	if body.Action != objectDownload && !objects.Writable() {
		nethttp.Error(w, "object writes disabled", nethttp.StatusForbidden)
		return false
	}

	if body.Action == objectDelete && (body.Key == "" || body.Confirmation != body.Key) {
		nethttp.Error(w, "confirm the exact object key", nethttp.StatusBadRequest)
		return false
	}

	return true
}

func (s *Server) executeObjectAction(ctx context.Context, objects *objectstore.Client, body objectAction) (any, error) {
	switch body.Action {
	case objectDownload:
		query := url.Values{"bucket": {body.Bucket}, "key": {body.Key}}
		return map[string]string{"url": "/api/storage/content?" + query.Encode()}, nil
	case objectUpload:
		return objects.PrepareUpload(ctx, body.Bucket, body.Key, body.Filename)
	case objectFolder:
		return map[string]bool{"ok": true}, objects.CreateFolder(ctx, body.Bucket, body.Key)
	case objectDelete:
		return map[string]bool{"ok": true}, objects.Delete(ctx, body.Bucket, body.Key)
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
