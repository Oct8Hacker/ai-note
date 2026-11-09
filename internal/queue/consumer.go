package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Delivery is a decoded message plus its raw AMQP delivery.
type Delivery struct {
	Msg Message
	Raw amqp.Delivery
}

// DeliveryCount returns the broker's x-delivery-count header (0 on the first
// delivery).
func (d Delivery) DeliveryCount() int {
	switch v := d.Raw.Headers["x-delivery-count"].(type) {
	case int64:
		return int(v)
	case int32:
		return int(v)
	case int:
		return v
	}
	return 0
}

// Ack acknowledges the delivery.
func (d Delivery) Ack() error { return d.Raw.Ack(false) }

// NackRequeue negatively acknowledges the delivery and requeues it.
func (d Delivery) NackRequeue() error { return d.Raw.Nack(false, true) }

// Consume reads queue with manual acknowledgement and the given prefetch,
// calling handle for each delivery (up to prefetch concurrently), and
// resubscribes after connection loss. It returns nil once ctx is done and all
// in-flight handlers have returned.
func Consume(ctx context.Context, c *Conn, queue string, prefetch int, handle func(context.Context, Delivery)) error {
	if prefetch < 1 {
		prefetch = 1
	}
	for {
		err := consumeOnce(ctx, c, queue, prefetch, handle)
		if ctx.Err() != nil || errors.Is(err, ErrConnClosed) {
			return nil
		}
		c.log.Warn("consumer stopped, resubscribing", "queue", queue, "err", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(minBackoff):
		}
	}
}

// consumeOnce runs one subscription until its delivery channel closes or ctx
// is done. It always lets this subscription's in-flight handlers finish
// (so their acks still land) before closing the channel.
func consumeOnce(ctx context.Context, c *Conn, queue string, prefetch int, handle func(context.Context, Delivery)) error {
	ch, err := c.Channel(ctx)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	tag := "ainote-" + uuid.NewString()
	defer func() {
		_ = ch.Cancel(tag, false) // stop new deliveries; error means channel already dead
		wg.Wait()                 // let in-flight handlers ack
		_ = ch.Close()            // requeues anything still unacked
	}()
	if err := ch.Qos(prefetch, 0, false); err != nil {
		return fmt.Errorf("qos: %w", err)
	}
	deliveries, err := ch.Consume(queue, tag, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume %s: %w", queue, err)
	}

	sem := make(chan struct{}, prefetch)
	for {
		select {
		case <-ctx.Done():
			return nil
		case raw, ok := <-deliveries:
			if !ok {
				return errors.New("delivery channel closed")
			}
			var msg Message
			if err := json.Unmarshal(raw.Body, &msg); err != nil {
				c.log.Error("dropping undecodable message", "queue", queue, "err", err)
				_ = raw.Reject(false) // poison: dead-letter, never requeue
				continue
			}
			d := Delivery{Msg: msg, Raw: raw}
			if prefetch == 1 {
				handle(ctx, d)
				continue
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				_ = raw.Nack(false, true)
				return nil
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				handle(ctx, d)
			}()
		}
	}
}
