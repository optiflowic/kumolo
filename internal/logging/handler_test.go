package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fixedTime = time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)

func record(level slog.Level, msg string, args ...any) slog.Record {
	r := slog.NewRecord(fixedTime, level, msg, 0)
	r.Add(args...)
	return r
}

func TestBracketHandler_Handle(t *testing.T) {
	t.Run("formats time, level, and message", func(t *testing.T) {
		var buf bytes.Buffer
		h := NewBracketHandler(&buf, slog.LevelInfo)
		require.NoError(t, h.Handle(context.Background(), record(slog.LevelInfo, "hello")))
		assert.Equal(t, "[2026-04-01T12:00:00Z] [INFO] hello\n", buf.String())
	})

	t.Run("appends record attributes as key=value", func(t *testing.T) {
		var buf bytes.Buffer
		h := NewBracketHandler(&buf, slog.LevelInfo)
		require.NoError(t, h.Handle(context.Background(), record(slog.LevelInfo, "msg", "k", "v")))
		assert.Contains(t, buf.String(), " k=v")
	})

	t.Run("appends pre-set attrs from WithAttrs", func(t *testing.T) {
		var buf bytes.Buffer
		h := NewBracketHandler(&buf, slog.LevelInfo).
			WithAttrs([]slog.Attr{slog.String("svc", "kumolo")})
		require.NoError(t, h.Handle(context.Background(), record(slog.LevelInfo, "msg")))
		assert.Contains(t, buf.String(), " svc=kumolo")
	})

	t.Run("returns write error", func(t *testing.T) {
		h := NewBracketHandler(new(failWriter), slog.LevelInfo)
		err := h.Handle(context.Background(), record(slog.LevelInfo, "msg"))
		assert.Error(t, err)
	})
}

func TestBracketHandler_Enabled(t *testing.T) {
	h := NewBracketHandler(new(bytes.Buffer), slog.LevelWarn)
	assert.False(t, h.Enabled(context.Background(), slog.LevelInfo))
	assert.True(t, h.Enabled(context.Background(), slog.LevelWarn))
	assert.True(t, h.Enabled(context.Background(), slog.LevelError))
}

func TestBracketHandler_WithAttrs(t *testing.T) {
	t.Run("does not mutate original handler", func(t *testing.T) {
		var buf bytes.Buffer
		orig := NewBracketHandler(&buf, slog.LevelInfo)
		child := orig.WithAttrs([]slog.Attr{slog.String("k", "v")})

		require.NoError(t, orig.Handle(context.Background(), record(slog.LevelInfo, "msg")))
		assert.False(
			t,
			strings.Contains(buf.String(), " k=v"),
			"original should not have child attr",
		)

		buf.Reset()
		require.NoError(t, child.Handle(context.Background(), record(slog.LevelInfo, "msg")))
		assert.True(t, strings.Contains(buf.String(), " k=v"), "child should have attr")
	})
}

func TestBracketHandler_WithGroup(t *testing.T) {
	t.Run("returns same handler (group not supported)", func(t *testing.T) {
		h := NewBracketHandler(new(bytes.Buffer), slog.LevelInfo)
		assert.Equal(t, h, h.WithGroup("grp"))
	})
}

// failWriter is an io.Writer that always returns an error.
type failWriter struct{}

func (f *failWriter) Write(_ []byte) (int, error) {
	return 0, assert.AnError
}
