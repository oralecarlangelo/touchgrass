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

// Read returns all complete new observations plus the cursor to persist after
// aggregate persistence succeeds. Incomplete final lines remain unread.
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
	reader := bufio.NewReader(file)
	for {
		if err := ctx.Err(); err != nil {
			return observations, cursor, err
		}
		lineStart := cursor.Offset
		line, readErr := reader.ReadBytes('\n')
		if errors.Is(readErr, io.EOF) {
			cursor.Offset = lineStart
			break
		}
		if readErr != nil {
			cursor.LastError = "read_error"
			cursor.UpdatedAt = time.Now().UTC()
			return observations, cursor, errors.New("reading nginx timing source")
		}
		cursor.Offset += int64(len(line))
		observation, parseErr := t.parser.Parse(line[:len(line)-1])
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
	t.cursor = cursor
	return observations, cursor, nil
}

func fileIdentity(info os.FileInfo) (string, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("reading nginx timing file identity")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
