// Command gateway is the HTTP entrypoint of the ainote system.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"ainote/internal/api"
	"ainote/internal/config"
	"ainote/internal/health"
	"ainote/internal/jobs"
	"ainote/internal/logging"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("gateway failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadGateway()
	if err != nil {
		return err
	}
	log := logging.New(cfg.Common.LogLevel, "gateway")
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := jobs.Migrate(ctx, cfg.Common.DatabaseURL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	// pgxpool and redis connect lazily; amqp is dialed per probe below so
	// /healthz reports degraded (not a crash) when a dependency is down.
	pool, err := pgxpool.New(ctx, cfg.Common.DatabaseURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	ropts, err := redis.ParseURL(cfg.Common.RedisURL)
	if err != nil {
		return fmt.Errorf("redis url: %w", err)
	}
	rdb := redis.NewClient(ropts)
	defer rdb.Close()

	checker := &health.Checker{
		Version: version,
		Probes: map[string]health.Probe{
			"postgres": func(ctx context.Context) error {
				var one int
				return pool.QueryRow(ctx, "SELECT 1").Scan(&one)
			},
			"rabbitmq": func(ctx context.Context) error { return probeRabbit(cfg.Common.AMQPURL, cfg.Common.AMQPHeartbeat) },
			"redis":    func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.HealthHandler(checker))
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("gateway listening", "addr", srv.Addr, "version", version)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return err
	}
	return nil
}

// probeRabbit opens a connection and channel and passive-declares jobs.work.
// NOT_FOUND (404) means topology is not declared yet, which is reported ok.
func probeRabbit(url string, heartbeat time.Duration) error {
	conn, err := amqp.DialConfig(url, amqp.Config{Heartbeat: heartbeat, Dial: amqp.DefaultDial(2 * time.Second)})
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if _, err := ch.QueueDeclarePassive("jobs.work", true, false, false, false, nil); err != nil {
		var ae *amqp.Error
		if errors.As(err, &ae) && ae.Code == amqp.NotFound {
			return nil
		}
		return err
	}
	return nil
}
