package http

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestHandleSecurityReadMatrixAndSafeDTO(t *testing.T) {
	t.Parallel()
	server, _ := fullTestServer(t)
	finding, err := server.security.Evaluate(t.Context(), model.SecurityEvaluation{Observation: model.SecurityObservation{
		ContainerID: "container", RuleID: "posture.docker_socket", Category: model.SecurityCategoryPosture,
		Severity: model.SecuritySeverityCritical, Confidence: model.SecurityConfidenceHigh,
		Detected: true, CoverageComplete: true, Evidence: map[string]string{"docker_socket": "true"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	cookie := viewerCookie(t, server)
	for _, target := range []string{
		"/api/security/summary",
		"/api/security/findings?limit=10&sort=last_seen",
		"/api/security/findings/" + intString(finding.ID),
		"/api/security/policies",
	} {
		t.Run(target, func(t *testing.T) {
			status, body := securityRequest(t, server, cookie, nethttp.MethodGet, target, "")
			if status != nethttp.StatusOK {
				t.Fatalf("GET %s status = %d body=%s, want 200", target, status, body)
			}
			for _, forbidden := range []string{"raw_command", "secret-value", "environment"} {
				if strings.Contains(strings.ToLower(body), forbidden) {
					t.Errorf("GET %s leaked %q: %s", target, forbidden, body)
				}
			}
		})
	}

	status, body := securityRequest(t, server, cookie, nethttp.MethodGet, "/api/security/findings?limit=1001", "")
	if status != nethttp.StatusBadRequest || !strings.Contains(body, testInvalidRequest) {
		t.Errorf("oversized limit status=%d body=%s, want 400", status, body)
	}
	status, _ = securityRequest(t, server, cookie, nethttp.MethodGet, "/api/security/findings?sort=raw_sql", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("invalid sort status=%d, want 400", status)
	}
}

func TestHandleSecurityWorkflowAuthorizationAndActor(t *testing.T) {
	t.Parallel()
	server, _ := fullTestServer(t)
	newFinding := func(rule string) model.SecurityFinding {
		finding, err := server.security.Evaluate(t.Context(), model.SecurityEvaluation{Observation: model.SecurityObservation{
			ContainerID: "container", RuleID: rule, Category: model.SecurityCategoryPosture,
			Severity: model.SecuritySeverityHigh, Confidence: model.SecurityConfidenceHigh,
			Detected: true, CoverageComplete: true, Evidence: map[string]string{"detected": "true"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return finding
	}
	viewer := viewerCookie(t, server)
	admin := authCookie(t, server)
	blocked := newFinding("posture.viewer_blocked")
	status, _ := securityRequest(t, server, viewer, nethttp.MethodPost,
		"/api/security/findings/"+intString(blocked.ID)+"/acknowledge", "{}")
	if status != nethttp.StatusForbidden {
		t.Fatalf("viewer mutation status = %d, want 403", status)
	}

	ack := newFinding("posture.ack")
	status, body := securityRequest(t, server, admin, nethttp.MethodPost,
		"/api/security/findings/"+intString(ack.ID)+"/acknowledge", "{}")
	if status != nethttp.StatusOK {
		t.Fatalf("acknowledge status=%d body=%s", status, body)
	}
	var acknowledged model.SecurityFinding
	if err := json.Unmarshal([]byte(body), &acknowledged); err != nil {
		t.Fatal(err)
	}
	if acknowledged.AcknowledgedBy != "admin" {
		t.Errorf("acknowledged_by=%q, want authenticated admin", acknowledged.AcknowledgedBy)
	}

	resolved := newFinding("posture.resolve")
	status, _ = securityRequest(t, server, admin, nethttp.MethodPost,
		"/api/security/findings/"+intString(resolved.ID)+"/resolve", "{}")
	if status != nethttp.StatusOK {
		t.Errorf("resolve status=%d, want 200", status)
	}
	suppressed := newFinding("posture.suppress")
	status, _ = securityRequest(t, server, admin, nethttp.MethodPost,
		"/api/security/findings/"+intString(suppressed.ID)+"/suppress", `{"reason":"known maintenance"}`)
	if status != nethttp.StatusOK {
		t.Errorf("suppress status=%d, want 200", status)
	}
	missingReason := newFinding("posture.no_reason")
	status, _ = securityRequest(t, server, admin, nethttp.MethodPost,
		"/api/security/findings/"+intString(missingReason.ID)+"/suppress", `{}`)
	if status != nethttp.StatusBadRequest {
		t.Errorf("suppress without reason status=%d, want 400", status)
	}
}

func TestHandleSecurityPolicyMatrix(t *testing.T) {
	t.Parallel()
	server, _ := fullTestServer(t)
	viewer := viewerCookie(t, server)
	admin := authCookie(t, server)
	body := `{"scope":"global","scope_id":"","policy":{"allow_root":true,"allowed_writable_paths":["/var/cache/app"]}}`
	status, _ := securityRequest(t, server, viewer, nethttp.MethodPost, "/api/security/policies", body)
	if status != nethttp.StatusForbidden {
		t.Fatalf("viewer policy create status=%d, want 403", status)
	}
	status, response := securityRequest(t, server, admin, nethttp.MethodPost, "/api/security/policies", body)
	if status != nethttp.StatusCreated {
		t.Fatalf("policy create status=%d body=%s, want 201", status, response)
	}
	var policy model.SecurityPolicy
	if err := json.Unmarshal([]byte(response), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.ID == 0 || policy.UpdatedBy != "admin" {
		t.Fatalf("created policy=%+v, want id and authenticated actor", policy)
	}
	path := "/api/security/policies/" + intString(policy.ID)
	status, _ = securityRequest(t, server, admin, nethttp.MethodPatch, path, `{"policy":{"allow_root":false}}`)
	if status != nethttp.StatusOK {
		t.Errorf("policy patch status=%d, want 200", status)
	}
	status, _ = securityRequest(t, server, admin, nethttp.MethodDelete, path, "")
	if status != nethttp.StatusNoContent {
		t.Errorf("policy delete status=%d, want 204", status)
	}
}

func securityRequest(t *testing.T, server *Server, cookie *nethttp.Cookie, method, target, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	server.handler.ServeHTTP(rec, req)
	result := rec.Result()
	defer func() { _ = result.Body.Close() }()
	data, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	return result.StatusCode, string(data)
}

func intString(value int64) string {
	return strconv.FormatInt(value, 10)
}
