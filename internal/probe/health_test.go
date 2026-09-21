package probe

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// roundTripFunc stubs HTTP transport without sockets.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// stubResponse builds a minimal transport response.
func stubResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     http.Header{},
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	ok := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return stubResponse(http.StatusOK), nil
	})
	sick := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return stubResponse(http.StatusInternalServerError), nil
	})
	broken := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("connection refused")
	})

	tests := []struct {
		name           string
		target         string
		transport      roundTripFunc
		expectedResult Result
		wantErr        bool
	}{
		{
			name:           "healthy on 200",
			target:         "http://127.0.0.1:4101/health",
			transport:      ok,
			expectedResult: Result{Healthy: true, StatusCode: http.StatusOK},
			wantErr:        false,
		},
		{
			name:           "unhealthy on 500",
			target:         "http://127.0.0.1:4101/health",
			transport:      sick,
			expectedResult: Result{Healthy: false, StatusCode: http.StatusInternalServerError},
			wantErr:        false,
		},
		{
			name:           "transport error",
			target:         "http://127.0.0.1:4101/health",
			transport:      broken,
			expectedResult: Result{},
			wantErr:        true,
		},
		{
			name:           "malformed url",
			target:         "://bad-url",
			transport:      ok,
			expectedResult: Result{},
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prober := New()
			prober.client.Transport = tt.transport

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			got, err := prober.Check(ctx, tt.target)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Check() error = nil, want error")
				}

				return
			}

			if err != nil {
				t.Fatalf("Check() error = %v, want nil", err)
			}

			if got != tt.expectedResult {
				t.Errorf("Check() = %+v, want %+v", got, tt.expectedResult)
			}
		})
	}
}
