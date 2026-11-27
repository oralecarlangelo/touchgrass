package service

import (
	"context"
	"errors"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const storageCollectionFailed = "collection_failed"

type storageQuerier interface {
	StorageUsage(context.Context) (model.StorageUsage, error)
}

// StorageUsage returns storage accounting when supported by the daemon adapter.
func (s *System) StorageUsage(context.Context) (model.StorageUsage, error) {
	s.storageMu.RLock()
	usage := s.storage
	usage.Entries = append([]model.StorageEntry{}, usage.Entries...)
	maxAge := s.storageMaxAge
	s.storageMu.RUnlock()

	if usage.Freshness.ObservedAt != nil && maxAge > 0 && time.Since(*usage.Freshness.ObservedAt) > maxAge {
		usage.Freshness.Stale = true
		usage.Freshness.LastErrorCategory = "stale"
	}

	return usage, nil
}

func (s *System) collectStorage(ctx context.Context) (model.StorageUsage, error) {
	reader, ok := s.docker.(storageQuerier)
	if !ok {
		return model.StorageUsage{}, errors.New("docker storage accounting unavailable")
	}

	return reader.StorageUsage(ctx)
}

func (s *System) runStorageRefresh(ctx context.Context, interval time.Duration) {
	if interval < 5*time.Minute {
		interval = 5 * time.Minute
	}

	s.storageMu.Lock()
	s.storageMaxAge = 2 * interval
	s.storageMu.Unlock()
	s.refreshStorage(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshStorage(ctx)
		case <-s.imageRefresh:
			s.refreshImages(ctx)
		}
	}
}

func (s *System) refreshImages(ctx context.Context) {
	imagesCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	images, err := s.collectImages(imagesCtx)
	observedAt := time.Now().UTC()

	s.storageMu.Lock()
	defer s.storageMu.Unlock()

	if err != nil {
		s.imagesFreshness.Stale = true
		s.imagesFreshness.LastErrorCategory = storageCollectionFailed

		return
	}

	s.images = images
	s.imagesFreshness = model.CollectionFreshness{ObservedAt: &observedAt, Complete: true}
}

func (s *System) refreshStorage(ctx context.Context) {
	s.storageRefreshMu.Lock()
	defer s.storageRefreshMu.Unlock()

	observedAt := time.Now().UTC()
	imagesCtx, cancelImages := context.WithTimeout(ctx, 5*time.Second)
	images, imagesErr := s.collectImages(imagesCtx)

	cancelImages()

	if imagesErr == nil {
		s.storageMu.Lock()
		s.images = images
		s.imagesFreshness = model.CollectionFreshness{ObservedAt: &observedAt, Complete: true}
		s.storageMu.Unlock()
	} else {
		s.storageMu.Lock()
		s.imagesFreshness.Stale = true
		s.imagesFreshness.LastErrorCategory = storageCollectionFailed
		s.storageMu.Unlock()
		s.logger.Warn("image cache refresh degraded", "error", imagesErr)
	}

	storageCtx, cancelStorage := context.WithTimeout(ctx, 50*time.Second)
	usage, storageErr := s.collectStorage(storageCtx)

	cancelStorage()

	if storageErr == nil {
		usage.Freshness = model.CollectionFreshness{ObservedAt: &observedAt, Complete: true}

		s.storageMu.Lock()
		s.storage = usage
		s.storageMu.Unlock()
	} else {
		s.storageMu.Lock()
		s.storage.Freshness.Stale = true
		s.storage.Freshness.LastErrorCategory = storageCollectionFailed
		s.storageMu.Unlock()
		s.logger.Warn("storage cache refresh degraded", "error", storageErr)
	}
}
