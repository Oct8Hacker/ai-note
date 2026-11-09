package queue

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestPollReturn(t *testing.T) {
	ch := make(chan amqp.Return, 1)

	if _, got, open := pollReturn(ch); got || !open {
		t.Fatalf("empty open chan: got=%v open=%v", got, open)
	}
	ch <- amqp.Return{ReplyCode: 312}
	if r, got, open := pollReturn(ch); !got || !open || r.ReplyCode != 312 {
		t.Fatalf("buffered return: r=%+v got=%v open=%v", r, got, open)
	}
	close(ch)
	if _, got, open := pollReturn(ch); got || open {
		t.Fatalf("closed chan must report closed, no return: got=%v open=%v", got, open)
	}
}
