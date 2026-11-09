package queue

import (
	"fmt"
	"sort"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Names of the RabbitMQ entities (SRS §8.1).
const (
	ExchangeJobs  = "jobs"
	ExchangeDLX   = "jobs.dlx"
	QueueWork     = "jobs.work"
	QueueDefer    = "jobs.retry.defer"
	QueueDLQ      = "jobs.dlq"
	QueueDLQAudit = "jobs.dlq.audit"
	RoutingWork   = "work"
)

// Topology carries the configurable parts of the broker layout.
type Topology struct {
	RetryDelays []time.Duration
	DeferDelay  time.Duration
}

// RetryQueueName returns the retry queue for delay d, encoding it in whole
// seconds (10s -> "jobs.retry.10s", 1m30s -> "jobs.retry.90s").
func RetryQueueName(d time.Duration) string {
	return fmt.Sprintf("jobs.retry.%ds", int64(d/time.Second))
}

// PickRetryDelay returns the smallest configured delay >= want, or the
// largest configured delay when none is long enough. It returns 0 when
// nothing is configured.
func PickRetryDelay(configured []time.Duration, want time.Duration) time.Duration {
	if len(configured) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), configured...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, d := range sorted {
		if d >= want {
			return d
		}
	}
	return sorted[len(sorted)-1]
}

// Declare idempotently declares the SRS §8.1 topology. The delivery limit is
// deliberately not a queue argument; see ApplyDeliveryLimitPolicy.
func Declare(ch *amqp.Channel, t Topology) error {
	if err := ch.ExchangeDeclare(ExchangeJobs, amqp.ExchangeDirect, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", ExchangeJobs, err)
	}
	if err := ch.ExchangeDeclare(ExchangeDLX, amqp.ExchangeFanout, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", ExchangeDLX, err)
	}

	work := amqp.Table{"x-queue-type": "quorum", "x-dead-letter-exchange": ExchangeDLX}
	if err := declareQueue(ch, QueueWork, work); err != nil {
		return err
	}
	if err := ch.QueueBind(QueueWork, RoutingWork, ExchangeJobs, false, nil); err != nil {
		return fmt.Errorf("bind %s: %w", QueueWork, err)
	}

	for _, q := range []string{QueueDLQ, QueueDLQAudit} {
		if err := declareQueue(ch, q, nil); err != nil {
			return err
		}
		if err := ch.QueueBind(q, "", ExchangeDLX, false, nil); err != nil {
			return fmt.Errorf("bind %s: %w", q, err)
		}
	}

	seen := map[string]bool{}
	for _, d := range t.RetryDelays {
		name := RetryQueueName(d)
		if seen[name] {
			continue
		}
		seen[name] = true
		if err := declareQueue(ch, name, delayArgs(d)); err != nil {
			return err
		}
	}
	return declareQueue(ch, QueueDefer, delayArgs(t.DeferDelay))
}

func delayArgs(d time.Duration) amqp.Table {
	return amqp.Table{
		"x-message-ttl":             d.Milliseconds(),
		"x-dead-letter-exchange":    ExchangeJobs,
		"x-dead-letter-routing-key": RoutingWork,
	}
}

func declareQueue(ch *amqp.Channel, name string, args amqp.Table) error {
	if _, err := ch.QueueDeclare(name, true, false, false, false, args); err != nil {
		return fmt.Errorf("declare queue %s: %w", name, err)
	}
	return nil
}
