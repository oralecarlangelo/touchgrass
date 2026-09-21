// Package probe checks service health and detects the live color.
package probe

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ErrMarkerNotFound reports an nginx conf without the active marker line.
var ErrMarkerNotFound = errors.New("probe: active marker not found")

var serverLine = regexp.MustCompile(`server\s+([^;]+);`)

// LiveTarget parses the nginx active target from the marker line. It
// replicates bluegreen-lib.sh nginx_active_target exactly: the first line
// containing the marker, the host:port after "server", whitespace stripped.
func LiveTarget(confPath, marker string) (string, error) {
	//nolint:gosec // confPath is admin-configured service data, never user input.
	file, err := os.Open(confPath)
	if err != nil {
		return "", fmt.Errorf("opening nginx conf: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		if !strings.Contains(scanner.Text(), marker) {
			continue
		}

		match := serverLine.FindStringSubmatch(scanner.Text())
		if match == nil {
			return "", fmt.Errorf("%w in line %q", ErrMarkerNotFound, scanner.Text())
		}

		return strings.ReplaceAll(match[1], " ", ""), nil
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading nginx conf: %w", err)
	}

	return "", fmt.Errorf("%w in %s", ErrMarkerNotFound, confPath)
}
