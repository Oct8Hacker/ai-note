// Command worker consumes jobs.work and runs the pipeline stages for each job.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"

	"ainote/internal/config"
	"ainote/internal/crypto"
	"ainote/internal/jobs"
	"ainote/internal/logging"
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/pipelinetest"
	"ainote/internal/queue"
	"ainote/internal/ratelimit"
	"ainote/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadWorker()
	if err != nil {
		return err
	}
	log := logging.New(cfg.LogLevel, "worker")
	slog.SetDefault(log)
	workerID, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("hostname: %w", err)
	}

	// ctx is the shutdown signal. The heartbeat has its own context so this
	// worker keeps looking alive while in-flight jobs finish their grace period.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()
	repo := jobs.NewRepo(pool)

	ropts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return fmt.Errorf("redis url: %w", err)
	}
	rdb := redis.NewClient(ropts)
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Warn("redis close", "err", err)
		}
	}()

	sealer, err := crypto.NewSealer(cfg.TokenEncKey)
	if err != nil {
		return fmt.Errorf("token sealer: %w", err)
	}

	buckets, notionTmpl, daily := ratelimit.FromWorkerConfig(cfg)
	limiter := ratelimit.New(rdb, buckets, notionTmpl, daily, cfg.LimiterMaxWait, time.Now)

	top := queue.Topology{RetryDelays: cfg.RetryDelays, DeferDelay: cfg.DeferDelay}
	mq, err := queue.Dial(ctx, cfg.AMQPURL, cfg.AMQPHeartbeat, log, func(ch *amqp.Channel) error {
		return queue.Declare(ch, top)
	})
	if err != nil {
		return fmt.Errorf("rabbitmq: %w", err)
	}
	defer func() {
		if err := mq.Close(); err != nil {
			log.Warn("amqp close", "err", err)
		}
	}()

	hbCtx, stopHeartbeat := context.WithCancel(context.WithoutCancel(ctx))
	defer stopHeartbeat()
	worker.StartHeartbeat(hbCtx, rdb, workerID, cfg.HeartbeatInterval, cfg.HeartbeatTTL)

	health := worker.HealthServer(":8081")
	go func() {
		if err := health.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server", "error", err)
			stop()
		}
	}()
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = health.Shutdown(sctx)
	}()

	rt := worker.New(worker.Config{
		WorkerID:            workerID,
		Prefetch:            cfg.WorkerConcurrency,
		LeaseTTL:            cfg.LeaseTTL,
		ShutdownGrace:       cfg.ShutdownGrace,
		MaxAttempts:         cfg.MaxAttempts,
		MaxRateLimitRetries: cfg.MaxRateLimitRetries,
		MaxDeferrals:        cfg.MaxDeferrals,
		DeliveryLimit:       cfg.DeliveryLimit,
		RetryDelays:         cfg.RetryDelays,
		DeferDelay:          cfg.DeferDelay,
	}, repo, queue.NewPublisher(mq, top), rdb, sealer, fakeStages(),
		// Placeholders until the fault injector (A12) and stages (Z1) replace them.
		limiter, pipelinetest.NoFaults{}, log)

	log.Info("worker started", "worker_id", workerID)
	err = rt.Run(ctx, mq)
	log.Info("worker stopped")
	return err
}

// fakeStages returns stand-in stages that each just sleep for a second, so the
// whole path (gateway, broker, worker, Postgres) can be exercised end to end
// before the real stages exist.
func fakeStages() map[pipeline.Status]pipeline.Stage {
	sleep := func(ctx context.Context) error {
		select {
		case <-time.After(time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m := map[pipeline.Status]pipeline.Stage{}
	for _, st := range []pipeline.Status{
		pipeline.StatusTranscribing, pipeline.StatusSummarizing,
		pipeline.StatusExtractingFrames, pipeline.StatusPublishing,
	} {
		m[st] = &pipelinetest.FakeStage{Status: st, Hook: sleep}
	}
	return m
}
