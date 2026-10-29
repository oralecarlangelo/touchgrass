package docker

import (
	"testing"

	containertypes "github.com/docker/docker/api/types/container"
)

func TestNormalizeProcessesKeepsPIDParentUserAndCommand(t *testing.T) {
	t.Parallel()
	got, err := normalizeProcesses(containertypes.TopResponse{
		Titles:    []string{"PID", "PPID", "USER", "COMMAND"},
		Processes: [][]string{{"12", "1", "app", "/usr/local/bin/worker"}},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := Process{PID: 12, ParentPID: 1, User: "app", Command: "/usr/local/bin/worker"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("normalizeProcesses() = %+v, want %+v", got, want)
	}
}

func TestNormalizeProcessesDropsFullArguments(t *testing.T) {
	t.Parallel()
	got, err := normalizeProcesses(containertypes.TopResponse{
		Titles:    []string{"PID", "PPID", "USER", "COMMAND"},
		Processes: [][]string{{"12", "1", "app", "/tmp/worker --token secret-value"}},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Command != "/tmp/worker" {
		t.Fatalf("normalizeProcesses() = %+v, want command only", got)
	}
}

func TestNormalizeProcessesRejectsControlCharacters(t *testing.T) {
	t.Parallel()
	_, err := normalizeProcesses(containertypes.TopResponse{
		Titles:    []string{"PID", "PPID", "USER", "COMMAND"},
		Processes: [][]string{{"12", "1", "app", "/tmp/bad\ncommand"}},
	}, 10)
	if err == nil {
		t.Fatal("normalizeProcesses() error = nil, want control-character rejection")
	}
}

func TestNormalizeProcessesReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	got, err := normalizeProcesses(containertypes.TopResponse{
		Titles: []string{"PID", "PPID", "USER", "COMMAND"},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("normalizeProcesses() = %#v, want non-nil empty", got)
	}
}
