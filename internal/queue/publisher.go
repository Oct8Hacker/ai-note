package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const confirmTimeout = 5 * time.Second

// ErrUnroutable is returned when the broker returns a mandatory publish
// because no queue matched.
var ErrUnroutable = errors.New("queue: message unroutable")

// Publisher publishes messages with broker confirms. A single confirm-mode
// channel is shared under a mutex (amqp channels are not goroutine-safe) and
// recreated after failures or connection drops.
type Publisher struct {
	c *Conn
	t Topology

	mu      sync.Mutex
	ch      *amqp.Channel
	returns chan amqp.Return
}

// NewPublisher returns a Publisher on c.
func NewPublisher(c *Conn, t Topology) *Publisher {
	return &Publisher{c: c, t: t}
}

// PublishWork publishes m to jobs.work.
func (p *Publisher) PublishWork(ctx context.Context, m Message) error {
	return p.publish(ctx, ExchangeJobs, RoutingWork, m, nil, "")
}

// PublishRetry publishes m to the retry queue whose delay is the smallest
// configured one >= delay (or the largest configured).
func (p *Publisher) PublishRetry(ctx context.Context, m Message, delay time.Duration) error {
	d := PickRetryDelay(p.t.RetryDelays, delay)
	if d == 0 {
		return errors.New("queue: no retry delays configured")
	}
	return p.publish(ctx, "", RetryQueueName(d), m, nil, "")
}

// PublishDefer publishes m to the short defer queue.
func (p *Publisher) PublishDefer(ctx context.Context, m Message) error {
	exp := strconv.FormatInt(p.t.DeferDelay.Milliseconds(), 10)
	return p.publish(ctx, "", QueueDefer, m, nil, exp)
}

// PublishDeadLetter publishes m to jobs.dlx with an x-reason header.
func (p *Publisher) PublishDeadLetter(ctx context.Context, m Message, reason string) error {
	return p.publish(ctx, ExchangeDLX, "", m, amqp.Table{"x-reason": reason}, "")
}

func (p *Publisher) publish(ctx context.Context, exchange, key string, m Message, headers amqp.Table, expiration string) error {
	body, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ch == nil || p.ch.IsClosed() {
		ch, err := p.c.Channel(ctx)
		if err != nil {
			return fmt.Errorf("publisher channel: %w", err)
		}
		if err := ch.Confirm(false); err != nil {
			_ = ch.Close()
			return fmt.Errorf("enable confirms: %w", err)
		}
		p.ch = ch
		p.returns = ch.NotifyReturn(make(chan amqp.Return, 16))
	}
	for drained := false; !drained; { // discard stale returns
		select {
		case <-p.returns:
		default:
			drained = true
		}
	}

	cctx, cancel := context.WithTimeout(ctx, confirmTimeout)
	defer cancel()
	dc, err := p.ch.PublishWithDeferredConfirmWithContext(cctx, exchange, key, true, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    m.JobID.String(),
		Expiration:   expiration,
		Headers:      headers,
		Timestamp:    time.Now(),
		Body:         body,
	})
	if err != nil {
		p.dropChannel()
		return fmt.Errorf("publish: %w", err)
	}
	acked, err := dc.WaitContext(cctx)
	if err != nil {
		p.dropChannel() // confirm state unknown: start over on a fresh channel
		return fmt.Errorf("wait for confirm: %w", err)
	}
	if !acked {
		return errors.New("publish nacked by broker")
	}
	// A Return is dispatched before the confirm for the same publish.
	select {
	case r := <-p.returns:
		return fmt.Errorf("%w: exchange=%q key=%q reply=%d %s", ErrUnroutable, exchange, key, r.ReplyCode, r.ReplyText)
	default:
	}
	return nil
}

// dropChannel discards the shared channel; p.mu must be held.
func (p *Publisher) dropChannel() {
	if p.ch != nil {
		_ = p.ch.Close()
		p.ch = nil
		p.returns = nil
	}
}
