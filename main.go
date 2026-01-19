package main

import (
    "bufio"
    "context"
    "flag"
    "io"
    "log"
    "net"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/spf13/viper"
    "metrics-collector/store"
)

func main() {
    viper.SetDefault("addr", ":8080")
    viper.SetDefault("db_url", "postgres://postgres:pass@localhost:5432/metricsdb?sslmode=disable")
    viper.SetDefault("flush_interval", 10)
    viper.SetConfigName("config")
    viper.AddConfigPath(".")
    viper.AutomaticEnv()

    pool, err := pgxpool.New(context.Background(), viper.GetString("db_url"))
    if err != nil {
        log.Fatal("Failed to connect to DB:", err)
    }
    defer pool.Close()

    flushInt := time.Duration(viper.GetInt("flush_interval")) * time.Second
    storeInst := store.NewMetricStore(pool, flushInt)

    ctx, cancel := context.WithCancel(context.Background())
    storeInst.StartFlush(ctx)

    listener, err := net.Listen("tcp", viper.GetString("addr"))
    if err != nil {
        log.Fatal("Failed to listen:", err)
    }
    defer listener.Close()

    log.Printf("Server listening on %s, flushing every %d sec", viper.GetString("addr"), viper.GetInt("flush_interval"))

    sig := make(chan os.Signal, 1)
    signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-sig
        log.Println("Shutting down...")
        storeInst.StopFlush()
        cancel()
        listener.Close()
    }()

    for {
        conn, err := listener.Accept()
        if err != nil {
            select {
            case <-ctx.Done():
                return
            default:
                log.Printf("Accept error: %v", err)
                continue
            }
        }
        go handleConnection(conn, storeInst)
    }
}

func handleConnection(conn net.Conn, storeInst *store.MetricStore) {
    defer conn.Close()
    reader := bufio.NewReader(conn)
    for {
        header := make([]byte, 9)
        if _, err := io.ReadFull(reader, header); err != nil {
            if err != io.EOF {
                log.Printf("Header read error: %v", err)
            }
            return
        }
        nameLen := int(header[8])
        if nameLen > 255 || nameLen < 0 {
            log.Printf("Invalid name len: %d", nameLen)
            continue
        }
        remaining := make([]byte, nameLen+8)
        if _, err := io.ReadFull(reader, remaining); err != nil {
            log.Printf("Body read error: %v", err)
            return
        }
        data := append(header, remaining...)
        if err := storeInst.AddMetric(data); err != nil {
            log.Printf("Add metric error: %v", err)
        }
    }
}
