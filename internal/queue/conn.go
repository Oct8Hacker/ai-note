package queue

import (
	"context"
	"errors"
	"fmt"
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
	onConnect func(*amqp.Channel) error

	mu      sync.Mutex
	conn    *amqp.Connection // nil while disconnected
	changed chan struct{}    // closed and replaced on every state change
	closed  bool
	done    chan struct{}
}

// Dial makes the first connection (failing fast if it cannot) and starts
// supervising it. onConnect, if non-nil, runs on a scratch channel after the
// initial connection and after every reconnect (for idempotent topology
// declaration); an error from it fails the Dial or triggers another redial.
func Dial(ctx context.Context, url string, heartbeat time.Duration, log *slog.Logger, onConnect func(*amqp.Channel) error) (*Conn, error) {
	c := &Conn{
		url: url, heartbeat: heartbeat, log: log, onConnect: onConnect,
		changed: make(chan struct{}), done: make(chan struct{}),
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, notify, err := c.connect()
	if err != nil {
		return nil, err
	}
	c.conn = conn
	go c.supervise(notify)
	return c, nil
}

// connect dials, registers the close notification BEFORE the connection is
// published (so a drop can never be missed) and runs the onConnect hook.
func (c *Conn) connect() (*amqp.Connection, chan *amqp.Error, error) {
	conn, err := amqp.DialConfig(c.url, amqp.Config{
		Heartbeat: c.heartbeat,
		Locale:    "en_US",
		Dial:      amqp.DefaultDial(5 * time.Second),
	})
	if err != nil {
		return nil, nil, err
	}
	notify := conn.NotifyClose(make(chan *amqp.Error, 1))
	if c.onConnect != nil {
		if err := c.runOnConnect(conn); err != nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("on-connect hook: %w", err)
		}
	}
	return conn, notify, nil
}

func (c *Conn) runOnConnect(conn *amqp.Connection) error {
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	herr := c.onConnect(ch)
	if cerr := ch.Close(); herr == nil && cerr != nil && !errors.Is(cerr, amqp.ErrClosed) {
		return cerr
	}
	return herr
}

func (c *Conn) isDone() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// supervise waits for the connection to drop and then redials until it
// succeeds or the Conn is closed. A close notification only counts as
// graceful when Close was called; anything else (including a nil error from a
// notify channel closed on an already-dead connection) triggers a redial.
func (c *Conn) supervise(notify chan *amqp.Error) {
	for {
		var amqpErr *amqp.Error
		select {
		case <-c.done:
			return
		case amqpErr = <-notify:
		}
		if c.isDone() {
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
			next, nextNotify, err := c.connect()
			if err == nil {
				if !c.setConn(next) { // closed while dialing
					_ = next.Close()
					return
				}
				c.log.Info("amqp reconnected")
				notify = nextNotify
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
