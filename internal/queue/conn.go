package queue

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ErrConnClosed is returned once Close has been called on a Conn.
var ErrConnClosed = errors.New("queue: connection closed")

const (
	minBackoff = time.Second
	maxBackoff = 10 * time.Second
)

// Conn wraps an AMQP connection and redials it (1s to 10s backoff) when the
// broker drops it.
type Conn struct {
	url       string
	heartbeat time.Duration
	log       *slog.Logger

	mu      sync.Mutex
	conn    *amqp.Connection // nil while disconnected
	changed chan struct{}    // closed and replaced on every state change
	closed  bool
	done    chan struct{}
}

// Dial makes the first connection (failing fast if it cannot) and starts
// supervising it.
func Dial(ctx context.Context, url string, heartbeat time.Duration, log *slog.Logger) (*Conn, error) {
	c := &Conn{
		url: url, heartbeat: heartbeat, log: log,
		changed: make(chan struct{}), done: make(chan struct{}),
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.dial()
	if err != nil {
		return nil, err
	}
	c.conn = conn
	go c.supervise(conn)
	return c, nil
}

func (c *Conn) dial() (*amqp.Connection, error) {
	return amqp.DialConfig(c.url, amqp.Config{
		Heartbeat: c.heartbeat,
		Locale:    "en_US",
		Dial:      amqp.DefaultDial(5 * time.Second),
	})
}

// supervise waits for conn to drop and then redials until it succeeds or the
// Conn is closed.
func (c *Conn) supervise(conn *amqp.Connection) {
	for {
		notify := conn.NotifyClose(make(chan *amqp.Error, 1))
		var amqpErr *amqp.Error
		select {
		case <-c.done:
			return
		case amqpErr = <-notify:
		}
		if amqpErr == nil { // graceful close initiated by us
			return
		}
		c.log.Warn("amqp connection lost, reconnecting", "err", amqpErr)
		c.setConn(nil)

		backoff := minBackoff
		for {
			select {
			case <-c.done:
				return
			case <-time.After(backoff):
			}
			next, err := c.dial()
			if err == nil {
				if !c.setConn(next) { // closed while dialing
					_ = next.Close()
					return
				}
				c.log.Info("amqp reconnected")
				conn = next
				break
			}
			c.log.Warn("amqp redial failed", "err", err, "retry_in", backoff)
			backoff = min(backoff*2, maxBackoff)
		}
	}
}

// setConn swaps the current connection and wakes waiters. It reports false if
// the Conn has been closed.
func (c *Conn) setConn(conn *amqp.Connection) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	c.conn = conn
	close(c.changed)
	c.changed = make(chan struct{})
	return true
}

// Channel opens a fresh channel on the current connection, waiting for a
// reconnect (up to ctx) if the connection is down.
func (c *Conn) Channel(ctx context.Context) (*amqp.Channel, error) {
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, ErrConnClosed
		}
		conn, changed := c.conn, c.changed
		c.mu.Unlock()

		if conn != nil && !conn.IsClosed() {
			ch, err := conn.Channel()
			if err == nil {
				return ch, nil
			}
			if !errors.Is(err, amqp.ErrClosed) {
				return nil, err
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.done:
			return nil, ErrConnClosed
		case <-changed:
		case <-time.After(100 * time.Millisecond): // connection closed but not yet noticed
		}
	}
}

// Close stops reconnection and closes the current connection.
func (c *Conn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.done)
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if conn != nil && !conn.IsClosed() {
		return conn.Close()
	}
	return nil
}
