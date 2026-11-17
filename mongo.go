package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func openMongo(c MongoConfig) (*mongoStore, error) {
	if c.URI == "" || c.Database == "" {
		return nil, errors.New("logging: mongo.uri and mongo.database are required when logToMongo is on")
	}
	ctx, cancel := context.WithTimeout(context.Background(), mongoSetupTimeout)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(c.URI).SetAppName("go-backend-logging"))
	if err != nil {
		return nil, fmt.Errorf("logging: mongo: %w", err)
	}
	s := &mongoStore{client: client, db: client.Database(c.Database)}
	if err := s.ensure(ctx, retention(c)); err != nil {
		fmt.Fprintf(os.Stderr, "logging: mongo setup skipped: %v\n", err)
	}
	return s, nil
}

func retention(c MongoConfig) int32 {
	days := c.RetentionDays
	if days <= 0 {
		days = DefaultRetentionDays
	}
	return int32(days * 24 * 60 * 60)
}

func (s *mongoStore) ensure(ctx context.Context, ttl int32) error {
	names, err := s.db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	if !have[CollectionDAU] {
		ts := options.TimeSeries().SetTimeField("timestamp").SetMetaField("meta").SetGranularity("minutes")
		if err := s.db.CreateCollection(ctx, CollectionDAU, options.CreateCollection().SetTimeSeriesOptions(ts).SetExpireAfterSeconds(int64(ttl))); err != nil {
			return err
		}
	}
	_, err = s.db.Collection(CollectionLogs).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "timestamp", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(ttl)},
		{Keys: bson.D{{Key: "service", Value: 1}, {Key: "timestamp", Value: -1}}},
		{Keys: bson.D{{Key: "level", Value: 1}, {Key: "timestamp", Value: -1}}},
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "timestamp", Value: -1}}},
		{Keys: bson.D{{Key: "requestId", Value: 1}}},
		{Keys: bson.D{{Key: "correlationId", Value: 1}}},
	})
	return err
}

func (s *mongoStore) Insert(ctx context.Context, collection string, docs []any) error {
	_, err := s.db.Collection(collection).InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
	return err
}

func (s *mongoStore) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

func newSink(ins Inserter, c MongoConfig) *sink {
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultBatchSize
	}
	if c.BufferSize <= 0 {
		c.BufferSize = DefaultBufferSize
	}
	flush := time.Duration(c.FlushMs) * time.Millisecond
	if flush <= 0 {
		flush = DefaultFlushInterval
	}
	s := &sink{ins: ins, in: make(chan entry, c.BufferSize), batch: c.BatchSize, flush: flush, closing: make(chan struct{}), done: make(chan struct{})}
	go s.run()
	return s
}

func (s *sink) add(collection string, doc bson.M) {
	select {
	case <-s.closing:
		s.dropped.Add(1)
		return
	default:
	}
	select {
	case s.in <- entry{coll: collection, doc: doc}:
	default:
		s.dropped.Add(1)
	}
}

func (s *sink) run() {
	defer close(s.done)
	t := time.NewTicker(s.flush)
	defer t.Stop()
	pending := map[string][]any{}
	n := 0
	flush := func() {
		for coll, docs := range pending {
			ctx, cancel := context.WithTimeout(context.Background(), mongoWriteTimeout)
			if err := s.ins.Insert(ctx, coll, docs); err != nil {
				s.warn(err)
			}
			cancel()
		}
		clear(pending)
		n = 0
	}
	push := func(e entry) {
		pending[e.coll] = append(pending[e.coll], e.doc)
		if n++; n >= s.batch {
			flush()
		}
	}
	for {
		select {
		case e := <-s.in:
			push(e)
		case <-t.C:
			flush()
		case <-s.closing:
			for {
				select {
				case e := <-s.in:
					push(e)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (s *sink) warn(err error) {
	now := time.Now()
	if now.Sub(s.lastWarn) < time.Minute {
		return
	}
	s.lastWarn = now
	fmt.Fprintf(os.Stderr, "logging: mongo write failed (%d dropped so far): %v\n", s.dropped.Load(), err)
}

func (s *sink) Dropped() int64 { return s.dropped.Load() }

func (s *sink) Close() error {
	s.once.Do(func() { close(s.closing) })
	ctx, cancel := context.WithTimeout(context.Background(), mongoSetupTimeout)
	defer cancel()
	select {
	case <-s.done:
	case <-ctx.Done():
	}
	return s.ins.Close(ctx)
}

func newMongoHandler(s *sink, min slog.Leveler, base bson.M) slog.Handler {
	return &mongoHandler{sink: s, min: min, base: base}
}

func (h *mongoHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.min.Level()
}

func (h *mongoHandler) Handle(_ context.Context, r slog.Record) error {
	doc := bson.M{}
	for k, v := range h.base {
		doc[k] = v
	}
	id := bson.NewObjectID()
	doc["_id"] = id
	doc["timestamp"] = r.Time.UTC()
	doc["level"] = LevelName(r.Level)
	doc["message"] = r.Message
	for _, a := range h.attrs {
		setAttr(nested(doc, a.path), a.attr)
	}
	target := nested(doc, h.groups)
	r.Attrs(func(a slog.Attr) bool {
		setAttr(target, a)
		return true
	})
	h.sink.add(CollectionLogs, doc)
	if row := dauRow(doc, id); row != nil {
		h.sink.add(CollectionDAU, row)
	}
	return nil
}

func nested(m bson.M, path []string) bson.M {
	for _, g := range path {
		next, ok := m[g].(bson.M)
		if !ok {
			next = bson.M{}
			m[g] = next
		}
		m = next
	}
	return m
}

func (h *mongoHandler) WithAttrs(as []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append([]groupedAttr{}, h.attrs...)
	for _, a := range as {
		c.attrs = append(c.attrs, groupedAttr{path: h.groups, attr: a})
	}
	return &c
}

func (h *mongoHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.groups = append(append([]string{}, h.groups...), name)
	return &c
}

func setAttr(m bson.M, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Key == "" || a.Equal(slog.Attr{}) {
		return
	}
	m[a.Key] = bsonValue(a.Value)
}

func bsonValue(v slog.Value) any {
	switch v.Kind() {
	case slog.KindGroup:
		m := bson.M{}
		for _, a := range v.Group() {
			setAttr(m, a)
		}
		return m
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return int64(v.Uint64())
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindTime:
		return v.Time().UTC()
	case slog.KindDuration:
		return v.Duration().Milliseconds()
	}
	switch x := v.Any().(type) {
	case error:
		return bson.M{"message": x.Error(), "type": fmt.Sprintf("%T", x)}
	case []string:
		return x
	case []int64:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprintf("%v", x)
	}
}

func dauRow(doc bson.M, id bson.ObjectID) bson.M {
	uid, _ := doc[FieldUserID].(int64)
	if uid == 0 {
		return nil
	}
	action, _ := doc[FieldEvent].(string)
	if action == "" {
		route, _ := doc[FieldRoute].(string)
		if _, isRequest := doc[FieldStatus]; !isRequest || route == "" {
			return nil
		}
		action = route
	}
	row := bson.M{
		"timestamp": doc["timestamp"],
		"meta":      bson.M{FieldUserID: uid, "service": doc["service"], "environment": doc["environment"]},
		"action":    action,
		"logId":     id,
	}
	if st, ok := doc[FieldStatus]; ok {
		row[FieldStatus] = st
	}
	return row
}

func Event(ctx context.Context, log *slog.Logger, name string, attrs ...slog.Attr) {
	log.LogAttrs(ctx, slog.LevelInfo, name, append([]slog.Attr{slog.String(FieldEvent, name)}, attrs...)...)
}
