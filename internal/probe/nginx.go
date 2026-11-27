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

var serverLine = regexp.MustCompile(`^\s*server\s+([^\s;]+)(?:\s+[^;]*)?;\s*$`)

// ErrAmbiguousMarker reports more than one active upstream directive.
var ErrAmbiguousMarker = errors.New("probe: multiple active upstream markers")

// LiveTarget reads the endpoint from the only marked server directive.
// Upstream options follow the endpoint and do not affect its identity.
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

	var target string

	for scanner.Scan() {
		directive, comment, ok := strings.Cut(scanner.Text(), "#")
		if !ok || !strings.Contains("#"+comment, marker) {
			continue
		}

		match := serverLine.FindStringSubmatch(directive)
		if match == nil {
			return "", ErrMarkerNotFound
		}

		if target != "" {
			return "", ErrAmbiguousMarker
		}

		target = match[1]
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading nginx conf: %w", err)
	}

	if target == "" {
		return "", fmt.Errorf("%w in %s", ErrMarkerNotFound, confPath)
	}

	return target, nil
}
