package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// BracketHandler writes log records in the format:
//
//	[time] [LEVEL] message [key=value] [key=value] ...
type BracketHandler struct {
	mu    sync.Mutex
	w     io.Writer
	level slog.Level
	attrs []slog.Attr
}

// NewBracketHandler returns a Handler that writes to w at or above level.
func NewBracketHandler(w io.Writer, level slog.Level) *BracketHandler {
	return &BracketHandler{w: w, level: level}
}

func (h *BracketHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *BracketHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "[%s] [%s] %s", r.Time.UTC().Format(time.RFC3339), r.Level, r.Message)

	for _, a := range h.attrs {
		fmt.Fprintf(&buf, " [%s=%v]", a.Key, a.Value)
	}
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&buf, " [%s=%v]", a.Key, a.Value)
		return true
	})

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

func (h *BracketHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(merged, h.attrs)
	copy(merged[len(h.attrs):], attrs)
	return &BracketHandler{w: h.w, level: h.level, attrs: merged}
}

func (h *BracketHandler) WithGroup(_ string) slog.Handler {
	// Group support is not required for this use case; return self unchanged.
	return h
}
