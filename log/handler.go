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

// WithUserID returns a copy of ctx carrying the user identifier that the
// context Handler writes as "user_id".
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// NewContextHandler wraps h so that every record logged with a context
// carries that context's trace_id and user_id, when set.
func NewContextHandler(h slog.Handler) slog.Handler {
	return contextHandler{h}
}

type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := ctx.Value(traceIDKey).(string); ok && id != "" {
		r.AddAttrs(slog.String("trace_id", id))
	}
	if id, ok := ctx.Value(userIDKey).(string); ok && id != "" {
		r.AddAttrs(slog.String("user_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
