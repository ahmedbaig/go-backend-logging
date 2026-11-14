# go-backend-logging

Structured logging for Go services on top of the standard library's `log/slog`. Part of the `go-backend-*` family.

- **Console**: one readable line per entry, `2026-01-02T10:00:00.000+11:00 [my-app] [info] message key=value`, coloured when stdout is a terminal.
- **Files**: JSON lines, rotated daily as `<date>_<app>_<host>_<level>.log`, with a stable symlink `<app>_<level>.log` pointing at today's file. There are two files: `verbose` gets everything at or above the minimum level, `error` gets errors only. Old files can be gzipped and pruned.
- **Levels**: `error`, `warn`, `info`, `debug` and an extra `verbose` level below debug.
- **Context**: attributes stored on a `context.Context` (request ID, correlation ID, user, ...) are added to every entry logged with that context.

## Install

```sh
go get github.com/ahmedbaig/go-backend-logging
```

## Usage

```go
cfg := logging.DefaultConfig()
log, err := logging.New(cfg, "my-app")
if err != nil {
	panic(err)
}
defer log.Close()

log.Info("server started", "port", 8080)
log.Verbose("cache warm", "entries", 1200)
log.Error("payment failed", logging.Err(err))

ctx = logging.WithAttrs(ctx, slog.String("requestId", id))
log.InfoContext(ctx, "handled")
```

`Logger` embeds `*slog.Logger`, so it can be passed anywhere a `*slog.Logger` is expected (`log.Logger`).

## Config

Loaded as JSON, typically with [go-backend-config](https://github.com/ahmedbaig/go-backend-config) under the name `<app>-logger`.

| Key | Default | |
|---|---|---|
| `logDirectory` | `/app/var/logs` | where log files go |
| `minimumLogLevel` | `verbose` | `error`, `warn`, `info`, `debug`, `verbose` |
| `logToFile` | `true` | write the JSON files |
| `logToConsole` | `true` | write console lines to stdout |
| `zippedArchive` | `false` | gzip yesterday's files after rotation |
| `maxFiles` | `14` | files kept per level (0 = keep all) |
| `consoleColor` | auto | force colour on or off |

## Building blocks

The pieces used by `New` are exported for custom setups: `NewConsoleHandler`, `NewJSONHandler`, `NewContextHandler`, `Fanout` (send one record to several handlers) and `NewDailyFile` (an `io.WriteCloser` with daily rotation).

## License

MIT
