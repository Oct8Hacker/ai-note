//go:build integration

package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"ainote/internal/crypto"
	"ainote/internal/jobs"
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/pipelinetest"
	"ainote/internal/queue"
)

// Retry queues are named in whole seconds (queue.RetryQueueName), so
// sub-second delays would collapse into one queue.
var testRetryDelays = []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second}

const (
	testDeferDelay = time.Second
	testToken      = "secret-notion-token"
)

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testTopology() queue.Topology {
	return queue.Topology{RetryDelays: testRetryDelays, DeferDelay: testDeferDelay}
}

// env is one test's isolated Postgres database plus a clean broker and Redis.
type env struct {
	t      *testing.T
	pool   *pgxpool.Pool
	repo   *jobs.Repo
	rdb    *redis.Client
	sealer *crypto.Sealer
	pub    *queue.Publisher
	conn   *queue.Conn // the test's own connection (publishing, inspection)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, pgAdminURL)
	require.NoError(t, err)
	name := fmt.Sprintf("w%d", time.Now().UnixNano())
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	admin.Close()
	i := strings.Index(pgAdminURL, "/postgres?")
	url := pgAdminURL[:i] + "/" + name + pgAdminURL[i+len("/postgres"):]
	require.NoError(t, jobs.Migrate(ctx, url))
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	sealer, err := crypto.NewSealer(key)
	require.NoError(t, err)

	e := &env{t: t, pool: pool, repo: jobs.NewRepo(pool), rdb: newRedis(t), sealer: sealer}
	e.conn = e.dial()
	e.pub = queue.NewPublisher(e.conn, testTopology())
	e.purge()
	return e
}

func (e *env) dial() *queue.Conn {
	e.t.Helper()
	c, err := queue.Dial(context.Background(), amqpURL, 10*time.Second, testLog(), func(ch *amqp.Channel) error {
		return queue.Declare(ch, testTopology())
	})
	require.NoError(e.t, err)
	e.t.Cleanup(func() { _ = c.Close() })
	return c
}

// purge empties every queue left over from earlier tests.
func (e *env) purge() {
	e.t.Helper()
	ch, err := e.conn.Channel(context.Background())
	require.NoError(e.t, err)
	defer func() { _ = ch.Close() }()
	names := []string{queue.QueueDefer, queue.QueueWork, queue.QueueDLQ, queue.QueueDLQAudit}
	for _, d := range testRetryDelays {
		names = append(names, queue.RetryQueueName(d))
	}
	for _, n := range names {
		_, err := ch.QueuePurge(n, false)
		require.NoError(e.t, err)
	}
}

// queueDepth is the total message count over the work, defer and retry queues.
func (e *env) queueDepth() int {
	e.t.Helper()
	ch, err := e.conn.Channel(context.Background())
	require.NoError(e.t, err)
	defer func() { _ = ch.Close() }()
	names := []string{queue.QueueWork, queue.QueueDefer}
	for _, d := range testRetryDelays {
		names = append(names, queue.RetryQueueName(d))
	}
	total := 0
	for _, n := range names {
		q, err := ch.QueueDeclarePassive(n, true, false, false, false, nil)
		require.NoError(e.t, err)
		total += q.Messages
	}
	return total
}

// newJob creates a QUEUED job and returns the message the relay would publish.
func (e *env) newJob(videoID string) queue.Message {
	e.t.Helper()
	id := uuid.New()
	_, _, err := e.repo.CreateJob(context.Background(), jobs.NewJob{
		ID: id, VideoID: videoID, YouTubeURL: "https://youtu.be/" + videoID,
	}, []byte(`{}`))
	require.NoError(e.t, err)
	enc, err := e.sealer.Seal(testToken)
	require.NoError(e.t, err)
	return queue.Message{V: 1, JobID: id, NotionTokenEnc: enc, EnqueuedAt: time.Now(), Reason: "new"}
}

func (e *env) publishNew(videoID string) queue.Message {
	e.t.Helper()
	m := e.newJob(videoID)
	require.NoError(e.t, e.pub.PublishWork(context.Background(), m))
	return m
}

func (e *env) view(id uuid.UUID) jobs.JobView {
	e.t.Helper()
	v, err := e.repo.GetJobView(context.Background(), id)
	require.NoError(e.t, err)
	return v
}

func (e *env) waitStatus(id uuid.UUID, want pipeline.Status, within time.Duration) jobs.JobView {
	e.t.Helper()
	require.Eventually(e.t, func() bool { return e.view(id).Status == want }, within, 50*time.Millisecond,
		"job %s never reached %s (now %s)", id, want, e.view(id).Status)
	return e.view(id)
}

func (e *env) intCol(id uuid.UUID, col string) int {
	e.t.Helper()
	var n int
	require.NoError(e.t, e.pool.QueryRow(context.Background(),
		"SELECT "+col+" FROM jobs WHERE id=$1", id).Scan(&n))
	return n
}

func (e *env) leaseOwner(id uuid.UUID) *string {
	e.t.Helper()
	var o *string
	require.NoError(e.t, e.pool.QueryRow(context.Background(),
		"SELECT lease_owner FROM jobs WHERE id=$1", id).Scan(&o))
	return o
}

// stageSet is the four fake stages of one test.
type stageSet struct{ transcribe, summarize, frames, publish *pipelinetest.FakeStage }

func newStages() *stageSet {
	return &stageSet{
		transcribe: &pipelinetest.FakeStage{Status: pipeline.StatusTranscribing},
		summarize:  &pipelinetest.FakeStage{Status: pipeline.StatusSummarizing},
		frames:     &pipelinetest.FakeStage{Status: pipeline.StatusExtractingFrames},
		publish:    &pipelinetest.FakeStage{Status: pipeline.StatusPublishing},
	}
}

func (s *stageSet) all() []*pipelinetest.FakeStage {
	return []*pipelinetest.FakeStage{s.transcribe, s.summarize, s.frames, s.publish}
}

func (s *stageSet) byStatus() map[pipeline.Status]pipeline.Stage {
	m := map[pipeline.Status]pipeline.Stage{}
	for _, f := range s.all() {
		m[f.Status] = f
	}
	return m
}

func testConfig(workerID string) Config {
	return Config{
		WorkerID: workerID, Prefetch: 1,
		LeaseTTL: 3 * time.Second, ShutdownGrace: time.Second,
		MaxAttempts: 5, MaxRateLimitRetries: 8, MaxDeferrals: 30, DeliveryLimit: 10,
		RetryDelays: testRetryDelays, DeferDelay: testDeferDelay,
	}
}

// running is a started Runtime with its own AMQP connection and heartbeat.
type running struct {
	conn     *queue.Conn
	cancel   context.CancelFunc // the shutdown signal passed to Run
	hbCancel context.CancelFunc
	done     chan struct{}
}

// stop sends the shutdown signal and waits for Run to return.
func (r *running) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(20 * time.Second):
		t.Fatal("runtime did not stop")
	}
}

func (e *env) start(cfg Config, stages map[pipeline.Status]pipeline.Stage) *running {
	e.t.Helper()
	conn := e.dial()
	rt := New(cfg, e.repo, queue.NewPublisher(conn, testTopology()), e.rdb, e.sealer, stages,
		pipelinetest.NoopLimiter{}, pipelinetest.NoFaults{}, testLog())

	hbCtx, hbCancel := context.WithCancel(context.Background())
	StartHeartbeat(hbCtx, e.rdb, cfg.WorkerID, 200*time.Millisecond, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{conn: conn, cancel: cancel, hbCancel: hbCancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		_ = rt.Run(ctx, conn)
	}()
	e.t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(20 * time.Second):
		}
		hbCancel()
	})
	return r
}

func TestHappyPathFakeStages(t *testing.T) {
	e := newEnv(t)
	s := newStages()
	e.start(testConfig("w1"), s.byStatus())

	m := e.publishNew("dQw4w9WgXcQ")
	v := e.waitStatus(m.JobID, pipeline.StatusCompleted, 15*time.Second)

	require.Equal(t, 100, v.Progress)
	for _, f := range s.all() {
		require.Equal(t, 1, f.CallCount(), "stage %s", f.Status)
	}
	require.Nil(t, e.leaseOwner(m.JobID), "lease must be released")
	require.Eventually(t, func() bool { return e.queueDepth() == 0 }, 5*time.Second, 100*time.Millisecond)
}

func TestNotionTokenOnlyForPublishing(t *testing.T) {
	e := newEnv(t)
	var mu sync.Mutex
	tokens := map[pipeline.Status]string{}
	stages := map[pipeline.Status]pipeline.Stage{}
	for _, st := range []pipeline.Status{pipeline.StatusTranscribing, pipeline.StatusSummarizing,
		pipeline.StatusExtractingFrames, pipeline.StatusPublishing} {
		stages[st] = tokenSpy{status: st, mu: &mu, tokens: tokens}
	}
	e.start(testConfig("w1"), stages)

	m := e.publishNew("dQw4w9WgXcQ")
	e.waitStatus(m.JobID, pipeline.StatusCompleted, 15*time.Second)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "", tokens[pipeline.StatusTranscribing])
	require.Equal(t, "", tokens[pipeline.StatusSummarizing])
	require.Equal(t, "", tokens[pipeline.StatusExtractingFrames])
	require.Equal(t, testToken, tokens[pipeline.StatusPublishing])
}

type tokenSpy struct {
	status pipeline.Status
	mu     *sync.Mutex
	tokens map[pipeline.Status]string
}

func (s tokenSpy) Name() pipeline.Status { return s.status }

func (s tokenSpy) Run(_ context.Context, _ *pipeline.Job, deps pipeline.Deps) (pipeline.StageResult, error) {
	s.mu.Lock()
	s.tokens[s.status] = deps.NotionToken
	s.mu.Unlock()
	return pipeline.StageResult{}, nil
}

func TestRetryableThenSuccess(t *testing.T) {
	e := newEnv(t)
	s := newStages()
	s.summarize.Results = []error{
		&pipeline.RetryableError{Code: "LLM_UNAVAILABLE", Err: errors.New("boom")},
		&pipeline.RetryableError{Code: "LLM_UNAVAILABLE", Err: errors.New("boom")},
	}
	e.start(testConfig("w1"), s.byStatus())

	m := e.publishNew("dQw4w9WgXcQ")
	e.waitStatus(m.JobID, pipeline.StatusCompleted, 30*time.Second)

	require.Equal(t, 2, e.intCol(m.JobID, "attempts"))
	require.Equal(t, 1, s.transcribe.CallCount(), "finished stage must not re-run")
	require.Equal(t, 3, s.summarize.CallCount())
	require.Equal(t, 1, s.frames.CallCount())
	require.Equal(t, 1, s.publish.CallCount())
}

func TestRateLimitedDoesNotSpendFailureBudget(t *testing.T) {
	e := newEnv(t)
	s := newStages()
	rl := &pipeline.RateLimitedError{Provider: "llm"}
	s.summarize.Results = []error{rl, rl, rl}
	e.start(testConfig("w1"), s.byStatus())

	m := e.publishNew("dQw4w9WgXcQ")

	// While the first retry is pending the job is visibly waiting, in its stage.
	require.Eventually(t, func() bool {
		v := e.view(m.JobID)
		return v.Waiting != nil && v.Waiting.Reason == "RATE_LIMITED"
	}, 10*time.Second, 25*time.Millisecond)
	v := e.view(m.JobID)
	require.Equal(t, pipeline.StatusSummarizing, v.Status)
	require.Equal(t, "llm", v.Waiting.Provider)

	e.waitStatus(m.JobID, pipeline.StatusCompleted, 30*time.Second)
	require.Equal(t, 0, e.intCol(m.JobID, "attempts"))
	require.Equal(t, 3, e.intCol(m.JobID, "rate_limit_retries"))
	require.Equal(t, 4, s.summarize.CallCount())
}

func TestDeadLetterAfterBudget(t *testing.T) {
	e := newEnv(t)
	s := newStages()
	s.summarize.Hook = func(context.Context) error {
		return &pipeline.RetryableError{Code: "LLM_UNAVAILABLE", Err: errors.New("always")}
	}
	cfg := testConfig("w1")
	cfg.MaxAttempts = 5
	e.start(cfg, s.byStatus())

	m := e.publishNew("dQw4w9WgXcQ")
	v := e.waitStatus(m.JobID, pipeline.StatusDeadLettered, 40*time.Second)

	require.NotNil(t, v.Error)
	require.Equal(t, "RETRIES_EXHAUSTED", v.Error.Code)
	require.Equal(t, 5, s.summarize.CallCount())

	ch, err := e.conn.Channel(context.Background())
	require.NoError(t, err)
	defer func() { _ = ch.Close() }()
	var got amqp.Delivery
	require.Eventually(t, func() bool {
		d, ok, err := ch.Get(queue.QueueDLQ, true)
		got = d
		return err == nil && ok
	}, 5*time.Second, 100*time.Millisecond, "dead-lettered message must be in jobs.dlq")
	var dm queue.Message
	require.NoError(t, json.Unmarshal(got.Body, &dm))
	require.Equal(t, m.JobID, dm.JobID)
	require.Equal(t, "RETRIES_EXHAUSTED", got.Headers["x-reason"])
}

func TestFatalFailsImmediately(t *testing.T) {
	e := newEnv(t)
	s := newStages()
	s.summarize.Results = []error{&pipeline.FatalError{Code: "VIDEO_TOO_LONG", Msg: "too long"}}
	e.start(testConfig("w1"), s.byStatus())

	m := e.publishNew("dQw4w9WgXcQ")
	v := e.waitStatus(m.JobID, pipeline.StatusFailed, 15*time.Second)

	require.NotNil(t, v.Error)
	require.Equal(t, "VIDEO_TOO_LONG", v.Error.Code)
	require.Equal(t, 1, s.summarize.CallCount())
	require.Equal(t, 0, s.frames.CallCount())
	require.Eventually(t, func() bool { return e.queueDepth() == 0 }, 5*time.Second, 100*time.Millisecond)
}

func TestCrashRecovery(t *testing.T) {
	e := newEnv(t)
	s := newStages()
	started := make(chan struct{})
	var calls atomic.Int32
	s.summarize.Hook = func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done() // the "crashed" worker's stage never finishes on its own
			return ctx.Err()
		}
		return nil
	}
	r1 := e.start(testConfig("w1"), s.byStatus())

	m := e.publishNew("dQw4w9WgXcQ")
	<-started

	// Hard kill: heartbeat gone and AMQP connection dropped, no lease release.
	r1.hbCancel()
	require.NoError(t, r1.conn.Close())
	require.Eventually(t, func() bool {
		ok, err := IsAlive(context.Background(), e.rdb, "w1")
		return err == nil && !ok
	}, 5*time.Second, 25*time.Millisecond)

	e.start(testConfig("w2"), s.byStatus())
	e.waitStatus(m.JobID, pipeline.StatusCompleted, 15*time.Second)

	require.Equal(t, 1, s.transcribe.CallCount(), "earlier stages must not re-run")
	require.Equal(t, 2, s.summarize.CallCount())
	require.Equal(t, 1, s.frames.CallCount())
	require.Equal(t, 1, s.publish.CallCount())
}

func TestDuplicateDeliveryDefers(t *testing.T) {
	e := newEnv(t)
	s := newStages()
	started := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	s.summarize.Hook = func(ctx context.Context) error {
		once.Do(func() { close(started) })
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	e.start(testConfig("w1"), s.byStatus())
	m := e.publishNew("dQw4w9WgXcQ")
	<-started

	e.start(testConfig("w2"), s.byStatus())
	require.NoError(t, e.pub.PublishWork(context.Background(), m)) // duplicate while w1 holds the lease

	require.Eventually(t, func() bool { return e.intCol(m.JobID, "deferrals") >= 1 },
		10*time.Second, 50*time.Millisecond, "second worker must defer the duplicate")
	v := e.view(m.JobID)
	require.Equal(t, pipeline.StatusSummarizing, v.Status)

	close(gate)
	e.waitStatus(m.JobID, pipeline.StatusCompleted, 15*time.Second)

	// The deferred duplicate comes back, finds the job terminal and is dropped.
	require.Eventually(t, func() bool { return e.queueDepth() == 0 }, 10*time.Second, 100*time.Millisecond)
	require.Equal(t, 1, s.publish.CallCount(), "job must complete exactly once")
	require.Equal(t, 1, s.summarize.CallCount())
	require.Equal(t, 1, s.transcribe.CallCount())
}

func TestTwoJobsSameVideoIndependent(t *testing.T) {
	e := newEnv(t)
	s := newStages()
	var inStage atomic.Int32
	both := make(chan struct{})
	proceed := make(chan struct{})
	s.summarize.Hook = func(ctx context.Context) error {
		if inStage.Add(1) == 2 {
			close(both)
		}
		select {
		case <-proceed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	e.start(testConfig("w1"), s.byStatus())
	e.start(testConfig("w2"), s.byStatus())

	m1 := e.publishNew("dQw4w9WgXcQ")
	m2 := e.publishNew("dQw4w9WgXcQ")
	require.NotEqual(t, m1.JobID, m2.JobID)

	select {
	case <-both:
	case <-time.After(15 * time.Second):
		t.Fatal("both jobs must run concurrently")
	}
	o1, o2 := e.leaseOwner(m1.JobID), e.leaseOwner(m2.JobID)
	require.NotNil(t, o1)
	require.NotNil(t, o2)
	require.NotEqual(t, *o1, *o2, "each job holds its own lease")
	close(proceed)

	e.waitStatus(m1.JobID, pipeline.StatusCompleted, 15*time.Second)
	e.waitStatus(m2.JobID, pipeline.StatusCompleted, 15*time.Second)
	require.Equal(t, 2, s.publish.CallCount())
}

func TestGracefulShutdown(t *testing.T) {
	e := newEnv(t)
	s := newStages()
	started := make(chan struct{})
	var calls atomic.Int32
	s.summarize.Hook = func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-time.After(5 * time.Second):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	cfg := testConfig("w1")
	cfg.ShutdownGrace = time.Second
	r1 := e.start(cfg, s.byStatus())

	m := e.publishNew("dQw4w9WgXcQ")
	<-started

	t0 := time.Now()
	r1.stop(t)
	took := time.Since(t0)
	require.GreaterOrEqual(t, took, 900*time.Millisecond, "stage gets the grace period")
	require.Less(t, took, 4*time.Second, "stage is cancelled once the grace period ends")

	require.Nil(t, e.leaseOwner(m.JobID), "lease released on shutdown")
	require.Equal(t, pipeline.StatusSummarizing, e.view(m.JobID).Status)
	require.Equal(t, 1, e.queueDepth(), "message requeued")

	e.start(testConfig("w2"), s.byStatus())
	e.waitStatus(m.JobID, pipeline.StatusCompleted, 15*time.Second)
	require.Equal(t, 1, s.transcribe.CallCount())
}
