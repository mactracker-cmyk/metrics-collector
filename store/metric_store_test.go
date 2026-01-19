package store

import (
    "bytes"
    "context"
    "encoding/binary"
    "fmt"
    "testing"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestAddMetric(t *testing.T) {
    pool, err := pgxpool.New(context.Background(), "postgres://postgres:pass@localhost:5432/metricsdb?sslmode=disable")
    require.NoError(t, err)
    defer pool.Close()

    ms := NewMetricStore(pool, 1*time.Second)
    defer ms.StopFlush()

    ts := time.Now().UnixNano()
    name := "cpu_usage"
    nameLen := byte(len(name))
    value := 75.5

    var buf bytes.Buffer
    binary.Write(&buf, binary.BigEndian, ts)
    buf.WriteByte(nameLen)
    buf.WriteString(name)
    binary.Write(&buf, binary.BigEndian, value)

    err = ms.AddMetric(buf.Bytes())
    assert.NoError(t, err)
    assert.Equal(t, int64(1), ms.pending)

    ms.flushToDB()
}

func TestInvalidData(t *testing.T) {
    ms := NewMetricStore(nil, 0)
    err := ms.AddMetric([]byte{1, 2, 3})
    assert.Error(t, err)
}

func BenchmarkAddMetric(b *testing.B) {
    ms := NewMetricStore(nil, 0)
    ts := time.Now().UnixNano()
    name := "test"
    nameLen := byte(len(name))
    value := 1.0
    var buf bytes.Buffer
    binary.Write(&buf, binary.BigEndian, ts)
    buf.WriteByte(nameLen)
    buf.WriteString(name)
    binary.Write(&buf, binary.BigEndian, value)
    data := buf.Bytes()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _ = ms.AddMetric(data)
    }
}
