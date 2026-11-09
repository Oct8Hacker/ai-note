//go:build integration

package queue

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	amqpURL string
	mgmtURL string
)

func TestMain(m *testing.M) { os.Exit(runMain(m)) }

func runMain(m *testing.M) int {
	ctx := context.Background()
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
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
	defer func() {
		if err := ctr.Terminate(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "terminate rabbitmq:", err)
		}
	}()
	host, err := ctr.Host(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "host:", err)
		return 1
	}
	ap, err := ctr.MappedPort(ctx, "5672/tcp")
	if err != nil {
		fmt.Fprintln(os.Stderr, "port:", err)
		return 1
	}
	mp, err := ctr.MappedPort(ctx, "15672/tcp")
	if err != nil {
		fmt.Fprintln(os.Stderr, "port:", err)
		return 1
	}
	amqpURL = fmt.Sprintf("amqp://guest:guest@%s:%s/", host, ap.Port())
	mgmtURL = fmt.Sprintf("http://%s:%s", host, mp.Port())
	return m.Run()
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newConn(t *testing.T) *Conn {
	t.Helper()
	c, err := Dial(context.Background(), amqpURL, 10*time.Second, testLog())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func declare(t *testing.T, c *Conn, top Topology) {
	t.Helper()
	ch, err := c.Channel(context.Background())
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	defer func() { _ = ch.Close() }()
	if err := Declare(ch, top); err != nil {
		t.Fatalf("declare: %v", err)
	}
}

var testTop = Topology{RetryDelays: []time.Duration{time.Second, 2 * time.Second}, DeferDelay: time.Second}

func purge(t *testing.T, c *Conn) {
	t.Helper()
	ch, err := c.Channel(context.Background())
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	defer func() { _ = ch.Close() }()
	for _, q := range []string{QueueWork, QueueDefer, QueueDLQ, QueueDLQAudit, RetryQueueName(time.Second), RetryQueueName(2 * time.Second)} {
		if _, err := ch.QueuePurge(q, false); err != nil {
			t.Fatalf("purge %s: %v", q, err)
		}
	}
}

func setup(t *testing.T) (*Conn, *Publisher) {
	t.Helper()
	c := newConn(t)
	declare(t, c, testTop)
	purge(t, c)
	return c, NewPublisher(c, testTop)
}

func newMsg() Message {
	return Message{V: 1, JobID: uuid.New(), NotionTokenEnc: "x", EnqueuedAt: time.Now().UTC(), Reason: "new"}
}

// getOne polls queue until a message is available or timeout elapses.
func getOne(t *testing.T, c *Conn, queue string, timeout time.Duration) (amqp.Delivery, bool) {
	t.Helper()
	ch, err := c.Channel(context.Background())
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	defer func() { _ = ch.Close() }()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		d, ok, err := ch.Get(queue, true)
		if err != nil {
			t.Fatalf("get %s: %v", queue, err)
		}
		if ok {
			return d, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return amqp.Delivery{}, false
}

func TestDeclareTwice(t *testing.T) {
	c := newConn(t)
	declare(t, c, testTop)
	declare(t, c, testTop)
	declare(t, c, Topology{RetryDelays: []time.Duration{time.Second, 2 * time.Second, 7 * time.Second}, DeferDelay: time.Second})
	ch, err := c.Channel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ch.Close() }()
	if _, err := ch.QueueDeclarePassive(RetryQueueName(7*time.Second), true, false, false, false, nil); err != nil {
		t.Fatalf("new retry queue missing: %v", err)
	}
}

func TestPolicyChangeDoesNotBreakRedeclare(t *testing.T) {
	c := newConn(t)
	declare(t, c, testTop)
	ctx := context.Background()
	for _, limit := range []int{10, 3} {
		if err := ApplyDeliveryLimitPolicy(ctx, mgmtURL, "guest", "guest", limit); err != nil {
			t.Fatalf("policy %d: %v", limit, err)
		}
		declare(t, c, testTop)
	}
}

func TestPublishConfirmed(t *testing.T) {
	c, p := setup(t)
	m := newMsg()
	if err := p.PublishWork(context.Background(), m); err != nil {
		t.Fatalf("publish: %v", err)
	}
	d, ok := getOne(t, c, QueueWork, 3*time.Second)
	if !ok {
		t.Fatal("message not on jobs.work")
	}
	if d.MessageId != m.JobID.String() || d.ContentType != "application/json" || d.DeliveryMode != amqp.Persistent {
		t.Fatalf("bad properties: %+v", d)
	}
}

func TestRetryReturnsToWork(t *testing.T) {
	c, p := setup(t)
	start := time.Now()
	if err := p.PublishRetry(context.Background(), newMsg(), time.Second); err != nil {
		t.Fatal(err)
	}
	if _, ok := getOne(t, c, QueueWork, 5*time.Second); !ok {
		t.Fatal("retry message never returned to jobs.work")
	}
	if el := time.Since(start); el < 900*time.Millisecond {
		t.Fatalf("returned too early: %v", el)
	}
}

func TestDeferReturnsToWork(t *testing.T) {
	c, p := setup(t)
	if err := p.PublishDefer(context.Background(), newMsg()); err != nil {
		t.Fatal(err)
	}
	if _, ok := getOne(t, c, QueueWork, 5*time.Second); !ok {
		t.Fatal("deferred message lost")
	}
}

func TestDeadLetterFansOut(t *testing.T) {
	c, p := setup(t)
	if err := p.PublishDeadLetter(context.Background(), newMsg(), "RETRIES_EXHAUSTED"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{QueueDLQ, QueueDLQAudit} {
		d, ok := getOne(t, c, q, 3*time.Second)
		if !ok {
			t.Fatalf("no copy in %s", q)
		}
		if d.Headers["x-reason"] != "RETRIES_EXHAUSTED" {
			t.Fatalf("%s: x-reason = %v", q, d.Headers["x-reason"])
		}
	}
}

func TestDeliveryLimitDeadLetters(t *testing.T) {
	c, p := setup(t)
	ctx := context.Background()
	if err := ApplyDeliveryLimitPolicy(ctx, mgmtURL, "guest", "guest", 2); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ApplyDeliveryLimitPolicy(ctx, mgmtURL, "guest", "guest", 20) }()
	time.Sleep(time.Second) // let the policy propagate to the queue

	if err := p.PublishWork(ctx, newMsg()); err != nil {
		t.Fatal(err)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var mu sync.Mutex
	deliveries := 0
	done := make(chan error, 1)
	go func() {
		done <- Consume(cctx, c, QueueWork, 1, func(_ context.Context, d Delivery) {
			mu.Lock()
			deliveries++
			mu.Unlock()
			_ = d.NackRequeue()
		})
	}()
	if _, ok := getOne(t, c, QueueDLQ, 10*time.Second); !ok {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("message not dead-lettered (deliveries=%d)", deliveries)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("consume: %v", err)
	}
}

func TestUnackedRedeliveredOnConnClose(t *testing.T) {
	c1, p := setup(t)
	ctx := context.Background()
	if err := p.PublishWork(ctx, newMsg()); err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{}, 1)
	c1ctx, cancel1 := context.WithCancel(ctx)
	defer cancel1()
	go func() {
		_ = Consume(c1ctx, c1, QueueWork, 1, func(_ context.Context, _ Delivery) {
			got <- struct{}{}
			<-c1ctx.Done() // never ack
		})
	}()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("first consumer got nothing")
	}
	if err := c1.Close(); err != nil {
		t.Logf("close: %v", err)
	}
	cancel1()

	c2 := newConn(t)
	redelivered := make(chan bool, 1)
	c2ctx, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	go func() {
		_ = Consume(c2ctx, c2, QueueWork, 1, func(_ context.Context, d Delivery) {
			redelivered <- d.Raw.Redelivered
			_ = d.Ack()
		})
	}()
	select {
	case r := <-redelivered:
		if !r {
			t.Fatal("expected Redelivered=true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second consumer got nothing")
	}
}
