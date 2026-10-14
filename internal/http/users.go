package http

import (
	"log/slog"
	"strconv"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

// userResponse is the user payload. It never carries a password hash.
type userResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
}

// usersResponse is the user list payload.
type usersResponse struct {
	Users []userResponse `json:"users"`
}

// createUserRequest is the user creation body.
type createUserRequest struct {
	Username string     `json:"username"`
	Password string     `json:"password"`
	Role     model.Role `json:"role"`
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

	created, err := s.users.Create(r.Context(), service.CreateUserInput{
		Username: body.Username, Password: body.Password, Role: body.Role,
	}, requestActor(r))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to create user")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusCreated, toUserResponse(created))
}

// handleUserPassword resets one user's password.
func (s *Server) handleUserPassword(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := decodeUserID(w, s.logger, r)
	if !ok {
		return
	}

	body, ok := decodeBody[setUserPasswordRequest](w, s.logger, r)
	if !ok {
		return
	}

	if err := s.users.SetPassword(r.Context(), id, body.Password, requestActor(r)); err != nil {
		writeServiceError(w, s.logger, err, "failed to reset password")

		return
	}

	w.WriteHeader(nethttp.StatusNoContent)
}

// handleUserRole changes one user's role.
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

	if err := s.users.Delete(r.Context(), id, requestActor(r)); err != nil {
		writeServiceError(w, s.logger, err, "failed to delete user")

		return
	}

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
	return userResponse{
		ID:        user.ID,
		Username:  user.Username,
		Role:      string(user.Role),
		Disabled:  user.Disabled(),
		CreatedAt: user.CreatedAt,
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
