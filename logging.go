package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

const (
	LevelVerbose = slog.Level(-8)
	LevelDebug   = slog.LevelDebug
	LevelInfo    = slog.LevelInfo
	LevelWarn    = slog.LevelWarn
	LevelError   = slog.LevelError
)

type Config struct {
	LogDirectory    string `json:"logDirectory"`
	ZippedArchive   bool   `json:"zippedArchive"`
	MinimumLogLevel string `json:"minimumLogLevel" validate:"omitempty,oneof=error warn info debug verbose"`
	LogToFile       bool   `json:"logToFile"`
	LogToConsole    bool   `json:"logToConsole"`
	MaxFiles        int    `json:"maxFiles" validate:"min=0"`
	ConsoleColor    *bool  `json:"consoleColor,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		LogDirectory:    "/app/var/logs",
		MinimumLogLevel: "verbose",
		LogToFile:       true,
		LogToConsole:    true,
		MaxFiles:        14,
	}
}

func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "verbose", "trace":
		return LevelVerbose, nil
	case "debug":
		return LevelDebug, nil
	case "", "info":
		return LevelInfo, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	}
	return 0, fmt.Errorf("unknown log level %q", s)
}

func LevelName(l slog.Level) string {
	switch {
	case l <= LevelVerbose:
		return "verbose"
	case l < LevelInfo:
		return "debug"
	case l < LevelWarn:
		return "info"
	case l < LevelError:
		return "warn"
	}
	return "error"
}

type Logger struct {
	*slog.Logger
	closers []io.Closer
}

func New(cfg Config, app string) (*Logger, error) {
	min, err := ParseLevel(cfg.MinimumLogLevel)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()

	var handlers []slog.Handler
	var closers []io.Closer

	if cfg.LogToConsole {
		color := isTerminal(os.Stdout)
		if cfg.ConsoleColor != nil {
			color = *cfg.ConsoleColor
		}
		handlers = append(handlers, NewConsoleHandler(os.Stdout, app, min, color))
	}

	if cfg.LogToFile {
		for _, f := range []struct {
			name  string
			level slog.Level
		}{{"verbose", min}, {"error", LevelError}} {
			w, err := NewDailyFile(DailyFileOptions{
				Dir:      cfg.LogDirectory,
				Pattern:  "%DATE%_" + app + "_" + host + "_" + f.name + ".log",
				Symlink:  app + "_" + f.name + ".log",
				Compress: cfg.ZippedArchive,
				MaxFiles: cfg.MaxFiles,
			})
			if err != nil {
				closeAll(closers)
				return nil, err
			}
			closers = append(closers, w)
			handlers = append(handlers, NewJSONHandler(w, app, f.level))
		}
	}

	var h slog.Handler = discard{}
	if len(handlers) > 0 {
		h = Fanout(handlers...)
	}
	return &Logger{Logger: slog.New(NewContextHandler(h)), closers: closers}, nil
}

func (l *Logger) Verbose(msg string, args ...any) {
	l.Log(context.Background(), LevelVerbose, msg, args...)
}

func (l *Logger) VerboseContext(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, LevelVerbose, msg, args...)
}

func (l *Logger) Close() error {
	return closeAll(l.closers)
}

func closeAll(cs []io.Closer) error {
	var errs []error
	for _, c := range cs {
		errs = append(errs, c.Close())
	}
	return errors.Join(errs...)
}

func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	return slog.Any("error", err)
}

type discard struct{}

func (discard) Enabled(context.Context, slog.Level) bool  { return false }
func (discard) Handle(context.Context, slog.Record) error { return nil }
func (d discard) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discard) WithGroup(string) slog.Handler           { return d }

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
