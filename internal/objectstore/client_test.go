package objectstore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testClient(t *testing.T, writable bool, transport http.RoundTripper) *Client {
	t.Helper()

	api, err := minio.New("objects.example.test", &minio.Options{Secure: true, Region: "us-east-1", Creds: credentials.NewStaticV4("test-access", "test-secret", ""), Transport: transport})
	if err != nil {
		t.Fatal(err)
	}

	return &Client{api: api, buckets: []string{"example-assets"}, writable: writable, httpClient: &http.Client{Transport: transport}}
}

func TestBucketAllowlistAndReadOnly(t *testing.T) {
	t.Parallel()
	c := testClient(t, false, testTransport(func(*http.Request) (*http.Response, error) {
		t.Error("forbidden operation reached network")
		return nil, errors.New("unexpected request")
	}))

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "outside allowlist", run: func() error { _, err := c.List(t.Context(), "other-bucket", "", ""); return err }},
		{name: "upload disabled", run: func() error { _, err := c.PrepareUpload(t.Context(), "example-assets", "", "a.txt"); return err }},
		{name: "folder disabled", run: func() error { return c.CreateFolder(t.Context(), "example-assets", "folder/") }},
		{name: "delete disabled", run: func() error { return c.Delete(t.Context(), "example-assets", "a.txt") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := tt.run(); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestListBoundedPage(t *testing.T) {
	t.Parallel()
	c := testClient(t, false, testTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("prefix") != "photos/" || r.URL.Query().Get("max-keys") != "100" {
			t.Errorf("unexpected listing query: %s", r.URL.RawQuery)
		}

		var body strings.Builder
		body.WriteString(`<ListBucketResult><Name>example-assets</Name><IsTruncated>true</IsTruncated><NextContinuationToken>opaque-next+token</NextContinuationToken><CommonPrefixes><Prefix>photos/folder/</Prefix></CommonPrefixes>`)

		for i := range 99 {
			fmt.Fprintf(&body, `<Contents><Key>photos/%03d.jpg</Key><Size>7</Size></Contents>`, i)
		}

		body.WriteString(`</ListBucketResult>`)

		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body.String())), Request: r}, nil
	}))

	page, err := c.List(t.Context(), "example-assets", "photos/", "")
	if err != nil {
		t.Fatal(err)
	}

	if len(page.Objects) != 100 || page.Next != "opaque-next+token" {
		t.Fatalf("page = %+v", page)
	}
}

func TestUploadGrantIsUniqueAndBounded(t *testing.T) {
	t.Parallel()
	c := testClient(t, true, nil)

	first, err := c.PrepareUpload(t.Context(), "example-assets", "photos/", "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}

	second, err := c.PrepareUpload(t.Context(), "example-assets", "photos/", "photo.jpg")
	if err != nil {
		t.Fatal(err)
	}

	if first.Key == second.Key || !strings.HasPrefix(first.Key, "photos/") {
		t.Fatal("upload must use a unique key in the selected prefix")
	}

	raw, err := base64.StdEncoding.DecodeString(first.Fields["policy"])
	if err != nil {
		t.Fatal(err)
	}

	var policy map[string]any
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(raw), "content-length-range") || !strings.Contains(string(raw), "104857600") {
		t.Fatalf("unbounded upload policy: %s", raw)
	}
}

func TestListCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	c := testClient(t, false, testTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() }))
	if _, err := c.List(ctx, "example-assets", "", ""); err == nil {
		t.Fatal("cancelled listing must fail")
	}
}

func TestObjectPageMixedPrefixesAndMissingCursor(t *testing.T) {
	t.Parallel()

	_, err := objectPage(minio.ListBucketV2Result{IsTruncated: true})
	if err == nil {
		t.Fatal("truncated response without cursor must fail")
	}
}

func TestConnectionRequiresPrivateConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "storage.json")

	data := []byte(`{"endpoint":"https://objects.example.test","region":"us-east-1","access_key":"test","secret_key":"secret","buckets":["example-assets"]}`)
	//nolint:gosec // Deliberately insecure fixture permissions verify that Open rejects exposed credentials.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path); err == nil {
		t.Fatal("world-readable credentials accepted")
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	if c.Writable() {
		t.Fatal("writes should require explicit opt-in")
	}
}
