package probe

import (
	"errors"
	"testing"
)

func TestLiveTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		conf     string
		expected string
		wantErr  bool
	}{
		{
			name:     "blue live",
			conf:     "testdata/nginx-blue.conf",
			expected: "127.0.0.1:4101",
			wantErr:  false,
		},
		{
			name:     "green live",
			conf:     "testdata/nginx-green.conf",
			expected: "127.0.0.1:4102",
			wantErr:  false,
		},
		{
			name:     "legacy live",
			conf:     "testdata/nginx-legacy.conf",
			expected: "127.0.0.1:4000",
			wantErr:  false,
		},
		{
			name:     "first marker wins",
			conf:     "testdata/nginx-duplicate.conf",
			expected: "127.0.0.1:4101",
			wantErr:  false,
		},
		{
			name:     "missing marker",
			conf:     "testdata/nginx-nomarker.conf",
			expected: "",
			wantErr:  true,
		},
		{
			name:     "missing file",
			conf:     "testdata/nginx-missing.conf",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := LiveTarget(tt.conf, "# BLUEGREEN-ACTIVE")

			if tt.wantErr {
				if err == nil {
					t.Fatal("LiveTarget() error = nil, want error")
				}

				return
			}

			if err != nil {
				t.Fatalf("LiveTarget() error = %v, want nil", err)
			}

			if got != tt.expected {
				t.Errorf("LiveTarget() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestLiveTargetMarkerError(t *testing.T) {
	t.Parallel()

	_, err := LiveTarget("testdata/nginx-nomarker.conf", "# BLUEGREEN-ACTIVE")
	if !errors.Is(err, ErrMarkerNotFound) {
		t.Errorf("LiveTarget() error = %v, want ErrMarkerNotFound", err)
	}
}
