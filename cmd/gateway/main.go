// Command gateway is the HTTP entrypoint of the ainote system.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"ainote/internal/api"
	"ainote/internal/config"
	"ainote/internal/crypto"
	"ainote/internal/dlqwatch"
	"ainote/internal/doctor"
	"ainote/internal/faults"
	"ainote/internal/health"
	"ainote/internal/jobs"
	"ainote/internal/logging"
	"ainote/internal/notion"
	"ainote/internal/outbox"
	"ainote/internal/queue"
	"ainote/internal/ratelimit"
	"ainote/internal/worker"

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
	log := logging.New(cfg.LogLevel, "gateway")
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	if err := jobs.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	mq, err := setupBroker(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := mq.Close(); err != nil {
			log.Warn("amqp close", "err", err)
		}
	}()

	// pgxpool and redis connect lazily; amqp is dialed per probe below so
	// /healthz reports degraded (not a crash) when a dependency is down.
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

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

	checker := &health.Checker{
		Doctor: func(ctx context.Context) (map[string]any, error) {
			workers, err := doctor.ReadAll(ctx, rdb)
			if err != nil {
				return nil, err
			}
			report := map[string]any{"notion_oauth_configured": cfg.NotionClientID != "" && cfg.NotionClientSecret != "" && cfg.NotionRedirectURI != ""}
			for id, results := range workers {
				report[id] = results
			}
			return report, nil
		},
		Version:      version,
		WorkersAlive: func(ctx context.Context) (int, error) { return worker.CountAlive(ctx, rdb) },
		Provider429:  func(ctx context.Context) (int64, error) { return ratelimit.Get429(ctx, rdb) },
		Probes: map[string]health.Probe{
			"postgres": func(ctx context.Context) error {
				var one int
				return pool.QueryRow(ctx, "SELECT 1").Scan(&one)
			},
			"rabbitmq": func(_ context.Context) error { return probeRabbit(cfg.AMQPURL, cfg.AMQPHeartbeat) },
			"redis":    func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
		},
	}

	// Notion-only limiter for the gateway-side Notion calls of Task B10; the
	// other buckets are worker-only. Wait is bounded like the worker default.
	notionLimiter := ratelimit.New(rdb, nil, ratelimit.BucketSpec{Capacity: 3, RefillPerSec: cfg.NotionRPS},
		nil, 60*time.Second, time.Now)
	// The gateway reads fault overrides from Redis.
	faultControls := faults.New(rdb, config.FaultDefaults{}, rand.Float64)

	sealer, err := crypto.NewSealer(cfg.TokenEncKey)
	if err != nil {
		return fmt.Errorf("token sealer: %w", err)
	}
	repo := jobs.NewRepo(pool)
	relay := outbox.NewRelay(repo, queue.NewPublisher(mq, queue.Topology{RetryDelays: cfg.RetryDelays, DeferDelay: cfg.DeferDelay}),
		cfg.OutboxSweepInterval, log)
	relayCtx, stopRelay := context.WithCancel(ctx)
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		relay.Run(relayCtx)
	}()
	defer func() {
		stopRelay()
		<-relayDone
	}()

	bgCtx, stopBg := context.WithCancel(ctx)
	var bg sync.WaitGroup
	defer func() {
		stopBg()
		bg.Wait()
	}()
	bg.Add(2)
	go func() {
		defer bg.Done()
		if err := dlqwatch.Watch(bgCtx, mq, repo, log); err != nil {
			log.Error("dlq watcher stopped", "err", err)
		}
	}()
	go func() {
		defer bg.Done()
		purgeFrames(bgCtx, repo, log)
	}()

	apiSrv := api.NewServer(cfg, api.ServerDeps{
		Jobs:   repo,
		Sealer: sealer,
		Outbox: relay,
		Health: checker,
		Admin:  repo,
		Faults: faultControls,
		Notion: notion.New("", cfg.NotionVersion, notionLimiter, faultControls, nil),
	})
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           apiSrv.Handler(),
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

// frameRetention is how long frame JPEG bytes are kept after a job finishes (SRS §7.2).
const frameRetention = 24 * time.Hour

// purgeFrames clears expired frame images once at start and then hourly,
// until ctx is done.
func purgeFrames(ctx context.Context, repo *jobs.Repo, log *slog.Logger) {
	sweep := func() {
		n, err := repo.PurgeFrameImages(ctx, frameRetention)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("frame purge failed", "err", err)
			}
			return
		}
		if n > 0 {
			log.Info("purged frame images", "frames", n)
		}
	}
	sweep()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}

// probeRabbit opens a connection and channel and passive-declares jobs.work.
// NOT_FOUND (404) means topology is not declared yet, which is reported ok.
func probeRabbit(url string, heartbeat time.Duration) error {
	conn, err := amqp.DialConfig(url, amqp.Config{Heartbeat: heartbeat, Dial: amqp.DefaultDial(2 * time.Second)})
	if err != nil {
		return err
	}
	// Probe-only connection: a close error carries no actionable information.
	defer func() { _ = conn.Close() }()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer func() { _ = ch.Close() }()
	if _, err := ch.QueueDeclarePassive("jobs.work", true, false, false, false, nil); err != nil {
		var ae *amqp.Error
		if errors.As(err, &ae) && ae.Code == amqp.NotFound {
			return nil
		}
		return err
	}
	return nil
}

// setupBroker dials RabbitMQ, declares the topology and applies the
// delivery-limit policy, retrying the management API for up to ~20 s while it
// starts.
func setupBroker(ctx context.Context, cfg config.Gateway, log *slog.Logger) (*queue.Conn, error) {
	top := queue.Topology{RetryDelays: cfg.RetryDelays, DeferDelay: cfg.DeferDelay}
	// The hook declares topology on the first connection and on every reconnect.
	conn, err := queue.Dial(ctx, cfg.AMQPURL, cfg.AMQPHeartbeat, log, func(ch *amqp.Channel) error {
		return queue.Declare(ch, top)
	})
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: %w", err)
	}
	fail := func(err error) (*queue.Conn, error) {
		if cerr := conn.Close(); cerr != nil {
			log.Warn("amqp close", "err", cerr)
		}
		return nil, err
	}
	const attempts = 10
	for i := 1; ; i++ {
		err = queue.ApplyDeliveryLimitPolicy(ctx, cfg.RabbitMgmtURL, cfg.RabbitUser, cfg.RabbitPass, cfg.DeliveryLimit)
		if err == nil {
			return conn, nil
		}
		if i == attempts {
			return fail(fmt.Errorf("delivery-limit policy after %d attempts: %w", attempts, err))
		}
		log.Warn("delivery-limit policy not applied yet", "attempt", i, "err", err)
		select {
		case <-ctx.Done():
			return fail(ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}
