package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// ErrNoOwner reports a resource id that names no row a service owns.
var ErrNoOwner = errors.New("resource has no owning service")

// Owners resolves which service a resource row belongs to. Requests
// keyed by a resource id (an issue, a log line, a rule, a key, a
// notification) are scoped by the answer.
type Owners struct {
	owners *store.OwnerStore
}

// NewOwners builds an Owners service.
func NewOwners(owners *store.OwnerStore) *Owners {
	return &Owners{owners: owners}
}

// ServiceOf returns the id of the service that owns one resource row.
// An id that names no row, or a row with no service, reports
// ErrNoOwner.
func (o *Owners) ServiceOf(ctx context.Context, kind model.ResourceKind, id int64) (string, error) {
	serviceID, err := o.owners.ServiceOf(ctx, kind, id)
	if err != nil {
		if errors.Is(err, store.ErrResourceNotFound) {
			return "", ErrNoOwner
		}

		return "", fmt.Errorf("resolving owning service: %w", err)
	}

	return serviceID, nil
}
