// Package objectstore manages explicitly allowed buckets through the S3 API.
package objectstore

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config is an operator-owned, mode-0600 connection file. Endpoint must be browser reachable.
type Config struct {
	Endpoint  string   `json:"endpoint"`
	Region    string   `json:"region"`
	AccessKey string   `json:"access_key"`
	SecretKey string   `json:"secret_key"`
	Buckets   []string `json:"buckets"`
	Writable  bool     `json:"writable"`
}

// Client holds server-side credentials and an explicit bucket allowlist.
type Client struct {
	api        *minio.Client
	httpClient *http.Client
	buckets    []string
	writable   bool
}

// Open loads optional connection configuration without exposing credentials to the browser.
func Open(path string) (*Client, error) {
	if path == "" {
		return nil, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading storage connection: %w", err)
	}

	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return nil, errors.New("storage connection must be a regular mode-0600 file")
	}
	//nolint:gosec // Path comes only from operator startup configuration; regular file and mode checked above.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading storage connection: %w", err)
	}
	defer clear(data)

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, errors.New("invalid storage connection JSON")
	}

	return newClient(cfg)
}

func newClient(cfg Config) (*Client, error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, errors.New("invalid storage endpoint")
	}

	if err := validateEndpoint(endpoint); err != nil {
		return nil, err
	}

	missingCredentials := cfg.AccessKey == "" || cfg.SecretKey == ""
	if missingCredentials || len(cfg.Buckets) == 0 {
		return nil, errors.New("storage credentials and bucket allowlist are required")
	}

	api, err := minio.New(endpoint.Host, &minio.Options{Creds: credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""), Secure: endpoint.Scheme == "https", Region: cfg.Region})
	if err != nil {
		return nil, fmt.Errorf("creating object storage client: %w", err)
	}

	return &Client{api: api, buckets: cfg.Buckets, writable: cfg.Writable, httpClient: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Buckets lists configured buckets, without requiring account-wide list permission.
func (c *Client) Buckets() []string { return append([]string{}, c.buckets...) }

// Writable reports the explicit operator opt-in for object writes.
func (c *Client) Writable() bool { return c.writable }

// Allowed checks every operation against the configured bucket allowlist.
func (c *Client) Allowed(bucket string) bool { return slices.Contains(c.buckets, bucket) }

func validateEndpoint(endpoint *url.URL) error {
	validScheme := endpoint.Scheme == "https" || endpoint.Scheme == "http"
	invalidOrigin := endpoint.Host == "" || endpoint.User != nil
	hasSuffix := endpoint.RawQuery != "" || endpoint.Fragment != ""

	hasPath := endpoint.Path != "" && endpoint.Path != "/"
	if !validScheme || invalidOrigin || hasSuffix || hasPath {
		return errors.New("storage endpoint must be an http or https origin")
	}

	return nil
}

// Object is bounded metadata, never object content or credentials.
type Object struct {
	Key      string    `json:"key"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Folder   bool      `json:"folder"`
}

// Page contains one bounded listing. Prefix search is performed server-side.
type Page struct {
	Objects []Object `json:"objects"`
	Next    string   `json:"next"`
}

// List returns a single S3 page with its opaque continuation token.
func (c *Client) List(ctx context.Context, bucket, prefix, after string) (Page, error) {
	page := Page{Objects: []Object{}}
	if !c.Allowed(bucket) {
		return page, errors.New("bucket not allowed")
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	query := url.Values{"list-type": {"2"}, "max-keys": {"100"}, "delimiter": {"/"}, "prefix": {prefix}}
	if after != "" {
		query.Set("continuation-token", after)
	}

	signed, err := c.api.Presign(ctx, http.MethodGet, bucket, "", time.Minute, query)
	if err != nil {
		return page, fmt.Errorf("signing bucket listing: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, signed.String(), nil)
	if err != nil {
		return page, errors.New("invalid bucket listing request")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return page, errors.New("bucket listing unavailable")
	}

	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			cancel()
		}
	}()

	if response.StatusCode != http.StatusOK {
		return page, fmt.Errorf("bucket listing returned status %d", response.StatusCode)
	}

	var result minio.ListBucketV2Result
	if err := xml.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(&result); err != nil {
		return page, errors.New("invalid bucket listing response")
	}

	return objectPage(result)
}

func objectPage(result minio.ListBucketV2Result) (Page, error) {
	page := Page{Objects: []Object{}}
	if len(result.Contents)+len(result.CommonPrefixes) > 100 {
		return page, errors.New("storage returned an oversized page")
	}

	for _, item := range result.Contents {
		page.Objects = append(page.Objects, Object{Key: item.Key, Size: item.Size, Modified: item.LastModified, Folder: strings.HasSuffix(item.Key, "/")})
	}

	for _, item := range result.CommonPrefixes {
		page.Objects = append(page.Objects, Object{Key: item.Prefix, Folder: true})
	}

	slices.SortFunc(page.Objects, func(a, b Object) int { return strings.Compare(a.Key, b.Key) })

	if result.IsTruncated {
		if result.NextContinuationToken == "" {
			return page, errors.New("storage omitted continuation token")
		}

		page.Next = result.NextContinuationToken
	}

	return page, nil
}
