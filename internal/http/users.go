package http

import (
	"errors"
	"log/slog"
	"strconv"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

// errScopeRequired reports an access body that leaves the scope unsaid.
var errScopeRequired = errors.New("scope is not stated")

// userResponse is the user payload. It never carries a password hash.
// Services is empty for an all-services user.
type userResponse struct {
	ID                 int64     `json:"id"`
	Username           string    `json:"username"`
	Role               string    `json:"role"`
	AllServices        bool      `json:"all_services"`
	Services           []string  `json:"services"`
	MustChangePassword bool      `json:"must_change_password"`
	Disabled           bool      `json:"disabled"`
	CreatedAt          time.Time `json:"created_at"`
}

// usersResponse is the user list payload.
type usersResponse struct {
	Users []userResponse `json:"users"`
}

// createUserRequest is the user creation body. AllServices may be
// omitted, which means every service, but only when no service is
// listed: a list never widens into everything by accident.
type createUserRequest struct {
	Username    string     `json:"username"`
	Password    string     `json:"password"`
	Role        model.Role `json:"role"`
	AllServices *bool      `json:"all_services"`
	Services    []string   `json:"services"`
}

// setUserAccessRequest is the access replacement body: the role and
// the whole scope, stated together.
type setUserAccessRequest struct {
	Role        model.Role `json:"role"`
	AllServices *bool      `json:"all_services"`
	Services    []string   `json:"services"`
}

// setUserPasswordRequest is the password reset body.
type setUserPasswordRequest struct {
	Password string `json:"password"`
}

// setUserRoleRequest is the role change body.
type setUserRoleRequest struct {
	Role model.Role `json:"role"`
}

// handleUsers lists managed users.
func (s *Server) handleUsers(w nethttp.ResponseWriter, r *nethttp.Request) {
	users, err := s.users.List(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load users")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, usersResponse{Users: toUserResponses(users)})
}

// handleCreateUser stores a new managed user.
func (s *Server) handleCreateUser(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[createUserRequest](w, s.logger, r)
	if !ok {
		return
	}

	allServices, ok := createScope(body)
	if !ok {
		writeError(w, s.logger, errScopeRequired,
			"set all_services to false to grant a list of services", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	created, err := s.users.Create(r.Context(), service.CreateUserInput{
		Username: body.Username, Password: body.Password, Role: body.Role,
		AllServices: allServices, Services: body.Services,
	}, requestActor(r))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to create user")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusCreated, toUserResponse(created))
}

// createScope resolves the scope of a creation body. An omitted
// all_services means every service unless services are listed, which
// is ambiguous and refused.
func createScope(body createUserRequest) (allServices, ok bool) {
	if body.AllServices != nil {
		return *body.AllServices, true
	}

	return true, len(body.Services) == 0
}

// handleUserPassword resets one user's password to a temporary one and
// ends that user's sessions and streams: whoever held the old password
// is out, and the new one must be replaced at the next login.
func (s *Server) handleUserPassword(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := decodeUserID(w, s.logger, r)
	if !ok {
		return
	}

	body, ok := decodeBody[setUserPasswordRequest](w, s.logger, r)
	if !ok {
		return
	}

	user, err := s.users.SetPassword(r.Context(), id, body.Password, requestActor(r))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to reset password")

		return
	}

	s.auth.DropSessions(user.Username)
	s.events.Disconnect(user.Username)

	w.WriteHeader(nethttp.StatusNoContent)
}

// handleUserRole changes one user's role. It is kept for clients that
// predate scopes; handleUserAccess sets the role and scope together.
func (s *Server) handleUserRole(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := decodeUserID(w, s.logger, r)
	if !ok {
		return
	}

	body, ok := decodeBody[setUserRoleRequest](w, s.logger, r)
	if !ok {
		return
	}

	updated, err := s.users.SetRole(r.Context(), id, body.Role, requestActor(r))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to change role")

		return
	}

	s.events.Disconnect(updated.Username)

	writeJSON(w, s.logger, nethttp.StatusOK, toUserResponse(updated))
}

// handleUserAccess replaces one user's role and scope in one step.
func (s *Server) handleUserAccess(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := decodeUserID(w, s.logger, r)
	if !ok {
		return
	}

	body, ok := decodeBody[setUserAccessRequest](w, s.logger, r)
	if !ok {
		return
	}

	if body.AllServices == nil {
		writeError(w, s.logger, errScopeRequired, "all_services is required", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	updated, err := s.users.SetAccess(r.Context(), id, model.UserAccess{
		Role: body.Role, AllServices: *body.AllServices, Services: body.Services,
	}, requestActor(r))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to change access")

		return
	}

	// The user's open streams were authorized against the old access.
	s.events.Disconnect(updated.Username)

	writeJSON(w, s.logger, nethttp.StatusOK, toUserResponse(updated))
}

// handleUserDisable locks one account.
func (s *Server) handleUserDisable(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := decodeUserID(w, s.logger, r)
	if !ok {
		return
	}

	updated, err := s.users.SetDisabled(r.Context(), id, true, requestActor(r))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to disable user")

		return
	}

	s.events.Disconnect(updated.Username)

	writeJSON(w, s.logger, nethttp.StatusOK, toUserResponse(updated))
}

// handleUserEnable unlocks one account.
func (s *Server) handleUserEnable(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := decodeUserID(w, s.logger, r)
	if !ok {
		return
	}

	updated, err := s.users.SetDisabled(r.Context(), id, false, requestActor(r))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to enable user")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, toUserResponse(updated))
}

// handleDeleteUser removes one user.
func (s *Server) handleDeleteUser(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := decodeUserID(w, s.logger, r)
	if !ok {
		return
	}

	deleted, err := s.users.Delete(r.Context(), id, requestActor(r))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to delete user")

		return
	}

	s.events.Disconnect(deleted.Username)

	w.WriteHeader(nethttp.StatusNoContent)
}

// decodeUserID parses the {id} path value.
func decodeUserID(
	w nethttp.ResponseWriter,
	logger *slog.Logger,
	r *nethttp.Request,
) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, logger, err, "invalid user id", "invalid_request", nethttp.StatusBadRequest)

		return 0, false
	}

	return id, true
}

// toUserResponse maps one user to its hash-free payload.
func toUserResponse(user model.User) userResponse {
	services := user.Services
	if services == nil || user.AllServices {
		services = []string{}
	}

	return userResponse{
		ID:                 user.ID,
		Username:           user.Username,
		Role:               string(user.Role),
		AllServices:        user.AllServices,
		Services:           services,
		MustChangePassword: user.MustChangePassword,
		Disabled:           user.Disabled(),
		CreatedAt:          user.CreatedAt,
	}
}

// toUserResponses maps users to hash-free payloads.
func toUserResponses(users []model.User) []userResponse {
	responses := []userResponse{}

	for _, user := range users {
		responses = append(responses, toUserResponse(user))
	}

	return responses
}
