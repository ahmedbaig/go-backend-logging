package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

func Fanout(hs ...slog.Handler) slog.Handler {
	return &fanout{handlers: hs}
}

func (f *fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f *fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

func (f *fanout) WithAttrs(as []slog.Attr) slog.Handler {
	out := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		out[i] = h.WithAttrs(as)
	}
	return &fanout{handlers: out}
}

func (f *fanout) WithGroup(name string) slog.Handler {
	out := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		out[i] = h.WithGroup(name)
	}
	return &fanout{handlers: out}
}

func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.LevelKey:
		if l, ok := a.Value.Any().(slog.Level); ok {
			return slog.String(slog.LevelKey, LevelName(l))
		}
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			return slog.String(slog.TimeKey, t.Format("2006-01-02T15:04:05.000Z07:00"))
		}
	}
	if err, ok := a.Value.Any().(error); ok {
		return slog.Group(a.Key, slog.String("message", err.Error()), slog.String("type", fmt.Sprintf("%T", err)))
	}
	return a
}

func NewJSONHandler(w io.Writer, app string, min slog.Leveler) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       min,
		ReplaceAttr: replaceAttr,
	}).WithAttrs([]slog.Attr{slog.String("app", app)})
}

func NewConsoleHandler(w io.Writer, app string, min slog.Leveler, color bool) slog.Handler {
	return &consoleHandler{mu: &sync.Mutex{}, w: w, app: app, min: min, color: color}
}

func (h *consoleHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.min.Level()
}

var levelColors = map[string]string{
	"error":   "\033[31m",
	"warn":    "\033[33m",
	"info":    "\033[32m",
	"debug":   "\033[34m",
	"verbose": "\033[36m",
}

func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	lvl := LevelName(r.Level)
	b.WriteString(r.Time.Format("2006-01-02T15:04:05.000Z07:00"))
	b.WriteString(" [" + h.app + "] ")
	if h.color {
		b.WriteString(levelColors[lvl] + "[" + lvl + "]\033[0m")
	} else {
		b.WriteString("[" + lvl + "]")
	}
	b.WriteString(" " + r.Message)
	for _, a := range h.attrs {
		writeAttr(&b, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, h.group, a)
		return true
	})
	b.WriteByte('\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func writeAttr(b *strings.Builder, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, g := range a.Value.Group() {
			writeAttr(b, key, g)
		}
		return
	}
	v := a.Value.String()
	if strings.ContainsAny(v, " \t\n\"=") {
		v = fmt.Sprintf("%q", v)
	}
	b.WriteString(" " + key + "=" + v)
}

func (h *consoleHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr{}, h.attrs...), prefixed(h.group, as)...)
	return &c
}

func (h *consoleHandler) WithGroup(name string) slog.Handler {
	c := *h
	if c.group != "" {
		c.group += "." + name
	} else {
		c.group = name
	}
	return &c
}

func prefixed(group string, as []slog.Attr) []slog.Attr {
	if group == "" {
		return as
	}
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = slog.Attr{Key: group + "." + a.Key, Value: a.Value}
	}
	return out
}
