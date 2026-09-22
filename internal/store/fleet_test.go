package store

import (
	"context"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// fleetBase is a minute-aligned seed time so history buckets are stable.
var fleetBase = time.Unix(1699999980, 0).UTC()

// testFleetState is the container state for fleet seeds.
const testFleetState = "running"

func TestFleetLatestContainers(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	fleet := NewFleetStore(db)

	now := time.Now().Truncate(time.Second)
	seedFleetContainers(t, fleet, now)

	got, err := fleet.LatestContainers(ctx)
	if err != nil {
		t.Fatalf("LatestContainers() error = %v, want nil", err)
	}

	if len(got) != 2 {
		t.Fatalf("containers = %d, want 2 (newest per name)", len(got))
	}

	checkHotContainer(t, got[0], now)
	checkColdContainer(t, got[1])
}

// seedFleetContainers stores a stale plus fresh hot row and one cold row.
func seedFleetContainers(t *testing.T, fleet *FleetStore, now time.Time) {
	t.Helper()

	seed := []model.ContainerSample{
		{
			ContainerName: "hot-1",
			Project:       "app",
			Managed:       true,
			ServiceID:     testServiceAPI,
			State:         testFleetState,
			CPUPercent:    10,
			MemBytes:      100,
			MemLimit:      1000,
			Restarts:      2,
			SampledAt:     now.Add(-time.Hour),
		},
		{
			ContainerName: "hot-1",
			Project:       "app",
			Managed:       true,
			ServiceID:     testServiceAPI,
			State:         testFleetState,
			CPUPercent:    50,
			MemBytes:      500,
			MemLimit:      1000,
			Restarts:      1,
			SampledAt:     now,
		},
		{
			ContainerName: "cold-1",
			Project:       "other",
			State:         testFleetState,
			CPUPercent:    5,
			MemBytes:      50,
			MemLimit:      1000,
			SampledAt:     now,
		},
	}

	for _, sample := range seed {
		if err := fleet.InsertContainer(context.Background(), sample); err != nil {
			t.Fatalf("InsertContainer() error = %v, want nil", err)
		}
	}
}

// checkHotContainer asserts the newest managed hot row.
func checkHotContainer(t *testing.T, hot model.FleetContainer, now time.Time) {
	t.Helper()

	if hot.Name != "hot-1" || hot.CPUPercent != 50 || hot.Restarts != 1 {
		t.Errorf("first = %+v, want the newest hot-1 row", hot)
	}

	if !hot.Managed || hot.ServiceID != testServiceAPI || hot.Project != "app" {
		t.Errorf("first = %+v, want managed tn-api row", hot)
	}

	if !hot.SampledAt.Equal(now) {
		t.Errorf("first sampled_at = %v, want %v", hot.SampledAt, now)
	}
}

// checkColdContainer asserts the unmanaged cold row sorts second.
func checkColdContainer(t *testing.T, cold model.FleetContainer) {
	t.Helper()

	if cold.Name != "cold-1" || cold.CPUPercent != 5 {
		t.Errorf("second = %+v, want cold-1 (cpu desc order)", cold)
	}

	if cold.Managed || cold.ServiceID != "" {
		t.Errorf("second = %+v, want unmanaged row without service", cold)
	}
}

func TestFleetHistory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metric   string
		values   [4]float64
		expected [2]float64
	}{
		{
			name:     "cpu averages per bucket",
			metric:   model.MetricCPU,
			values:   [4]float64{10, 30, 50, 70},
			expected: [2]float64{20, 60},
		},
		{
			name:     "mem averages used bytes",
			metric:   model.MetricMem,
			values:   [4]float64{100, 300, 500, 700},
			expected: [2]float64{200, 600},
		},
		{
			name:     "load averages per bucket",
			metric:   model.MetricLoad,
			values:   [4]float64{1, 3, 5, 7},
			expected: [2]float64{2, 6},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := openTestDB(t)
			ctx := context.Background()
			fleet := NewFleetStore(db)

			offsets := []time.Duration{0, 30 * time.Second, 60 * time.Second, 90 * time.Second}

			for i, offset := range offsets {
				sample := model.HostSample{SampledAt: fleetBase.Add(offset)}
				setHistoryValue(&sample, tt.metric, tt.values[i])

				if err := fleet.InsertHost(ctx, sample); err != nil {
					t.Fatalf("InsertHost() error = %v, want nil", err)
				}
			}

			// A null-valued row in the first bucket must not move the average.
			if err := fleet.InsertHost(ctx, model.HostSample{SampledAt: fleetBase.Add(10 * time.Second)}); err != nil {
				t.Fatalf("InsertHost() error = %v, want nil", err)
			}

			// A row outside the window must not appear.
			outside := model.HostSample{SampledAt: fleetBase.Add(-2 * time.Hour)}
			setHistoryValue(&outside, tt.metric, 1000)

			if err := fleet.InsertHost(ctx, outside); err != nil {
				t.Fatalf("InsertHost() error = %v, want nil", err)
			}

			got, err := fleet.History(ctx, tt.metric, fleetBase.Add(-time.Hour), 60)
			if err != nil {
				t.Fatalf("History() error = %v, want nil", err)
			}

			if len(got) != 2 {
				t.Fatalf("points = %d, want 2 buckets", len(got))
			}

			for i, want := range tt.expected {
				if got[i].Value != want {
					t.Errorf("points[%d].value = %v, want %v", i, got[i].Value, want)
				}
			}

			if !got[0].TS.Equal(fleetBase) || !got[1].TS.Equal(fleetBase.Add(time.Minute)) {
				t.Errorf("bucket starts = %v, %v, want %v and +60s", got[0].TS, got[1].TS, fleetBase)
			}
		})
	}
}

// setHistoryValue stamps one metric column on a host sample.
func setHistoryValue(sample *model.HostSample, metric string, value float64) {
	switch metric {
	case model.MetricCPU:
		sample.CPUPercent = &value
	case model.MetricMem:
		bytes := int64(value)
		sample.MemUsed = &bytes
	case model.MetricLoad:
		sample.Load1 = &value
	}
}

func TestFleetHistorySkipsNullBuckets(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	fleet := NewFleetStore(db)

	if err := fleet.InsertHost(ctx, model.HostSample{SampledAt: fleetBase}); err != nil {
		t.Fatalf("InsertHost() error = %v, want nil", err)
	}

	got, err := fleet.History(ctx, model.MetricCPU, fleetBase.Add(-time.Hour), 60)
	if err != nil {
		t.Fatalf("History() error = %v, want nil", err)
	}

	if len(got) != 0 {
		t.Errorf("points = %d, want 0 (all-null bucket skipped)", len(got))
	}
}

func TestFleetHistoryUnknownMetric(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	fleet := NewFleetStore(db)

	if _, err := fleet.History(ctx, "disk", time.Now(), 60); err == nil {
		t.Error("History(disk) error = nil, want unknown metric error")
	}
}

func TestFleetTrims(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	fleet := NewFleetStore(db)

	now := time.Now().Truncate(time.Second)
	cutoff := now.Add(-time.Hour)

	cpu := 10.0

	if err := fleet.InsertHost(ctx, model.HostSample{SampledAt: cutoff.Add(-time.Minute), CPUPercent: &cpu}); err != nil {
		t.Fatalf("InsertHost() error = %v, want nil", err)
	}

	if err := fleet.InsertHost(ctx, model.HostSample{SampledAt: now, CPUPercent: &cpu}); err != nil {
		t.Fatalf("InsertHost() error = %v, want nil", err)
	}

	stale := model.ContainerSample{ContainerName: "old-1", CPUPercent: 1, SampledAt: cutoff.Add(-time.Minute)}
	fresh := model.ContainerSample{ContainerName: "new-1", CPUPercent: 1, SampledAt: now}

	if err := fleet.InsertContainer(ctx, stale); err != nil {
		t.Fatalf("InsertContainer() error = %v, want nil", err)
	}

	if err := fleet.InsertContainer(ctx, fresh); err != nil {
		t.Fatalf("InsertContainer() error = %v, want nil", err)
	}

	hosts, err := fleet.TrimHostsBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("TrimHostsBefore() error = %v, want nil", err)
	}

	if hosts != 1 {
		t.Errorf("trimmed hosts = %d, want 1", hosts)
	}

	containers, err := fleet.TrimContainersBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("TrimContainersBefore() error = %v, want nil", err)
	}

	if containers != 1 {
		t.Errorf("trimmed containers = %d, want 1", containers)
	}

	latest, err := fleet.LatestContainers(ctx)
	if err != nil {
		t.Fatalf("LatestContainers() error = %v, want nil", err)
	}

	if len(latest) != 1 || latest[0].Name != "new-1" {
		t.Errorf("latest = %+v, want only new-1", latest)
	}
}
