package http

import (
	"errors"
	"strconv"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

type routesResponse struct {
	Routes []service.PerformanceRoute `json:"routes"`
}

type postgresResponse struct {
	Metrics []model.PostgresMetricBucket `json:"metrics"`
}

type redisResponse struct {
	Metrics []model.RedisMetricBucket `json:"metrics"`
}

type collectorsResponse struct {
	Collectors []model.CollectorStatus `json:"collectors"`
}

func (s *Server) handlePerformanceSummary(w nethttp.ResponseWriter, r *nethttp.Request) {
	query, ok := s.performanceQuery(w, r)
	if !ok {
		return
	}
	result, err := s.performance.Summary(r.Context(), query)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load performance summary")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, result)
}

func (s *Server) handlePerformanceTimeline(w nethttp.ResponseWriter, r *nethttp.Request) {
	query, ok := s.performanceQuery(w, r)
	if !ok {
		return
	}
	result, err := s.performance.Timeline(r.Context(), query)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load performance timeline")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, result)
}

func (s *Server) handlePerformanceRoutes(w nethttp.ResponseWriter, r *nethttp.Request) {
	query, ok := s.performanceQuery(w, r)
	if !ok {
		return
	}
	result, err := s.performance.Routes(r.Context(), query)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load performance routes")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, routesResponse{Routes: result})
}

func (s *Server) handlePerformancePostgres(w nethttp.ResponseWriter, r *nethttp.Request) {
	query, ok := s.performanceQuery(w, r)
	if !ok {
		return
	}
	result, err := s.performance.Postgres(r.Context(), query)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load postgres performance")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, postgresResponse{Metrics: result})
}

func (s *Server) handlePerformanceRedis(w nethttp.ResponseWriter, r *nethttp.Request) {
	query, ok := s.performanceQuery(w, r)
	if !ok {
		return
	}
	result, err := s.performance.Redis(r.Context(), query)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load redis performance")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, redisResponse{Metrics: result})
}

func (s *Server) handleCollectorStatus(w nethttp.ResponseWriter, r *nethttp.Request) {
	result, err := s.performance.CollectorStatus(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load collector status")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, collectorsResponse{Collectors: result})
}

func (s *Server) performanceQuery(w nethttp.ResponseWriter, r *nethttp.Request) (model.PerformanceQuery, bool) {
	values := r.URL.Query()
	query := model.PerformanceQuery{
		ServiceID:   r.PathValue("id"),
		Resolution:  values.Get("resolution"),
		RouteFamily: values.Get("route_family"),
		Sort:        values.Get("sort"),
	}
	if query.ServiceID == "" {
		query.ServiceID = values.Get("service_id")
	}

	var err error
	query.Start, err = parsePerformanceTime(values.Get("start"))
	if err == nil {
		query.End, err = parsePerformanceTime(values.Get("end"))
	}
	if err == nil {
		query.Limit, err = parsePerformanceLimit(values.Get("limit"))
	}
	if err != nil {
		writeError(w, s.logger, err, err.Error(), "invalid_request", nethttp.StatusBadRequest)
		return model.PerformanceQuery{}, false
	}
	return query, true
}

func parsePerformanceTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.New("invalid timestamp (want RFC3339)")
	}
	return value.UTC(), nil
}

func parsePerformanceLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 100 {
		return 0, errors.New("invalid limit (want 1-100)")
	}
	return value, nil
}
