package nginxlog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const (
	maxRecordsPerRead  = 10_000
	maxTimingLineBytes = 64 * 1024
)

// Tailer reads complete JSONL records from a persisted file identity/offset.
type Tailer struct {
	path   string
	parser Parser
	source string
	cursor model.CollectorCursor
}

// NewTailer constructs a tailer from a cursor loaded by the service layer.
func NewTailer(path string, parser Parser, source string, cursor model.CollectorCursor) *Tailer {
	if cursor.Source == "" {
		cursor.Source = source
	}
	return &Tailer{path: path, parser: parser, source: source, cursor: cursor}
}

// Read returns a bounded batch of complete new observations plus the cursor to
// persist after aggregate persistence succeeds. Incomplete final lines remain
// unread, so the next poll can retry them after the writer appends a newline.
func (t *Tailer) Read(ctx context.Context) ([]model.HTTPObservation, model.CollectorCursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, t.cursor, err
	}

	file, err := os.Open(t.path)
	if err != nil {
		cursor := t.cursor
		cursor.LastError = "source_unavailable"
		cursor.UpdatedAt = time.Now().UTC()
		return nil, cursor, errors.New("opening nginx timing source")
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, t.cursor, errors.New("stating nginx timing source")
	}
	identity, err := fileIdentity(info)
	if err != nil {
		return nil, t.cursor, err
	}

	cursor := t.cursor
	cursor.Source = t.source
	if cursor.Identity != "" && cursor.Identity != identity {
		cursor.Offset = 0
		cursor.DroppedRecords++
	}
	cursor.Identity = identity
	if info.Size() < cursor.Offset {
		cursor.Offset = 0
		cursor.DroppedRecords++
	}
	if _, err := file.Seek(cursor.Offset, io.SeekStart); err != nil {
		return nil, cursor, errors.New("seeking nginx timing source")
	}
	cursor.LastError = ""

	observations := []model.HTTPObservation{}
	reader := bufio.NewReaderSize(file, maxTimingLineBytes)

	for range maxRecordsPerRead {
		if err := ctx.Err(); err != nil {
			return observations, cursor, err
		}
		lineStart := cursor.Offset

		line, bytesRead, oversized, readErr := readTimingLine(ctx, reader)
		if errors.Is(readErr, io.EOF) {
			cursor.Offset = lineStart
			break
		}
		if readErr != nil {
			if err := ctx.Err(); err != nil {
				return observations, cursor, err
			}
			cursor.LastError = "read_error"
			cursor.UpdatedAt = time.Now().UTC()
			return observations, cursor, errors.New("reading nginx timing source")
		}

		cursor.Offset = lineStart + bytesRead
		if oversized {
			cursor.DroppedRecords++
			cursor.LastError = "line_too_large"

			continue
		}

		observation, parseErr := t.parser.Parse(line)
		if parseErr != nil {
			cursor.DroppedRecords++
			cursor.LastError = "parse_error"
			continue
		}
		observations = append(observations, observation)
		if observation.Timestamp.After(cursor.LastRecordedAt) {
			cursor.LastRecordedAt = observation.Timestamp
		}
	}

	now := time.Now().UTC()
	cursor.LastSuccessAt = now
	cursor.UpdatedAt = now
	return observations, cursor, nil
}

// Acknowledge advances the in-memory cursor only after the caller has durably
// persisted both the aggregate batch and the returned cursor.
func (t *Tailer) Acknowledge(cursor model.CollectorCursor) {
	t.cursor = cursor
}

func readTimingLine(ctx context.Context, reader *bufio.Reader) ([]byte, int64, bool, error) {
	var bytesRead int64

	oversized := false

	for {
		if err := ctx.Err(); err != nil {
			return nil, bytesRead, oversized, err
		}

		fragment, err := reader.ReadSlice('\n')
		bytesRead += int64(len(fragment))

		switch {
		case err == nil:
			if oversized {
				return nil, bytesRead, true, nil
			}

			return fragment[:len(fragment)-1], bytesRead, false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			oversized = true
			continue
		case errors.Is(err, io.EOF):
			return nil, bytesRead, oversized, io.EOF
		default:
			return nil, bytesRead, oversized, err
		}
	}
}

func fileIdentity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("reading nginx timing file identity")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
