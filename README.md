# go-backend-logging

Structured logging for Go services on top of the standard library's `log/slog`. Part of the `go-backend-*` family.

- **Console**: one readable line per entry, `2026-01-02T10:00:00.000+11:00 [my-app] [info] message key=value`, coloured when stdout is a terminal.
- **Files**: JSON lines, rotated daily as `<date>_<app>_<host>_<level>.log`, with a stable symlink `<app>_<level>.log` pointing at today's file. There are two files: `verbose` gets everything at or above the minimum level, `error` gets errors only. Old files can be gzipped and pruned.
- **Levels**: `error`, `warn`, `info`, `debug` and an extra `verbose` level below debug.
- **Context**: attributes stored on a `context.Context` (request ID, correlation ID, user, ...) are added to every entry logged with that context. `WithScope` starts a per-request bag, so attributes added deeper in the chain (the user, set by auth middleware) also reach entries logged further out, such as the access log.
- **MongoDB**: every entry goes to the `logs` collection, written in the background in batches. Signed-in requests and `Event` calls also become rows in the `dau` time-series collection for daily active users. Both expire after `retentionDays` (default 90).
- **Redaction**: keys such as `password`, `authorization`, `cookie`, `refreshToken`, `secret` (see `DefaultRedactKeys`, plus `redactKeys` from config) are replaced with `[redacted]` in every output, including nested groups.

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

log, err = logging.New(cfg, "my-app", logging.WithMeta("production", "1.4.0"))
logging.Event(ctx, log.Logger, "weather.viewed", slog.String("city", "Sydney"))
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
| `logToMongo` | `false` | write every entry to MongoDB |
| `mongo.uri`, `mongo.database` | | connection; collections `logs` and `dau` are created with indexes and TTL on start |
| `mongo.retentionDays` | `90` | TTL for both collections |
| `mongo.batchSize`, `mongo.bufferSize`, `mongo.flushMs` | `200`, `10000`, `1000` | batching; when the buffer is full entries are dropped and counted, requests never wait on Mongo |
| `redactKeys` | | extra keys to redact |

## MongoDB documents

`logs`: `_id`, `timestamp`, `level`, `message`, `service`, `environment`, `version`, `host`, then every attribute of the entry (`requestId`, `correlationId`, `ip`, `userAgent`, `userId`, `email`, `roles`, `sid`, `method`, `route`, `status`, `durationMs`, `error`, `stack`, ...).

`dau` (time-series, `meta` = `{userId, service, environment}`): one row per access-log entry of a signed-in request (`action` = route) and per `Event` (`action` = event name), with `status` and `logId` pointing back to the `logs` entry. Unique users per day: group by day and `$addToSet: "$meta.userId"`. Anonymous requests are never counted.

## Building blocks

The pieces used by `New` are exported for custom setups: `NewConsoleHandler`, `NewJSONHandler`, `NewContextHandler`, `Fanout` (send one record to several handlers) and `NewDailyFile` (an `io.WriteCloser` with daily rotation).

## License

MIT
