package objectstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7"
)

// Upload describes a short-lived, size-limited browser upload grant.
type Upload struct {
	URL    string            `json:"url"`
	Fields map[string]string `json:"fields"`
	Key    string            `json:"key"`
}

// Download creates a short-lived attachment link; object content never executes in Touchgrass.
func (c *Client) Download(ctx context.Context, bucket, key string) (string, error) {
	if !c.Allowed(bucket) || key == "" {
		return "", errors.New("object not allowed")
	}

	params := url.Values{"response-content-disposition": {mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(key)})}}

	signed, err := c.api.PresignedGetObject(ctx, bucket, key, 5*time.Minute, params)
	if err != nil {
		return "", fmt.Errorf("signing download: %w", err)
	}

	return signed.String(), nil
}

// PrepareUpload grants a new, unique object key instead of overwriting an existing object.
func (c *Client) PrepareUpload(ctx context.Context, bucket, prefix, filename string) (Upload, error) {
	if !c.writable || !c.Allowed(bucket) {
		return Upload{}, errors.New("object writes disabled")
	}

	key, err := objectKey(prefix, filename)
	if err != nil {
		return Upload{}, err
	}

	policy := minio.NewPostPolicy()
	for _, err := range []error{policy.SetBucket(bucket), policy.SetKey(key), policy.SetExpires(time.Now().Add(5 * time.Minute)), policy.SetContentLengthRange(1, 100*1024*1024)} {
		if err != nil {
			return Upload{}, fmt.Errorf("building upload policy: %w", err)
		}
	}

	endpoint, fields, err := c.api.PresignedPostPolicy(ctx, policy)
	if err != nil {
		return Upload{}, fmt.Errorf("signing upload: %w", err)
	}

	return Upload{URL: endpoint.String(), Fields: fields, Key: key}, nil
}

// Read opens one allowed object for authenticated streaming through Touch Grass.
func (c *Client) Read(ctx context.Context, bucket, key string) (io.ReadCloser, int64, error) {
	if !c.Allowed(bucket) || key == "" {
		return nil, 0, errors.New("object not allowed")
	}

	object, err := c.api.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, errors.New("object unavailable")
	}

	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return nil, 0, errors.New("object unavailable")
	}

	return object, info.Size, nil
}

// Write stores one bounded object under a unique key.
func (c *Client) Write(ctx context.Context, bucket, prefix, filename string, body io.Reader, size int64) (string, error) {
	if !c.writable || !c.Allowed(bucket) || size < 1 || size > 100*1024*1024 {
		return "", errors.New("object upload not allowed")
	}

	key, err := objectKey(prefix, filename)
	if err != nil {
		return "", err
	}

	contentType := mime.TypeByExtension(path.Ext(filename))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	options := minio.PutObjectOptions{ContentType: contentType, NumThreads: 1, PartSize: 5 * 1024 * 1024}

	if _, err := c.api.PutObject(ctx, bucket, key, body, size, options); err != nil {
		return "", errors.New("object upload unavailable")
	}

	return key, nil
}

func objectKey(prefix, filename string) (string, error) {
	filename = path.Base(strings.ReplaceAll(filename, "\\", "/"))
	if filename == "." || filename == "/" || filename == "" {
		return "", errors.New("invalid filename")
	}

	return prefix + uuid.NewString() + "-" + filename, nil
}

// CreateFolder creates a zero-byte prefix marker, refusing to overwrite a key.
func (c *Client) CreateFolder(ctx context.Context, bucket, key string) error {
	if !c.writable || !c.Allowed(bucket) || key == "" || !strings.HasSuffix(key, "/") {
		return errors.New("folder write not allowed")
	}

	opts := minio.PutObjectOptions{ContentType: "application/octet-stream"}
	opts.SetMatchETagExcept("*")

	if _, err := c.api.PutObject(ctx, bucket, key, strings.NewReader(""), 0, opts); err != nil {
		return fmt.Errorf("creating folder: %w", err)
	}

	return nil
}

// Delete removes one exact current key. It does not recursively delete prefixes or purge versions.
func (c *Client) Delete(ctx context.Context, bucket, key string) error {
	if !c.writable || !c.Allowed(bucket) || key == "" {
		return errors.New("object delete not allowed")
	}

	if err := c.api.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("deleting object: %w", err)
	}

	return nil
}
