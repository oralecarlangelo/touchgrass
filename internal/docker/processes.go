package docker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	containertypes "github.com/docker/docker/api/types/container"
)

const maxProcessRows = 256

// Process is the privacy-safe identity of one process. Command contains only
// the executable token, never its arguments.
type Process struct {
	PID       int    `json:"pid"`
	ParentPID int    `json:"parent_pid"`
	User      string `json:"user"`
	Command   string `json:"command"`
}

// ProcessSnapshot is a bounded container process sample.
type ProcessSnapshot struct {
	Processes []Process `json:"processes"`
	Complete  bool      `json:"complete"`
}

// ProcessLister reads bounded process snapshots.
type ProcessLister interface {
	Processes(ctx context.Context, id string) (ProcessSnapshot, error)
}

// Processes requests only the fields required by the security scanner.
func (c *Client) Processes(ctx context.Context, id string) (ProcessSnapshot, error) {
	response, err := c.cli.ContainerTop(ctx, id, []string{"-eo", "pid,ppid,user,args"})
	if err != nil {
		return ProcessSnapshot{Processes: []Process{}, Complete: false}, fmt.Errorf("listing container processes: %w", err)
	}
	processes, err := normalizeProcesses(response, maxProcessRows)
	if err != nil {
		return ProcessSnapshot{Processes: []Process{}, Complete: false}, err
	}
	return ProcessSnapshot{Processes: processes, Complete: len(response.Processes) <= maxProcessRows}, nil
}

func normalizeProcesses(response containertypes.TopResponse, limit int) ([]Process, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("normalizing processes: positive row limit is required")
	}
	indices := map[string]int{}
	for index, title := range response.Titles {
		indices[strings.ToUpper(strings.TrimSpace(title))] = index
	}
	commandIndex, ok := indices["COMMAND"]
	if !ok {
		commandIndex, ok = indices["CMD"]
	}
	if !ok {
		commandIndex, ok = indices["ARGS"]
	}
	pidIndex, pidOK := indices["PID"]
	parentIndex, parentOK := indices["PPID"]
	userIndex, userOK := indices["USER"]
	if !pidOK || !parentOK || !userOK || !ok {
		return nil, fmt.Errorf("normalizing processes: required PID, PPID, USER, and COMMAND headers are missing")
	}

	processes := make([]Process, 0, min(len(response.Processes), limit))
	for rowIndex, row := range response.Processes {
		if len(processes) == limit {
			break
		}
		maximum := max(pidIndex, parentIndex, userIndex, commandIndex)
		if len(row) <= maximum {
			return nil, fmt.Errorf("normalizing process row %d: malformed columns", rowIndex)
		}
		for _, value := range []string{row[pidIndex], row[parentIndex], row[userIndex], row[commandIndex]} {
			if containsUnsafeControl(value) {
				return nil, fmt.Errorf("normalizing process row %d: control character rejected", rowIndex)
			}
		}
		pid, err := strconv.Atoi(strings.TrimSpace(row[pidIndex]))
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("normalizing process row %d: invalid pid", rowIndex)
		}
		parent, err := strconv.Atoi(strings.TrimSpace(row[parentIndex]))
		if err != nil || parent < 0 {
			return nil, fmt.Errorf("normalizing process row %d: invalid parent pid", rowIndex)
		}
		fields := strings.Fields(row[commandIndex])
		if len(fields) == 0 {
			return nil, fmt.Errorf("normalizing process row %d: command is required", rowIndex)
		}
		command := fields[0]
		if len(command) > 256 {
			command = command[:256]
		}
		user := strings.TrimSpace(row[userIndex])
		if len(user) > 128 {
			user = user[:128]
		}
		processes = append(processes, Process{PID: pid, ParentPID: parent, User: user, Command: command})
	}
	return processes, nil
}

func containsUnsafeControl(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && r != '\t'
	})
}
