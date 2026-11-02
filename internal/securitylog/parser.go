// Package securitylog parses a dedicated privacy-safe JSONL security stream.
package securitylog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const maxSecurityFieldBytes = 128

var securityIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

type eventRecord struct {
	Timestamp  string `json:"timestamp"`
	EventType  string `json:"event_type"`
	RuleID     string `json:"rule_id"`
	Category   string `json:"category"`
	Severity   string `json:"severity"`
	Confidence string `json:"confidence"`
	Source     string `json:"source"`
	Action     string `json:"action"`
	Method     string `json:"method"`
	Status     string `json:"status"`
}

// Parser decodes only approved bounded fields. Unknown JSON fields are
// intentionally ignored and never copied into evidence.
type Parser struct{}

// Parse validates one security event record.
func (Parser) Parse(line []byte) (model.SecurityEvent, error) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	var record eventRecord
	if err := decoder.Decode(&record); err != nil {
		return model.SecurityEvent{}, errors.New("security event record is malformed")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return model.SecurityEvent{}, errors.New("security event record has trailing data")
	}

	timestamp, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return model.SecurityEvent{}, errors.New("security event timestamp is invalid")
	}
	for name, value := range map[string]string{
		"event type": record.EventType, "rule id": record.RuleID, "source": record.Source,
		"action": record.Action,
	} {
		if len(value) > maxSecurityFieldBytes || !securityIdentifier.MatchString(value) {
			return model.SecurityEvent{}, fmt.Errorf("security event %s is invalid", name)
		}
	}

	category := model.SecurityCategory(record.Category)
	if category != model.SecurityCategoryPosture && category != model.SecurityCategoryProcess &&
		category != model.SecurityCategoryDrift && category != model.SecurityCategoryTraffic {
		return model.SecurityEvent{}, errors.New("security event category is invalid")
	}
	severity := model.SecuritySeverity(record.Severity)
	if severity != model.SecuritySeverityLow && severity != model.SecuritySeverityMedium &&
		severity != model.SecuritySeverityHigh && severity != model.SecuritySeverityCritical {
		return model.SecurityEvent{}, errors.New("security event severity is invalid")
	}
	confidence := model.SecurityConfidence(record.Confidence)
	if confidence != model.SecurityConfidenceLow && confidence != model.SecurityConfidenceMedium &&
		confidence != model.SecurityConfidenceHigh {
		return model.SecurityEvent{}, errors.New("security event confidence is invalid")
	}

	method := strings.ToUpper(strings.TrimSpace(record.Method))
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		method = "OTHER"
	}
	status, err := strconv.Atoi(record.Status)
	if err != nil || status < 100 || status > 599 {
		return model.SecurityEvent{}, errors.New("security event status is invalid")
	}

	return model.SecurityEvent{
		Timestamp: timestamp.UTC(), EventType: record.EventType, RuleID: record.RuleID,
		Category: category, Severity: severity, Confidence: confidence,
		Source: record.Source, Action: record.Action, Method: method, Status: status,
	}, nil
}
