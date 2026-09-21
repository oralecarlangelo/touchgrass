package service

import (
	"errors"
)

// ErrInvalidInput reports caller-supplied data that failed validation.
// Handlers translate it to 400; anything else is a 500.
var ErrInvalidInput = errors.New("service: invalid input")

// ErrConflict reports a request that collides with in-flight work.
// Handlers translate it to 409.
var ErrConflict = errors.New("service: conflict")

// ErrUnauthorized reports a missing, malformed, or revoked credential.
// Handlers translate it to 401 without logging secrets.
var ErrUnauthorized = errors.New("service: unauthorized")
