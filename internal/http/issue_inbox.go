package http

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const issueInboxLimit = 25

var errIssueInboxFilter = errors.New("invalid issue inbox filter")

type issueInboxResponse struct {
	Issues     []model.Issue `json:"issues"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

func (s *Server) handleIssueInbox(w nethttp.ResponseWriter, r *nethttp.Request) {
	filter, err := issueInboxQuery(r)
	if err != nil {
		writeError(w, s.logger, err, "invalid issue inbox filter", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	issues, next, err := s.ingestor.IssueInbox(r.Context(), filter)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load issue inbox")

		return
	}

	response := issueInboxResponse{Issues: issues}

	if next.ID > 0 {
		encoded, encodeErr := json.Marshal(next)
		if encodeErr != nil {
			writeServiceError(w, s.logger, encodeErr, "failed to encode issue cursor")

			return
		}

		response.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
	}

	writeJSON(w, s.logger, nethttp.StatusOK, response)
}

func issueInboxQuery(r *nethttp.Request) (model.IssueInboxQuery, error) {
	values := r.URL.Query()
	all, serviceIDs := listScope(r, authz.IssuesRead)

	if serviceID := values.Get(serviceIDParam); serviceID != "" {
		// The route guard has already verified this named service.
		all = false
		serviceIDs = []string{serviceID}
	}

	filter := model.IssueInboxQuery{
		AllServices: all, ServiceIDs: serviceIDs, Limit: issueInboxLimit,
		Search: strings.TrimSpace(values.Get("q")), Release: strings.TrimSpace(values.Get("release")),
	}

	if len(filter.Search) > 200 || len(filter.Release) > 200 {
		return model.IssueInboxQuery{}, errIssueInboxFilter
	}

	start, err := issueInboxDate(values.Get("start"))
	if err != nil {
		return model.IssueInboxQuery{}, err
	}

	end, err := issueInboxDate(values.Get("end"))
	if err != nil {
		return model.IssueInboxQuery{}, err
	}

	if !start.IsZero() {
		filter.Start = start.UTC().Format(time.RFC3339Nano)
	}

	if !end.IsZero() {
		filter.End = end.UTC().Format(time.RFC3339Nano)
	}

	if !start.IsZero() && !end.IsZero() && end.Before(start) {
		return model.IssueInboxQuery{}, errIssueInboxFilter
	}

	filter.Before, err = decodeIssueCursor(values.Get("cursor"))

	return filter, err
}

func issueInboxDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}

	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing issue window: %w", errIssueInboxFilter)
	}

	return parsed, nil
}

func decodeIssueCursor(value string) (model.IssueCursor, error) {
	var cursor model.IssueCursor

	if value == "" {
		return cursor, nil
	}

	if len(value) > 200 {
		return cursor, errIssueInboxFilter
	}

	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, errIssueInboxFilter
	}

	if err := json.Unmarshal(encoded, &cursor); err != nil || cursor.ID <= 0 || cursor.LastSeen == "" {
		return model.IssueCursor{}, errIssueInboxFilter
	}

	if _, err := issueInboxDate(cursor.LastSeen); err != nil {
		return model.IssueCursor{}, err
	}

	return cursor, nil
}
