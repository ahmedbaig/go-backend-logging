package logging

import (
	"context"
	"log/slog"
)

type ctxKey struct{}

func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	existing, _ := ctx.Value(ctxKey{}).([]slog.Attr)
	merged := make([]slog.Attr, 0, len(existing)+len(attrs))
	merged = append(merged, existing...)
	merged = append(merged, attrs...)
	return context.WithValue(ctx, ctxKey{}, merged)
}

func AttrsFrom(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}
	a, _ := ctx.Value(ctxKey{}).([]slog.Attr)
	return a
}

type contextHandler struct {
	next slog.Handler
}

func NewContextHandler(next slog.Handler) slog.Handler {
	return contextHandler{next: next}
}

func (h contextHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	if attrs := AttrsFrom(ctx); len(attrs) > 0 {
		r = r.Clone()
		r.AddAttrs(attrs...)
	}
	return h.next.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return contextHandler{next: h.next.WithAttrs(as)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{next: h.next.WithGroup(name)}
}
