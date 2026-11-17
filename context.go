package logging

import (
	"context"
	"log/slog"
	"strings"
)

func WithScope(ctx context.Context) context.Context {
	if scopeFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, scopeKey{}, &scope{})
}

func WithAttrs(ctx context.Context, attrs ...slog.Attr) context.Context {
	if s := scopeFrom(ctx); s != nil {
		s.add(attrs)
	}
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
	s := scopeFrom(ctx)
	if s == nil {
		return a
	}
	return dedupe(append(append([]slog.Attr{}, a...), s.list()...))
}

func scopeFrom(ctx context.Context) *scope {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(scopeKey{}).(*scope)
	return s
}

func (s *scope) add(attrs []slog.Attr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attrs = append(s.attrs, attrs...)
}

func (s *scope) list() []slog.Attr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]slog.Attr{}, s.attrs...)
}

func dedupe(attrs []slog.Attr) []slog.Attr {
	seen := make(map[string]int, len(attrs))
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if i, ok := seen[a.Key]; ok {
			out[i] = a
			continue
		}
		seen[a.Key] = len(out)
		out = append(out, a)
	}
	return out
}

func NewContextHandler(next slog.Handler, redactKeys ...string) slog.Handler {
	return contextHandler{next: next, redact: redactSet(redactKeys)}
}

func (h contextHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.redact.apply(a))
		return true
	})
	for _, a := range AttrsFrom(ctx) {
		out.AddAttrs(h.redact.apply(a))
	}
	return h.next.Handle(ctx, out)
}

func (h contextHandler) WithAttrs(as []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(as))
	for i, a := range as {
		clean[i] = h.redact.apply(a)
	}
	return contextHandler{next: h.next.WithAttrs(clean), redact: h.redact}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{next: h.next.WithGroup(name), redact: h.redact}
}

func redactSet(extra []string) redactor {
	r := redactor{}
	for _, k := range DefaultRedactKeys {
		r[normalizeKey(k)] = struct{}{}
	}
	for _, k := range extra {
		r[normalizeKey(k)] = struct{}{}
	}
	return r
}

func normalizeKey(k string) string {
	return strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(k))
}

func (r redactor) apply(a slog.Attr) slog.Attr {
	if _, hit := r[normalizeKey(a.Key)]; hit {
		return slog.String(a.Key, Redacted)
	}
	v := a.Value.Resolve()
	if v.Kind() != slog.KindGroup {
		return a
	}
	group := v.Group()
	clean := make([]any, len(group))
	for i, g := range group {
		clean[i] = r.apply(g)
	}
	return slog.Group(a.Key, clean...)
}
