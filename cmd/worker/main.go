// Command worker is a placeholder: it validates config, serves /healthz on
// :8081 and logs a heartbeat. A later task replaces it with the real runtime.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ainote/internal/config"
	"ainote/internal/logging"
)

func main() {
	cfg, err := config.LoadWorker()
	if err != nil {
		slog.Error("worker config", "error", err)
		os.Exit(1)
	}
	log := logging.New(cfg.LogLevel, "worker")
	host, _ := os.Hostname()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv := &http.Server{Addr: ":8081", Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("health server", "error", err)
			stop()
		}
	}()

	log.Info("worker started", "host", host)
	t := time.NewTicker(cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			log.Info("heartbeat", "host", host)
		case <-ctx.Done():
			log.Info("worker shutting down")
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
			return
		}
	}
}
