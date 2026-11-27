package log

import (
	"context"
	"log/slog"
)

type contextKey int

const (
	traceIDKey contextKey = iota
	userIDKey
)

// WithTraceID returns a copy of ctx carrying the trace ID that the context
// Handler writes as "trace_id".
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// TraceID returns the trace ID carried by ctx, or "" when there is none.
func TraceID(ctx context.Context) string {
	id, _ := ctx.Value(traceIDKey).(string)
	return id
}

// WithUserID returns a copy of ctx carrying the user identifier that the
// context Handler writes as "user_id".
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// NewContextHandler wraps h so that every record logged with a context
// carries that context's trace_id and user_id, when set. They stay top-level
// fields even on a logger with groups.
func NewContextHandler(h slog.Handler) slog.Handler {
	return &contextHandler{root: h, current: h}
}

// contextHandler remembers the WithAttrs/WithGroup calls made on it so that,
// when the context carries fields, it can add them to the root handler
// before replaying those calls.
type contextHandler struct {
	root    slog.Handler
	current slog.Handler // root with steps applied
	steps   []func(slog.Handler) slog.Handler
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.current.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error {
	var attrs []slog.Attr
	if id, ok := ctx.Value(traceIDKey).(string); ok && id != "" {
		attrs = append(attrs, slog.String("trace_id", id))
	}
	if id, ok := ctx.Value(userIDKey).(string); ok && id != "" {
		attrs = append(attrs, slog.String("user_id", id))
	}
	if len(attrs) == 0 {
		return h.current.Handle(ctx, r)
	}
	out := h.root.WithAttrs(attrs)
	for _, step := range h.steps {
		out = step(out)
	}
	return out.Handle(ctx, r)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.with(func(next slog.Handler) slog.Handler { return next.WithAttrs(attrs) })
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return h.with(func(next slog.Handler) slog.Handler { return next.WithGroup(name) })
}

func (h *contextHandler) with(step func(slog.Handler) slog.Handler) slog.Handler {
	return &contextHandler{
		root:    h.root,
		current: step(h.current),
		steps:   append(h.steps[:len(h.steps):len(h.steps)], step),
	}
}
