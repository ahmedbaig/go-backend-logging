package logging

import (
	"log/slog"
	"time"
)

const (
	LevelVerbose = slog.Level(-8)
	LevelDebug   = slog.LevelDebug
	LevelInfo    = slog.LevelInfo
	LevelWarn    = slog.LevelWarn
	LevelError   = slog.LevelError
)

const (
	CollectionLogs = "logs"
	CollectionDAU  = "dau"

	FieldUserID = "userId"
	FieldEvent  = "event"
	FieldRoute  = "route"
	FieldStatus = "status"

	Redacted = "[redacted]"

	dateLayout = "2006-01-02"
)

const (
	DefaultRetentionDays = 90
	DefaultBatchSize     = 200
	DefaultBufferSize    = 10000
	DefaultFlushInterval = time.Second
	mongoSetupTimeout    = 5 * time.Second
	mongoWriteTimeout    = 10 * time.Second
)

var DefaultRedactKeys = []string{
	"password", "newPassword", "currentPassword", "oldPassword", "confirmPassword", "passwordHash", "hash",
	"authorization", "cookie", "set-cookie", "token", "accessToken", "refreshToken", "idToken",
	"x-user-token", "x-refresh-token", "serviceToken", "secret", "clientSecret", "apiKey", "x-api-key",
	"privateKey", "otp",
}
