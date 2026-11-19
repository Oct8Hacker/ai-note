//go:build integration

package dlqwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"ainote/internal/jobs"
	"ainote/internal/pipeline"
	"ainote/internal/queue"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	pgAdminURL string
	amqpURL    string
	mgmtURL    string
)

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	ctx := context.Background()
	pg, err := tcpg.Run(ctx, "postgres:16-alpine",
		tcpg.WithDatabase("postgres"), tcpg.WithUsername("test"), tcpg.WithPassword("test"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		fmt.Fprintln(os.Stderr, "start postgres:", err)
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(pg) }()
	if pgAdminURL, err = pg.ConnectionString(ctx, "sslmode=disable"); err != nil {
		fmt.Fprintln(os.Stderr, "postgres url:", err)
		return 1
	}

	rmq, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "rabbitmq:3.13-management",
			ExposedPorts: []string{"5672/tcp", "15672/tcp"},
			WaitingFor:   wait.ForLog("Server startup complete").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start rabbitmq:", err)
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(rmq) }()
	host, _ := rmq.Host(ctx)
	ap, err := rmq.MappedPort(ctx, "5672/tcp")
	if err != nil {
		fmt.Fprintln(os.Stderr, "amqp port:", err)
		return 1
	}
	mp, err := rmq.MappedPort(ctx, "15672/tcp")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mgmt port:", err)
		return 1
	}
	amqpURL = fmt.Sprintf("amqp://guest:guest@%s:%s/", host, ap.Port())
	mgmtURL = fmt.Sprintf("http://%s:%s", host, mp.Port())
	return m.Run()
}

var top = queue.Topology{RetryDelays: []time.Duration{time.Second}, DeferDelay: time.Second}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newRepo(t *testing.T) (*jobs.Repo, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, pgAdminURL)
	require.NoError(t, err)
	name := fmt.Sprintf("d%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	admin.Close()
	i := strings.Index(pgAdminURL, "/postgres?")
	url := pgAdminURL[:i] + "/" + name + pgAdminURL[i+len("/postgres"):]
	require.NoError(t, jobs.Migrate(ctx, url))
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return jobs.NewRepo(pool), pool
}

func newConn(t *testing.T) *queue.Conn {
	t.Helper()
	c, err := queue.Dial(context.Background(), amqpURL, 10*time.Second, quietLog(), func(ch *amqp.Channel) error {
		return queue.Declare(ch, top)
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func purgeAll(t *testing.T, c *queue.Conn) {
	t.Helper()
	ch, err := c.Channel(context.Background())
	require.NoError(t, err)
	defer func() { _ = ch.Close() }()
	for _, q := range []string{queue.QueueWork, queue.QueueDefer, queue.QueueDLQ, queue.QueueDLQAudit, queue.RetryQueueName(time.Second)} {
		_, err := ch.QueuePurge(q, false)
		require.NoError(t, err)
	}
}

// waitLimitApplied blocks until the broker reports the delivery-limit policy
// on jobs.work, so the test never relies on a fixed sleep.
func waitLimitApplied(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		req, _ := http.NewRequest(http.MethodGet, mgmtURL+"/api/queues/%2F/"+queue.QueueWork, nil)
		req.SetBasicAuth("guest", "guest")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		var q struct {
			Eff map[string]any `json:"effective_policy_definition"`
		}
		if json.NewDecoder(resp.Body).Decode(&q) != nil {
			return false
		}
		_, ok := q.Eff["delivery-limit"]
		return ok
	}, 20*time.Second, 200*time.Millisecond, "delivery-limit policy not effective")
}

func TestWatcherMarksDeliveryLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo, pool := newRepo(t)
	c := newConn(t)
	purgeAll(t, c)

	// A SUMMARIZING job (as if a worker kept crashing on it).
	id := uuid.New()
	_, _, err := repo.CreateJob(ctx, jobs.NewJob{ID: id, VideoID: "dQw4w9WgXcQ", YouTubeURL: "https://youtu.be/dQw4w9WgXcQ"}, []byte("x"))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE jobs SET status='SUMMARIZING' WHERE id=$1`, id)
	require.NoError(t, err)

	require.NoError(t, queue.ApplyDeliveryLimitPolicy(ctx, mgmtURL, "guest", "guest", 2))
	defer func() { _ = queue.ApplyDeliveryLimitPolicy(context.Background(), mgmtURL, "guest", "guest", 20) }()
	waitLimitApplied(t)

	// Watcher first, so the audit copy is consumed whenever it arrives.
	wdone := make(chan error, 1)
	go func() { wdone <- Watch(ctx, c, repo, quietLog()) }()

	// A "crashing worker": requeue every delivery until the broker gives up.
	crasher := newConn(t)
	cdone := make(chan error, 1)
	cctx, ccancel := context.WithCancel(ctx)
	defer ccancel()
	go func() {
		cdone <- queue.Consume(cctx, crasher, queue.QueueWork, 1, func(_ context.Context, d queue.Delivery) { _ = d.NackRequeue() })
	}()
	require.NoError(t, queue.NewPublisher(c, top).PublishWork(ctx,
		queue.Message{V: 1, JobID: id, NotionTokenEnc: "x", EnqueuedAt: time.Now().UTC(), Reason: "new"}))

	require.Eventually(t, func() bool {
		v, err := repo.GetJobView(ctx, id)
		return err == nil && v.Status == pipeline.StatusDeadLettered
	}, 30*time.Second, 100*time.Millisecond)
	v, err := repo.GetJobView(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, v.Error)
	require.Equal(t, "DELIVERY_LIMIT_EXCEEDED", v.Error.Code)

	// The audit queue is drained; the inspection copy stays in jobs.dlq.
	ch, err := c.Channel(ctx)
	require.NoError(t, err)
	defer func() { _ = ch.Close() }()
	d, ok, err := ch.Get(queue.QueueDLQ, true)
	require.NoError(t, err)
	require.True(t, ok, "message must remain visible in jobs.dlq")
	var m queue.Message
	require.NoError(t, json.Unmarshal(d.Body, &m))
	require.Equal(t, id, m.JobID)
	require.NotNil(t, d.Headers["x-death"], "broker must record x-death on delivery-limit dead-lettering")
	require.Eventually(t, func() bool {
		qi, err := ch.QueueDeclarePassive(queue.QueueDLQAudit, true, false, false, false, nil)
		return err == nil && qi.Messages == 0
	}, 5*time.Second, 50*time.Millisecond)

	ccancel()
	require.NoError(t, <-cdone)
	cancel()
	require.NoError(t, <-wdone)
}

func TestWatcherLeavesTerminalJobsAlone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo, _ := newRepo(t)
	c := newConn(t)
	purgeAll(t, c)

	id := uuid.New()
	_, _, err := repo.CreateJob(ctx, jobs.NewJob{ID: id, VideoID: "dQw4w9WgXcQ", YouTubeURL: "https://youtu.be/dQw4w9WgXcQ"}, []byte("x"))
	require.NoError(t, err)
	require.NoError(t, repo.Fail(ctx, id, pipeline.StatusDeadLettered, "RETRIES_EXHAUSTED", "boom"))

	pub := queue.NewPublisher(c, top)
	unknown := uuid.New()
	for _, jid := range []uuid.UUID{id, unknown} {
		require.NoError(t, pub.PublishDeadLetter(ctx, queue.Message{V: 1, JobID: jid, EnqueuedAt: time.Now().UTC(), Reason: "new"}, "RETRIES_EXHAUSTED"))
	}
	// An undecodable body must not wedge the watcher either.
	ch, err := c.Channel(ctx)
	require.NoError(t, err)
	defer func() { _ = ch.Close() }()
	require.NoError(t, ch.PublishWithContext(ctx, queue.ExchangeDLX, "", false, false, amqp.Publishing{Body: []byte("not json")}))

	wdone := make(chan error, 1)
	go func() { wdone <- Watch(ctx, c, repo, quietLog()) }()

	require.Eventually(t, func() bool {
		qi, err := ch.QueueDeclarePassive(queue.QueueDLQAudit, true, false, false, false, nil)
		return err == nil && qi.Messages == 0 && qi.Consumers == 1
	}, 10*time.Second, 50*time.Millisecond)

	v, err := repo.GetJobView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "RETRIES_EXHAUSTED", v.Error.Code, "terminal job must be unchanged")
	cancel()
	require.NoError(t, <-wdone)
}
