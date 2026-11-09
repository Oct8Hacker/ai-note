package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const confirmTimeout = 5 * time.Second

// Publisher publishes messages with broker confirms. A single confirm-mode
// channel is shared under a mutex (amqp channels are not goroutine-safe) and
// recreated after failures or connection drops.
type Publisher struct {
	c *Conn
	t Topology

	mu sync.Mutex
	ch *amqp.Channel
}

// NewPublisher returns a Publisher on c.
func NewPublisher(c *Conn, t Topology) *Publisher {
	return &Publisher{c: c, t: t}
}

// PublishWork publishes m to jobs.work.
func (p *Publisher) PublishWork(ctx context.Context, m Message) error {
	return p.publish(ctx, ExchangeJobs, RoutingWork, m, nil)
}

// PublishRetry publishes m to the retry queue whose delay is the smallest
// configured one >= delay (or the largest configured).
func (p *Publisher) PublishRetry(ctx context.Context, m Message, delay time.Duration) error {
	d := PickRetryDelay(p.t.RetryDelays, delay)
	if d == 0 {
		return errors.New("queue: no retry delays configured")
	}
	return p.publish(ctx, "", RetryQueueName(d), m, nil)
}

// PublishDefer publishes m to the short defer queue.
func (p *Publisher) PublishDefer(ctx context.Context, m Message) error {
	return p.publish(ctx, "", QueueDefer, m, nil)
}

// PublishDeadLetter publishes m to jobs.dlx with an x-reason header.
func (p *Publisher) PublishDeadLetter(ctx context.Context, m Message, reason string) error {
	return p.publish(ctx, ExchangeDLX, "", m, amqp.Table{"x-reason": reason})
}

func (p *Publisher) publish(ctx context.Context, exchange, key string, m Message, headers amqp.Table) error {
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
	}

	cctx, cancel := context.WithTimeout(ctx, confirmTimeout)
	defer cancel()
	dc, err := p.ch.PublishWithDeferredConfirmWithContext(cctx, exchange, key, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    m.JobID.String(),
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
	return nil
}

// dropChannel discards the shared channel; p.mu must be held.
func (p *Publisher) dropChannel() {
	if p.ch != nil {
		_ = p.ch.Close()
		p.ch = nil
	}
}
