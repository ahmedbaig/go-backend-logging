package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type Option func(*settings)

type settings struct {
	environment string
	version     string
	inserter    Inserter
}

type Inserter interface {
	Insert(ctx context.Context, collection string, docs []any) error
	Close(ctx context.Context) error
}

type ctxKey struct{}

type scopeKey struct{}

type scope struct {
	mu    sync.Mutex
	attrs []slog.Attr
}

type redactor map[string]struct{}

type contextHandler struct {
	next   slog.Handler
	redact redactor
}

type mongoStore struct {
	client *mongo.Client
	db     *mongo.Database
}

type entry struct {
	coll string
	doc  bson.M
}

type sink struct {
	ins      Inserter
	in       chan entry
	batch    int
	flush    time.Duration
	dropped  atomic.Int64
	lastWarn time.Time
	closing  chan struct{}
	done     chan struct{}
	once     sync.Once
}

type groupedAttr struct {
	path []string
	attr slog.Attr
}

type mongoHandler struct {
	sink   *sink
	min    slog.Leveler
	base   bson.M
	attrs  []groupedAttr
	groups []string
}

type DailyFileOptions struct {
	Dir      string
	Pattern  string
	Symlink  string
	Compress bool
	MaxFiles int
	Now      func() time.Time
}

type DailyFile struct {
	opts DailyFileOptions
	mu   sync.Mutex
	day  string
	f    *os.File
	wg   sync.WaitGroup
}

type fanout struct {
	handlers []slog.Handler
}

type consoleHandler struct {
	mu    *sync.Mutex
	w     io.Writer
	app   string
	min   slog.Leveler
	color bool
	attrs []slog.Attr
	group string
}

type Config struct {
	LogDirectory    string      `json:"logDirectory"`
	ZippedArchive   bool        `json:"zippedArchive"`
	MinimumLogLevel string      `json:"minimumLogLevel" validate:"omitempty,oneof=error warn info debug verbose"`
	LogToFile       bool        `json:"logToFile"`
	LogToConsole    bool        `json:"logToConsole"`
	MaxFiles        int         `json:"maxFiles" validate:"min=0"`
	ConsoleColor    *bool       `json:"consoleColor,omitempty"`
	LogToMongo      bool        `json:"logToMongo"`
	Mongo           MongoConfig `json:"mongo"`
	RedactKeys      []string    `json:"redactKeys"`
}

type MongoConfig struct {
	URI           string `json:"uri"`
	Database      string `json:"database"`
	RetentionDays int    `json:"retentionDays" validate:"min=0"`
	BatchSize     int    `json:"batchSize" validate:"min=0"`
	BufferSize    int    `json:"bufferSize" validate:"min=0"`
	FlushMs       int    `json:"flushMs" validate:"min=0"`
}

type Logger struct {
	*slog.Logger
	closers []io.Closer
}

type discard struct{}
