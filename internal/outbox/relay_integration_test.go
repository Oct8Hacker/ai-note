//go:build integration

package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"ainote/internal/jobs"
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
	adminURL string
	amqpURL  string
	rabbitID string
	dbSeq    int
	dbMu     sync.Mutex
)

var testTop = queue.Topology{RetryDelays: []time.Duration{time.Second}, DeferDelay: time.Second}

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
	adminURL, err = pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, "conn string:", err)
		return 1
	}
	rb, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "rabbitmq:3.13-management",
			ExposedPorts: []string{"5672/tcp"},
			WaitingFor:   wait.ForLog("Server startup complete").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "start rabbitmq:", err)
		return 1
	}
	defer func() { _ = testcontainers.TerminateContainer(rb) }()
	host, err := rb.Host(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "host:", err)
		return 1
	}
	port, err := rb.MappedPort(ctx, "5672/tcp")
	if err != nil {
		fmt.Fprintln(os.Stderr, "port:", err)
		return 1
	}
	amqpURL = fmt.Sprintf("amqp://guest:guest@%s:%s/", host, port.Port())
	rabbitID = rb.GetContainerID()
	return m.Run()
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type env struct {
	repo *jobs.Repo
	pool *pgxpool.Pool
	conn *queue.Conn
	pub  *queue.Publisher
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminURL)
	require.NoError(t, err)
	dbMu.Lock()
	dbSeq++
	name := fmt.Sprintf("o%d_%d", time.Now().UnixNano(), dbSeq)
	dbMu.Unlock()
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	admin.Close()
	require.NoError(t, err)
	i := strings.Index(adminURL, "/postgres?")
	url := adminURL[:i] + "/" + name + adminURL[i+len("/postgres"):]
	require.NoError(t, jobs.Migrate(ctx, url))
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	conn, err := queue.Dial(ctx, amqpURL, 5*time.Second, quietLog(), func(ch *amqp.Channel) error {
		return queue.Declare(ch, testTop)
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	purge(t, conn)
	return &env{repo: jobs.NewRepo(pool), pool: pool, conn: conn, pub: queue.NewPublisher(conn, testTop)}
}

func purge(t *testing.T, c *queue.Conn) {
	t.Helper()
	ch, err := c.Channel(context.Background())
	require.NoError(t, err)
	defer func() { _ = ch.Close() }()
	_, err = ch.QueuePurge(queue.QueueWork, false)
	require.NoError(t, err)
}

func (e *env) createJob(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	payload, err := json.Marshal(queue.Message{V: 1, JobID: id, NotionTokenEnc: "sealed", EnqueuedAt: time.Now().UTC(), Reason: "new"})
	require.NoError(t, err)
	_, created, err := e.repo.CreateJob(context.Background(), jobs.NewJob{ID: id, VideoID: "vid", YouTubeURL: "https://youtu.be/x"}, payload)
	require.NoError(t, err)
	require.True(t, created)
	return id
}

// collector consumes jobs.work and counts deliveries per job id.
type collector struct {
	mu   sync.Mutex
	seen map[uuid.UUID]int
}

func (c *collector) count(id uuid.UUID) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen[id]
}

func startCollector(t *testing.T, conn *queue.Conn) *collector {
	t.Helper()
	c := &collector{seen: map[uuid.UUID]int{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = queue.Consume(ctx, conn, queue.QueueWork, 10, func(_ context.Context, d queue.Delivery) {
			var m queue.Message
			if err := json.Unmarshal(d.Raw.Body, &m); err == nil {
				c.mu.Lock()
				c.seen[m.JobID]++
				c.mu.Unlock()
			}
			_ = d.Ack()
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return c
}

func runRelay(t *testing.T, e *env, interval time.Duration) *Relay {
	t.Helper()
	r := NewRelay(e.repo, e.pub, interval, quietLog())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return r
}

func (e *env) outboxState(t *testing.T, id uuid.UUID) (payloadNull, published bool) {
	t.Helper()
	require.NoError(t, e.pool.QueryRow(context.Background(),
		`SELECT payload IS NULL, published_at IS NOT NULL FROM outbox WHERE job_id=$1`, id).Scan(&payloadNull, &published))
	return
}

func TestRelayPublishesAndClears(t *testing.T) {
	e := newEnv(t)
	col := startCollector(t, e.conn)
	r := runRelay(t, e, time.Hour) // only Kick can trigger a later batch
	id := e.createJob(t)
	r.Kick()
	require.Eventually(t, func() bool { return col.count(id) == 1 }, 10*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool { n, p := e.outboxState(t, id); return n && p }, 5*time.Second, 50*time.Millisecond)
}

func TestRelaySurvivesBrokerOutage(t *testing.T) {
	e := newEnv(t)
	col := startCollector(t, e.conn)
	r := runRelay(t, e, 500*time.Millisecond)

	require.NoError(t, exec.Command("docker", "pause", rabbitID).Run()) //nolint:gosec // test-only: container id from testcontainers
	unpaused := false
	unpause := func() {
		if !unpaused {
			unpaused = true
			require.NoError(t, exec.Command("docker", "unpause", rabbitID).Run()) //nolint:gosec // test-only: container id from testcontainers
		}
	}
	defer unpause()

	id := e.createJob(t)
	r.Kick()
	time.Sleep(2 * time.Second)
	require.Zero(t, col.count(id))
	_, published := e.outboxState(t, id)
	require.False(t, published, "row must stay unpublished while the broker is down")

	unpause()
	// An in-flight publish may wait out the 5s confirm timeout; the next sweep
	// then republishes on a fresh channel.
	require.Eventually(t, func() bool { return col.count(id) >= 1 }, 30*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool { n, p := e.outboxState(t, id); return n && p }, 5*time.Second, 50*time.Millisecond)
}

func TestRelayNoDoublePublishConcurrent(t *testing.T) {
	e := newEnv(t)
	col := startCollector(t, e.conn)
	ids := make([]uuid.UUID, 0, 150)
	for i := 0; i < 150; i++ {
		ids = append(ids, e.createJob(t))
	}
	r1 := runRelay(t, e, 100*time.Millisecond)
	r2 := runRelay(t, e, 100*time.Millisecond)
	r1.Kick()
	r2.Kick()
	require.Eventually(t, func() bool {
		for _, id := range ids {
			if col.count(id) < 1 {
				return false
			}
		}
		return true
	}, 30*time.Second, 100*time.Millisecond)
	time.Sleep(time.Second) // let any duplicate arrive
	for _, id := range ids {
		require.Equal(t, 1, col.count(id), "job %s", id)
	}
}

func TestRelayBadPayloadDoesNotBlock(t *testing.T) {
	e := newEnv(t)
	col := startCollector(t, e.conn)
	r := runRelay(t, e, time.Hour)
	bad := uuid.New()
	_, created, err := e.repo.CreateJob(context.Background(), jobs.NewJob{ID: bad, VideoID: "v", YouTubeURL: "u"}, []byte("not json"))
	require.NoError(t, err)
	require.True(t, created)
	good := e.createJob(t)
	r.Kick()
	require.Eventually(t, func() bool { return col.count(good) == 1 }, 10*time.Second, 50*time.Millisecond)
	n, p := e.outboxState(t, bad)
	require.True(t, n && p)
	var events int
	require.NoError(t, e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM job_events WHERE job_id=$1 AND type='outbox_bad_payload'`, bad).Scan(&events))
	require.Equal(t, 1, events)
}
