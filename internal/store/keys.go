package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrKeyNotFound reports an unknown or revoked API key id.
var ErrKeyNotFound = errors.New("store: api key not found")

// KeyStore persists ingestion API keys.
type KeyStore struct {
	db *DB
}

// NewKeyStore builds a KeyStore on db.
func NewKeyStore(db *DB) *KeyStore {
	return &KeyStore{db: db}
}

// Create stores one key and returns it with its id.
func (s *KeyStore) Create(ctx context.Context, key model.APIKey) (model.APIKey, error) {
	if key.CreatedAt.IsZero() {
		key.CreatedAt = time.Now()
	}

	res, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO api_keys (service_id, key_hash, key_prefix, sample_rate, revoked_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		key.ServiceID, key.KeyHash, key.KeyPrefix, key.SampleRate,
		formatNullTime(key.RevokedAt), formatTime(key.CreatedAt),
	)
	if err != nil {
		return model.APIKey{}, fmt.Errorf("creating api key: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return model.APIKey{}, fmt.Errorf("reading api key id: %w", err)
	}

	key.ID = id

	return key, nil
}

// ByHash returns the live key for a hash, or ErrKeyNotFound when unknown
// or revoked.
func (s *KeyStore) ByHash(ctx context.Context, hash string) (model.APIKey, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT id, service_id, key_hash, key_prefix, sample_rate, revoked_at, created_at
		 FROM api_keys WHERE key_hash = ?`,
		hash,
	)

	key, err := scanKey(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.APIKey{}, ErrKeyNotFound
		}

		return model.APIKey{}, err
	}

	if key.RevokedAt != nil {
		return model.APIKey{}, ErrKeyNotFound
	}

	return key, nil
}

// ListByService returns keys newest first, bounded by limit.
func (s *KeyStore) ListByService(ctx context.Context, serviceID string, limit int) ([]model.APIKey, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT id, service_id, key_hash, key_prefix, sample_rate, revoked_at, created_at
		 FROM api_keys WHERE service_id = ? ORDER BY id DESC LIMIT ?`,
		serviceID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying api keys: %w", err)
	}

	defer func() {
		_ = rows.Close()
	}()

	keys := []model.APIKey{}

	for rows.Next() {
		key, err := scanKey(rows)
		if err != nil {
			return nil, err
		}

		key.KeyHash = ""

		keys = append(keys, key)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating api keys: %w", err)
	}

	return keys, nil
}

// Revoke stamps a key revoked. Revoking twice still succeeds.
func (s *KeyStore) Revoke(ctx context.Context, id int64) error {
	res, err := s.db.sql.ExecContext(ctx,
		"UPDATE api_keys SET revoked_at = ? WHERE id = ?",
		formatTime(time.Now()), id,
	)
	if err != nil {
		return fmt.Errorf("revoking api key: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting revoked api keys: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %d", ErrKeyNotFound, id)
	}

	return nil
}

// scanKey maps one api_keys row.
func scanKey(row scanner) (model.APIKey, error) {
	var (
		key       model.APIKey
		revokedAt sql.NullString
		createdAt string
	)

	if err := row.Scan(
		&key.ID,
		&key.ServiceID,
		&key.KeyHash,
		&key.KeyPrefix,
		&key.SampleRate,
		&revokedAt,
		&createdAt,
	); err != nil {
		return model.APIKey{}, fmt.Errorf("scanning api key: %w", err)
	}

	revoked, err := parseNullTime(revokedAt, "revoked_at")
	if err != nil {
		return model.APIKey{}, err
	}

	key.RevokedAt = revoked

	created, err := parseTime(createdAt, "created_at")
	if err != nil {
		return model.APIKey{}, err
	}

	key.CreatedAt = created

	return key, nil
}
