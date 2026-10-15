// Package nginxlog parses the dedicated privacy-safe Nginx timing stream.
package nginxlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

var allowedRouteFamilies = map[string]string{
	"health": "health", "auth": "auth", "public_events": "public_events",
	"account_orders": "account_orders", "checkout": "checkout", "callbacks": "callbacks",
	"admin": "admin", "organization": "organization", "static": "static",
	// Ticketnation's producer uses this bounded label; store it under the
	// public contract's canonical static family.
	"static_assets": "static", "other": "other",
}

var allowedMethods = map[string]bool{
	"GET": true, "HEAD": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "OPTIONS": true,
}

type timingRecord struct {
	Timestamp          string `json:"timestamp"`
	Method             string `json:"method"`
	Status             string `json:"status"`
	RouteFamily        string `json:"route_family"`
	RequestTime        string `json:"request_time"`
	UpstreamConnect    string `json:"upstream_connect_time"`
	UpstreamHeader     string `json:"upstream_header_time"`
	UpstreamResponse   string `json:"upstream_response_time"`
	UpstreamStatus     string `json:"upstream_status"`
	UpstreamAddress    string `json:"upstream_addr"`
	BytesSent          string `json:"bytes_sent"`
	RequestLength      string `json:"request_length"`
	ConnectionRequests string `json:"connection_requests"`
}

// Parser decodes only the fields represented by timingRecord. Unknown JSON
// fields—including a mistakenly supplied raw URI—are ignored and never copied.
type Parser struct{}

// Parse validates one Nginx timing line and returns a bounded observation.
func (Parser) Parse(line []byte) (model.HTTPObservation, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	var record timingRecord
	if err := decoder.Decode(&record); err != nil {
		return model.HTTPObservation{}, errors.New("nginx timing record is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return model.HTTPObservation{}, errors.New("nginx timing record has trailing data")
	}

	timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return model.HTTPObservation{}, errors.New("nginx timing timestamp is invalid")
	}
	status, err := boundedInt(record.Status, 100, 599, "status")
	if err != nil {
		return model.HTTPObservation{}, err
	}
	upstreamStatus := 0
	if record.UpstreamStatus != "" && record.UpstreamStatus != "-" {
		upstreamStatus, err = firstStatus(record.UpstreamStatus)
		if err != nil {
			return model.HTTPObservation{}, err
		}
	}
	duration, err := timingMilliseconds(record.RequestTime, true)
	if err != nil {
		return model.HTTPObservation{}, fmt.Errorf("request timing: %w", err)
	}
	connect, err := timingMilliseconds(record.UpstreamConnect, false)
	if err != nil {
		return model.HTTPObservation{}, fmt.Errorf("upstream connect timing: %w", err)
	}
	header, err := timingMilliseconds(record.UpstreamHeader, false)
	if err != nil {
		return model.HTTPObservation{}, fmt.Errorf("upstream header timing: %w", err)
	}
	response, err := timingMilliseconds(record.UpstreamResponse, false)
	if err != nil {
		return model.HTTPObservation{}, fmt.Errorf("upstream response timing: %w", err)
	}
	bytesSent, err := boundedInt64(record.BytesSent, 0, 1<<50, "bytes sent")
	if err != nil {
		return model.HTTPObservation{}, err
	}
	requestBytes, err := boundedInt64(record.RequestLength, 0, 1<<40, "request bytes")
	if err != nil {
		return model.HTTPObservation{}, err
	}
	connectionRequests, err := boundedInt64(record.ConnectionRequests, 0, 1<<31, "connection requests")
	if err != nil {
		return model.HTTPObservation{}, err
	}

	method := strings.ToUpper(strings.TrimSpace(record.Method))
	if !allowedMethods[method] {
		method = "OTHER"
	}

	return model.HTTPObservation{
		Timestamp: timestamp.UTC(), Method: method, RouteFamily: normalizeRouteFamily(record.RouteFamily),
		Status: status, UpstreamStatus: upstreamStatus, DurationMS: duration,
		UpstreamConnectMS: connect, UpstreamHeaderMS: header, UpstreamResponseMS: response,
		BytesSent: bytesSent, RequestBytes: requestBytes, ConnectionRequests: connectionRequests,
		Upstream: normalizeUpstream(record.UpstreamAddress),
	}, nil
}

func normalizeRouteFamily(value string) string {
	if strings.ContainsFunc(value, unicode.IsControl) || strings.ContainsAny(value, "%?/#\\") {
		return "other"
	}
	if normalized, ok := allowedRouteFamilies[value]; ok {
		return normalized
	}
	return "other"
}

func normalizeUpstream(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" {
		return "none"
	}
	if len(value) > 64 || strings.ContainsAny(value, ",; ") || strings.ContainsFunc(value, unicode.IsControl) {
		return "multiple"
	}
	for _, char := range value {
		if !(unicode.IsLetter(char) || unicode.IsDigit(char) || strings.ContainsRune("._:-[]", char)) {
			return "other"
		}
	}
	return value
}

func timingMilliseconds(raw string, required bool) (float64, error) {
	if raw == "" || raw == "-" {
		if required {
			return 0, errors.New("timing is required")
		}
		return 0, nil
	}

	var total float64
	found := false
	for _, part := range strings.FieldsFunc(raw, func(char rune) bool {
		return char == ',' || char == ':' || unicode.IsSpace(char)
	}) {
		if part == "" || part == "-" {
			continue
		}
		seconds, err := strconv.ParseFloat(part, 64)
		if err != nil || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds > 3600 {
			return 0, errors.New("timing is invalid")
		}
		total += seconds * 1000
		found = true
	}
	if !found && required {
		return 0, errors.New("timing is required")
	}
	return total, nil
}

func boundedInt(raw string, minimum, maximum int, name string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return value, nil
}

func boundedInt64(raw string, minimum, maximum int64, name string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return value, nil
}

func firstStatus(raw string) (int, error) {
	for _, part := range strings.FieldsFunc(raw, func(char rune) bool {
		return char == ',' || char == ':' || unicode.IsSpace(char)
	}) {
		if part == "" || part == "-" {
			continue
		}
		return boundedInt(part, 100, 599, "upstream status")
	}
	return 0, nil
}
