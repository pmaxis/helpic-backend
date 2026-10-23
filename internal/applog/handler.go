package applog

import (
	"context"
	"encoding"
	"fmt"
	"log/slog"
	"time"
)

// Handler is a slog.Handler that passes every record on to the next handler
// and copies the warnings and errors to a Sink.
type Handler struct {
	next  slog.Handler
	sink  *Sink
	attrs []slog.Attr
	group []string
}

// NewHandler wraps next.
func NewHandler(next slog.Handler, sink *Sink) *Handler {
	return &Handler{next: next, sink: sink}
}

// Enabled is on for warnings and errors, which are kept whatever next takes,
// and for what next takes.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn || h.next.Enabled(ctx, level)
}

// Handle writes the record to next and, from WARN up, to the sink.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		level := LevelWarn
		if r.Level >= slog.LevelError {
			level = LevelError
		}
		at := r.Time
		if at.IsZero() {
			at = time.Now()
		}
		attrs := map[string]any{}
		for _, a := range h.attrs {
			put(h.target(attrs), a)
		}
		r.Attrs(func(a slog.Attr) bool {
			put(h.target(attrs), a)
			return true
		})
		// The route says which endpoint; the path may carry a link's token.
		if _, ok := attrs["route"]; ok {
			delete(attrs, "path")
		}
		h.sink.add(Entry{At: at, Level: level, Message: r.Message, Attrs: attrs})
	}
	if !h.next.Enabled(ctx, r.Level) {
		return nil
	}
	return h.next.Handle(ctx, r)
}

// WithAttrs returns a handler that adds attrs to every record.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.next = h.next.WithAttrs(attrs)
	c.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &c
}

// WithGroup returns a handler that puts the record's attributes in a group.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.next = h.next.WithGroup(name)
	c.group = append(append([]string{}, h.group...), name)
	return &c
}

// target is the map the record's attributes go in: attrs, or its nested
// group.
func (h *Handler) target(attrs map[string]any) map[string]any {
	m := attrs
	for _, g := range h.group {
		inner, ok := m[g].(map[string]any)
		if !ok {
			inner = map[string]any{}
			m[g] = inner
		}
		m = inner
	}
	return m
}

// put adds a to m as a JSON-friendly value.
func put(m map[string]any, a slog.Attr) {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		inner := map[string]any{}
		for _, ga := range v.Group() {
			put(inner, ga)
		}
		if a.Key == "" {
			for k, iv := range inner {
				m[k] = iv
			}
			return
		}
		m[a.Key] = inner
		return
	}
	if a.Key == "" {
		return
	}
	m[a.Key] = value(v)
}

func value(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format(time.RFC3339Nano)
	}
	switch x := v.Any().(type) {
	case error:
		return x.Error()
	case fmt.Stringer:
		return x.String()
	case encoding.TextMarshaler:
		if b, err := x.MarshalText(); err == nil {
			return string(b)
		}
	}
	return fmt.Sprint(v.Any())
}
