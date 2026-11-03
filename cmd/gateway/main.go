// Command gateway runs the Bales HTTP gateway.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ab1nv/bales/internal/gateway"
	"github.com/ab1nv/bales/internal/telemetry"
)

func main() {
	var (
		addr        = flag.String("addr", "127.0.0.1:8080", "listen address")
		replicas    = flag.String("replicas", "", "comma separated backend base URLs")
		policy      = flag.String("policy", "bales_ect", "routing policy")
		telemetryTo = flag.String("telemetry", "evidence/runs/gateway.csv", "CSV output path or - for stdout")
		qmax        = flag.Float64("qmax", 60000, "admission cap in estimated milliseconds")
		deltaMax    = flag.Float64("delta-max", 250, "hold deadline cap in milliseconds")
		dirBlocks   = flag.Int("dir-blocks", 4096, "directory blocks per replica")
		blockBytes  = flag.Int("block-bytes", 2048, "prefix block size in bytes")
		kvMax       = flag.Int("kv-max", 65536, "KV capacity in tokens per replica")
		tenantSalt  = flag.String("tenant-salt", "change-me", "tenant hash salt")
		metricsTick = flag.Duration("metrics-interval", time.Second, "metrics scrape interval")
	)
	flag.Parse()

	urls := splitURLs(*replicas)
	if len(urls) == 0 {
		log.Fatal("-replicas is required, for example http://127.0.0.1:9001,http://127.0.0.1:9002")
	}
	cfg := gateway.DefaultConfig()
	cfg.Addr = *addr
	cfg.Replicas = urls
	cfg.Policy = *policy
	cfg.QMaxMS = *qmax
	cfg.Coalesce.DeltaMaxMS = *deltaMax
	cfg.DirectoryBlocks = *dirBlocks
	cfg.BlockBytes = *blockBytes
	cfg.BlockTokens = max(1, *blockBytes/4)
	cfg.KVMaxTokens = *kvMax
	cfg.TenantSalt = *tenantSalt
	cfg.MetricsInterval = *metricsTick

	rec, closeRec, err := openTelemetry(*telemetryTo)
	if err != nil {
		log.Fatal(err)
	}
	srv, err := gateway.New(cfg, rec)
	if err != nil {
		closeRec()
		log.Fatal(err)
	}
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()
	log.Printf("bales gateway listening on %s with policy %s and %d replicas", cfg.Addr, cfg.Policy, len(urls))
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	srv.Close()
	closeRec()
}

func splitURLs(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, strings.TrimRight(p, "/"))
		}
	}
	return out
}

func openTelemetry(path string) (*telemetry.Recorder, func(), error) {
	if path == "-" {
		return telemetry.NewRecorder(os.Stdout), func() {}, nil
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	rec := telemetry.NewRecorder(f)
	return rec, func() {
		rec.Close()
		_ = f.Close()
	}, nil
}
