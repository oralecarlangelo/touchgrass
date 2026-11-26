package service

import (
	"context"
	"errors"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

type storageQuerier interface {
	StorageUsage(context.Context) (model.StorageUsage, error)
}

// StorageUsage returns storage accounting when supported by the daemon adapter.
func (s *System) StorageUsage(ctx context.Context) (model.StorageUsage, error) {
	reader, ok := s.docker.(storageQuerier)
	if !ok {
		return model.StorageUsage{}, errors.New("docker storage accounting unavailable")
	}

	return reader.StorageUsage(ctx)
}
