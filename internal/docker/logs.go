package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// LogLine is one tailed container log line.
type LogLine struct {
	Timestamp time.Time
	Stream    string
	Message   string
}

// Logs tails a container's recent lines. An empty since reads the tail;
// otherwise only lines after since (RFC3339). Never follows.
func (c *Client) Logs(
	ctx context.Context,
	containerID, since string,
	tail int,
) ([]LogLine, error) {
	resp, err := c.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Since:      since,
		Timestamps: true,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return nil, fmt.Errorf("tailing container logs: %w", err)
	}

	defer func() {
		_ = resp.Close()
	}()

	lines, err := parseLogStream(resp)
	if err != nil {
		return nil, err
	}

	return lines, nil
}

// parseLogStream demuxes a Docker log stream into timestamped lines.
// TTY containers emit raw (non-multiplexed) bytes, handled as stdout.
func parseLogStream(stream io.Reader) ([]LogLine, error) {
	raw, err := io.ReadAll(stream)
	if err != nil {
		return nil, fmt.Errorf("reading log stream: %w", err)
	}

	var stdout, stderr bytes.Buffer

	if _, err := stdcopy.StdCopy(&stdout, &stderr, bytes.NewReader(raw)); err != nil {
		stdout.Reset()
		stdout.Write(raw)
		stderr.Reset()
	}

	lines := []LogLine{}
	lines = appendLogBuffer(lines, stdout.String(), "stdout")
	lines = appendLogBuffer(lines, stderr.String(), "stderr")

	return lines, nil
}

// appendLogBuffer splits one stream buffer into lines.
func appendLogBuffer(lines []LogLine, buffer, name string) []LogLine {
	for line := range strings.Lines(buffer) {
		trimmed := strings.TrimSuffix(line, "\n")
		if trimmed == "" {
			continue
		}

		moment, message := splitLogLine(trimmed)
		lines = append(lines, LogLine{Timestamp: moment, Stream: name, Message: message})
	}

	return lines
}

// splitLogLine cuts a "timestamp message" line, keeping the whole line
// with a zero time when the stamp does not parse.
func splitLogLine(trimmed string) (time.Time, string) {
	stamp, message, _ := strings.Cut(trimmed, " ")

	moment, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}, trimmed
	}

	return moment, message
}
