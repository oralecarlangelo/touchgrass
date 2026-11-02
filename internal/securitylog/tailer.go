package securitylog

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
	maxEventsPerRead  = 1000
	maxEventLineBytes = 4096
)

// Tailer reads complete JSONL security events from a persisted identity and
// offset. Cursor acknowledgement happens only after durable evaluation.
type Tailer struct {
	path   string
	parser Parser
	source string
	cursor model.CollectorCursor
}

// NewTailer constructs a security-event tailer.
func NewTailer(path string, parser Parser, source string, cursor model.CollectorCursor) *Tailer {
	if cursor.Source == "" {
		cursor.Source = source
	}
	return &Tailer{path: path, parser: parser, source: source, cursor: cursor}
}

// Read returns a bounded batch plus the cursor that covers it. Incomplete
// final lines stay unread until the producer appends a newline.
func (t *Tailer) Read(ctx context.Context) ([]model.SecurityEvent, model.CollectorCursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, t.cursor, err
	}
	file, err := os.Open(t.path)
	if err != nil {
		return nil, t.failureCursor("source_unavailable"), errors.New("opening security event source")
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, t.failureCursor("source_unavailable"), errors.New("stating security event source")
	}
	identity, err := securityFileIdentity(info)
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
		return nil, cursor, errors.New("seeking security event source")
	}
	cursor.LastError = ""

	events := []model.SecurityEvent{}
	reader := bufio.NewReaderSize(file, maxEventLineBytes)
	for range maxEventsPerRead {
		lineStart := cursor.Offset
		line, bytesRead, oversized, readErr := readEventLine(ctx, reader)
		if errors.Is(readErr, io.EOF) {
			cursor.Offset = lineStart
			break
		}
		if readErr != nil {
			cursor.LastError = "read_error"
			cursor.UpdatedAt = time.Now().UTC()
			return events, cursor, errors.New("reading security event source")
		}
		cursor.Offset = lineStart + bytesRead
		if oversized {
			cursor.DroppedRecords++
			cursor.LastError = "line_too_large"
			continue
		}
		event, parseErr := t.parser.Parse(line)
		if parseErr != nil {
			cursor.DroppedRecords++
			cursor.LastError = "parse_error"
			continue
		}
		events = append(events, event)
		if event.Timestamp.After(cursor.LastRecordedAt) {
			cursor.LastRecordedAt = event.Timestamp
		}
	}
	now := time.Now().UTC()
	cursor.LastSuccessAt = now
	cursor.UpdatedAt = now
	return events, cursor, nil
}

// Acknowledge advances the in-memory cursor after durable persistence.
func (t *Tailer) Acknowledge(cursor model.CollectorCursor) { t.cursor = cursor }

func (t *Tailer) failureCursor(category string) model.CollectorCursor {
	cursor := t.cursor
	cursor.LastError = category
	cursor.UpdatedAt = time.Now().UTC()
	return cursor
}

func readEventLine(ctx context.Context, reader *bufio.Reader) ([]byte, int64, bool, error) {
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
		case errors.Is(err, io.EOF):
			return nil, bytesRead, oversized, io.EOF
		default:
			return nil, bytesRead, oversized, err
		}
	}
}

func securityFileIdentity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("reading security event file identity")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
