package logging

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{"verbose": LevelVerbose, "DEBUG": LevelDebug, "": LevelInfo, "warn": LevelWarn, "error": LevelError} {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseLevel("loud"); err == nil {
		t.Error("expected error")
	}
}

func TestConsoleFormat(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewConsoleHandler(&buf, "svc", LevelVerbose, false))
	l.With("user", "a b").WithGroup("req").Info("hello", "id", 7)
	line := buf.String()
	for _, want := range []string{"[svc] [info] hello", `user="a b"`, "req.id=7"} {
		if !strings.Contains(line, want) {
			t.Errorf("console line %q missing %q", line, want)
		}
	}
}

func TestConsoleRespectsMinimum(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewConsoleHandler(&buf, "svc", LevelWarn, false))
	l.Info("hidden")
	l.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Fatalf("got %q", buf.String())
	}
}

func TestJSONHandlerFields(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewJSONHandler(&buf, "svc", LevelVerbose))
	l.Log(context.Background(), LevelVerbose, "deep", "error", errors.New("boom"))
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["level"] != "verbose" || m["app"] != "svc" || m["msg"] != "deep" {
		t.Fatalf("got %v", m)
	}
	e, _ := m["error"].(map[string]any)
	if e["message"] != "boom" {
		t.Fatalf("error = %v", m["error"])
	}
}

func TestContextAttrs(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewContextHandler(NewJSONHandler(&buf, "svc", LevelInfo)))
	ctx := WithAttrs(context.Background(), slog.String("requestId", "r1"))
	ctx = WithAttrs(ctx, slog.String("correlationId", "c1"))
	l.InfoContext(ctx, "x")
	var m map[string]any
	_ = json.Unmarshal(buf.Bytes(), &m)
	if m["requestId"] != "r1" || m["correlationId"] != "c1" {
		t.Fatalf("got %v", m)
	}
}

func TestNewWritesSplitFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.LogDirectory = dir
	cfg.LogToConsole = false
	l, err := New(cfg, "svc")
	if err != nil {
		t.Fatal(err)
	}
	l.Verbose("v")
	l.Info("i")
	l.Error("e", Err(errors.New("bad")))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	verbose := readLines(t, filepath.Join(dir, "svc_verbose.log"))
	errs := readLines(t, filepath.Join(dir, "svc_error.log"))
	if len(verbose) != 3 || len(errs) != 1 {
		t.Fatalf("verbose=%d error=%d", len(verbose), len(errs))
	}
}

func readLines(t *testing.T, p string) []string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		out = append(out, s.Text())
	}
	return out
}

func TestDailyRotationCompressAndPrune(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	d, err := NewDailyFile(DailyFileOptions{
		Dir: dir, Pattern: "%DATE%_svc.log", Symlink: "svc.log",
		Compress: true, MaxFiles: 2, Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := d.Write([]byte("line\n")); err != nil {
			t.Fatal(err)
		}
		now = now.Add(24 * time.Hour)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	joined := strings.Join(names, ",")
	if strings.Contains(joined, "2026-01-01") || !strings.Contains(joined, "2026-01-04_svc.log") || !strings.Contains(joined, ".gz") {
		t.Fatalf("files = %v", names)
	}
	target, err := os.Readlink(filepath.Join(dir, "svc.log"))
	if err != nil || target != "2026-01-04_svc.log" {
		t.Fatalf("symlink -> %q %v", target, err)
	}
}

func TestPatternNeedsDate(t *testing.T) {
	if _, err := NewDailyFile(DailyFileOptions{Dir: t.TempDir(), Pattern: "x.log"}); err == nil {
		t.Fatal("expected error")
	}
}

type memInserter struct {
	mu   sync.Mutex
	docs map[string][]bson.M
}

func (m *memInserter) Insert(_ context.Context, coll string, docs []any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.docs == nil {
		m.docs = map[string][]bson.M{}
	}
	for _, d := range docs {
		m.docs[coll] = append(m.docs[coll], d.(bson.M))
	}
	return nil
}

func (m *memInserter) Close(context.Context) error { return nil }

func (m *memInserter) get(coll string) []bson.M {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]bson.M{}, m.docs[coll]...)
}

func mongoLogger(t *testing.T) (*Logger, *memInserter) {
	t.Helper()
	ins := &memInserter{}
	l, err := New(Config{MinimumLogLevel: "debug", LogToMongo: true, Mongo: MongoConfig{FlushMs: 10}}, "svc", WithMeta("testing", "1.2.3"), WithInserter(ins))
	if err != nil {
		t.Fatal(err)
	}
	return l, ins
}

func TestMongoEntriesCarryRequestAndUser(t *testing.T) {
	l, ins := mongoLogger(t)
	ctx := WithScope(context.Background())
	ctx = WithAttrs(ctx, slog.String("requestId", "r1"))
	inner := WithAttrs(ctx, slog.Int64("userId", 7), slog.String("email", "a@b.c"))
	l.ErrorContext(inner, "boom", Err(errors.New("bad")), slog.String("stack", "trace"))
	l.InfoContext(ctx, "GET /x 200 1ms", slog.Int("status", 200), slog.String("route", "GET /x"), slog.String("password", "hunter2"))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	logs := ins.get(CollectionLogs)
	if len(logs) != 2 {
		t.Fatalf("logs = %d", len(logs))
	}
	e := logs[0]
	if e["service"] != "svc" || e["environment"] != "testing" || e["version"] != "1.2.3" || e["level"] != "error" || e["stack"] != "trace" {
		t.Fatalf("error entry %v", e)
	}
	if e["error"].(bson.M)["message"] != "bad" {
		t.Fatalf("error = %v", e["error"])
	}
	access := logs[1]
	if access["userId"] != int64(7) || access["requestId"] != "r1" {
		t.Fatalf("access entry misses scope attrs: %v", access)
	}
	if access["password"] != Redacted {
		t.Fatalf("password not redacted: %v", access["password"])
	}

	dau := ins.get(CollectionDAU)
	if len(dau) != 1 || dau[0]["action"] != "GET /x" || dau[0]["meta"].(bson.M)["userId"] != int64(7) || dau[0]["logId"] != access["_id"] {
		t.Fatalf("dau = %v", dau)
	}
}

func TestDAUSkipsAnonymousAndCountsEvents(t *testing.T) {
	l, ins := mongoLogger(t)
	l.Info("GET /x 200 1ms", slog.Int("status", 200), slog.String("route", "GET /x"))
	ctx := WithAttrs(context.Background(), slog.Int64("userId", 3))
	Event(ctx, l.Logger, "weather.viewed", slog.String("city", "Sydney"))
	l.InfoContext(ctx, "plain line")
	_ = l.Close()
	dau := ins.get(CollectionDAU)
	if len(dau) != 1 || dau[0]["action"] != "weather.viewed" {
		t.Fatalf("dau = %v", dau)
	}
	if len(ins.get(CollectionLogs)) != 3 {
		t.Fatal("every line goes to logs")
	}
}

func TestRedactsNestedGroupsInFiles(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(NewContextHandler(NewJSONHandler(&buf, "svc", LevelInfo), "pin"))
	l.Info("x", slog.Group("body", slog.String("refresh_token", "abc"), slog.String("pin", "1234"), slog.String("name", "ok")))
	var m map[string]any
	_ = json.Unmarshal(buf.Bytes(), &m)
	body := m["body"].(map[string]any)
	if body["refresh_token"] != Redacted || body["pin"] != Redacted || body["name"] != "ok" {
		t.Fatalf("body = %v", body)
	}
}

func TestMongoGroupsNest(t *testing.T) {
	l, ins := mongoLogger(t)
	l.WithGroup("req").With(slog.String("method", "GET")).Info("x", slog.Int("n", 1))
	_ = l.Close()
	e := ins.get(CollectionLogs)[0]
	req := e["req"].(bson.M)
	if req["method"] != "GET" || req["n"] != int64(1) {
		t.Fatalf("req = %v", req)
	}
}
