package store

import (
	"context"
	"errors"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestKeyStoreRoundTrip(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	keys := NewKeyStore(db)

	created, err := keys.Create(ctx, model.APIKey{
		ServiceID: testServiceAPI, KeyHash: "hash-one",
		KeyPrefix: "tg_prefix1", SampleRate: 0.5,
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if created.ID == 0 {
		t.Fatal("Create() id = 0, want non-zero")
	}

	got, err := keys.ByHash(ctx, "hash-one")
	if err != nil {
		t.Fatalf("ByHash() error = %v, want nil", err)
	}

	if got.ID != created.ID || got.ServiceID != testServiceAPI || got.SampleRate != 0.5 {
		t.Errorf("ByHash() = %+v, want the created key", got)
	}

	if _, err := keys.ByHash(ctx, "hash-unknown"); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("ByHash() error = %v, want ErrKeyNotFound", err)
	}
}

func TestKeyStoreListHidesHash(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	keys := NewKeyStore(db)

	if _, err := keys.Create(ctx, model.APIKey{
		ServiceID: testServiceAPI, KeyHash: "hash-list",
		KeyPrefix: "tg_prefix2", SampleRate: 1,
	}); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	listed, err := keys.ListByService(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(listed) != 1 {
		t.Fatalf("ListByService() = %d keys, want 1", len(listed))
	}

	if listed[0].KeyHash != "" {
		t.Error("ListByService() leaks key hash, want blank")
	}

	if listed[0].KeyPrefix != "tg_prefix2" {
		t.Errorf("ListByService() prefix = %q, want tg_prefix2", listed[0].KeyPrefix)
	}
}

func TestKeyStoreRevoke(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	keys := NewKeyStore(db)

	created, err := keys.Create(ctx, model.APIKey{
		ServiceID: testServiceAPI, KeyHash: "hash-revoke",
		KeyPrefix: "tg_prefix3", SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if err := keys.Revoke(ctx, created.ID); err != nil {
		t.Fatalf("Revoke() error = %v, want nil", err)
	}

	if err := keys.Revoke(ctx, created.ID); err != nil {
		t.Fatalf("second Revoke() error = %v, want nil (idempotent)", err)
	}

	if _, err := keys.ByHash(ctx, "hash-revoke"); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("ByHash() error = %v, want ErrKeyNotFound after revoke", err)
	}

	if err := keys.Revoke(ctx, 9999); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("Revoke() error = %v, want ErrKeyNotFound", err)
	}
}
