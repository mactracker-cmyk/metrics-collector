package store

import (
    "bytes"
    "context"
    "encoding/binary"
    "log"
    "strings"
    "sync"
    "sync/atomic"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Metric struct {
    Timestamp time.Time
    Name      string
    Value     float64
}

type MetricStore struct {
    metrics   sync.Map
    pool      *pgxpool.Pool
    flushInt  time.Duration
    flushCtx  context.Context
    flushStop context.CancelFunc
    pending   int64
}

func NewMetricStore(pool *pgxpool.Pool, flushInt time.Duration) *MetricStore {
    ctx, cancel := context.WithCancel(context.Background())
    return &MetricStore{
        pool:      pool,
        flushInt:  flushInt,
        flushCtx:  ctx,
        flushStop: cancel,
    }
}

func (ms *MetricStore) AddMetric(data []byte) error {
    if len(data) < 17 {
        return logFatalf("invalid data length: %d", len(data)) // Используй fmt.Errorf в prod
    }

    var ts int64
    if err := binary.Read(bytes.NewReader(data[:8]), binary.BigEndian, &ts); err != nil {
        return err
    }
    timestamp := time.Unix(0, ts)

    nameLen := data[8]
    if len(data) < int(9+nameLen+8) {
        return logFatalf("invalid name length")
    }
    name := string(data[9 : 9+nameLen])
    var value float64
    if err := binary.Read(bytes.NewReader(data[9+nameLen:]), binary.BigEndian, &value); err != nil {
        return err
    }
  
    if v, loaded := ms.metrics.LoadOrStore(name, make([]Metric, 0, 100)); loaded {
        mlist := v.([]Metric)
        mlist = append(mlist, Metric{Timestamp: timestamp, Name: name, Value: value})
        ms.metrics.Store(name, mlist)
    } else {
        ms.metrics.Store(name, []Metric{{Timestamp: timestamp, Name: name, Value: value}})
    }
    atomic.AddInt64(&ms.pending, 1)
    return nil
}

func (ms *MetricStore) StartFlush(ctx context.Context) {
    ticker := time.NewTicker(ms.flushInt)
    go func() {
        for {
            select {
            case <-ctx.Done():
                ticker.Stop()
                return
            case <-ticker.C:
                if atomic.LoadInt64(&ms.pending) == 0 {
                    continue
                }
                ms.flushToDB()
            }
        }
    }()
}

func (ms *MetricStore) StopFlush() {
    ms.flushStop()
}

func (ms *MetricStore) flushToDB() {
    var rows [][]interface{}
    ms.metrics.Range(func(key, value interface{}) bool {
        name := key.(string)
        mlist := value.([]Metric)
        for _, m := range mlist {
            rows = append(rows, []interface{}{m.Timestamp, name, m.Value})
        }
        ms.metrics.Store(key, make([]Metric, 0))
        return true
    })

    if len(rows) == 0 {
        return
    }

    flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    _, err := ms.pool.CopyFrom(
        flushCtx,
        pgx.Identifier{"metrics"},
        []string{"timestamp", "name", "value"},
        pgx.CopyFromRows(rows),
    )
    if err != nil {
        log.Printf("Flush error: %v", err)
        return
    }

    atomic.AddInt64(&ms.pending, -int64(len(rows)))
    log.Printf("Flushed %d metrics via COPY", len(rows))
}

func logFatalf(format string, v ...interface{}) error { // Helper
    err := fmt.Errorf(format, v...)
    log.Printf("Error: %v", err)
    return err
}
