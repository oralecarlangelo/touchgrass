package http

import (
	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
)

// accessMatrixRow is one capability of the access matrix payload.
// Roles has an entry for every role in the payload's role list; Limits
// has one only for a role whose grant is narrower than the name says.
type accessMatrixRow struct {
	Capability string            `json:"capability"`
	Scope      string            `json:"scope"`
	Summary    string            `json:"summary"`
	Roles      map[string]bool   `json:"roles"`
	Limits     map[string]string `json:"limits"`
}

// accessMatrixResponse is the role-to-capability table. Roles are
// ordered from the least to the most powerful and capabilities in
// matrix order.
type accessMatrixResponse struct {
	Roles        []string          `json:"roles"`
	Capabilities []accessMatrixRow `json:"capabilities"`
}

// handleAccessMatrix returns the policy as the guards hold it. The
// payload is built from authz on every call: there is no second table
// to keep in step.
func (s *Server) handleAccessMatrix(w nethttp.ResponseWriter, _ *nethttp.Request) {
	response := accessMatrixResponse{Roles: []string{}, Capabilities: []accessMatrixRow{}}

	for _, role := range authz.Roles() {
		response.Roles = append(response.Roles, string(role))
	}

	for _, row := range authz.Matrix() {
		entry := accessMatrixRow{
			Capability: string(row.Capability),
			Scope:      row.Scope.String(),
			Summary:    row.Summary,
			Roles:      map[string]bool{},
			Limits:     map[string]string{},
		}

		for role, held := range row.Grants {
			entry.Roles[string(role)] = held
		}

		for role, limit := range row.Limits {
			entry.Limits[string(role)] = limit
		}

		response.Capabilities = append(response.Capabilities, entry)
	}

	writeJSON(w, s.logger, nethttp.StatusOK, response)
}
