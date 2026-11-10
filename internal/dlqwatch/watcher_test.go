package dlqwatch

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"ainote/internal/jobs"
	"ainote/internal/pipeline"
	"ainote/internal/queue"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
)

type fakeAck struct {
	mu            sync.Mutex
	acks, requeue int
	done          chan struct{}
}

func newAck() *fakeAck { return &fakeAck{done: make(chan struct{}, 8)} }

func (a *fakeAck) Ack(uint64, bool) error {
	a.mu.Lock()
	a.acks++
	a.mu.Unlock()
	a.done <- struct{}{}
	return nil
}
func (a *fakeAck) Nack(_ uint64, _ bool, requeue bool) error {
	a.mu.Lock()
	if requeue {
		a.requeue++
	}
	a.mu.Unlock()
	a.done <- struct{}{}
	return nil
}
func (a *fakeAck) Reject(uint64, bool) error { return nil }

type fakeRepo struct {
	view    jobs.JobView
	viewErr error
	failErr error
	failed  []string
}

func (f *fakeRepo) GetJobView(context.Context, uuid.UUID) (jobs.JobView, error) {
	return f.view, f.viewErr
}

func (f *fakeRepo) Fail(_ context.Context, _ uuid.UUID, st pipeline.Status, code, _ string) error {
	if f.failErr != nil {
		return f.failErr
	}
	f.failed = append(f.failed, string(st)+"/"+code)
	return nil
}

func run(t *testing.T, repo *fakeRepo) (*fakeAck, *time.Duration) {
	t.Helper()
	a := newAck()
	var slept time.Duration
	w := &watcher{repo: repo, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		backoff: 50 * time.Millisecond,
		sleep:   func(_ context.Context, d time.Duration) { slept = d }}
	w.handle(context.Background(), queue.Delivery{
		Msg: queue.Message{V: 1, JobID: uuid.New()},
		Raw: amqp.Delivery{Acknowledger: a},
	})
	return a, &slept
}

func TestHandleDeadLettersNonTerminal(t *testing.T) {
	repo := &fakeRepo{view: jobs.JobView{Status: pipeline.StatusSummarizing}}
	a, _ := run(t, repo)
	require.Equal(t, []string{"DEAD_LETTERED/DELIVERY_LIMIT_EXCEEDED"}, repo.failed)
	require.Equal(t, 1, a.acks)
}

func TestHandleTerminalAcksWithoutChange(t *testing.T) {
	for _, st := range []pipeline.Status{pipeline.StatusCompleted, pipeline.StatusFailed, pipeline.StatusDeadLettered} {
		repo := &fakeRepo{view: jobs.JobView{Status: st}}
		a, _ := run(t, repo)
		require.Empty(t, repo.failed)
		require.Equal(t, 1, a.acks)
	}
}

func TestHandleMissingJobAcks(t *testing.T) {
	repo := &fakeRepo{viewErr: jobs.ErrNotFound}
	a, _ := run(t, repo)
	require.Empty(t, repo.failed)
	require.Equal(t, 1, a.acks)
}

func TestHandleRaceToTerminalAcks(t *testing.T) {
	repo := &fakeRepo{view: jobs.JobView{Status: pipeline.StatusSummarizing}, failErr: jobs.ErrIllegalTransition}
	a, _ := run(t, repo)
	require.Equal(t, 1, a.acks)
}

func TestHandleDBErrorRequeuesWithBackoff(t *testing.T) {
	for _, repo := range []*fakeRepo{
		{viewErr: errors.New("db down")},
		{view: jobs.JobView{Status: pipeline.StatusQueued}, failErr: errors.New("db down")},
	} {
		a, slept := run(t, repo)
		require.Equal(t, 0, a.acks)
		require.Equal(t, 1, a.requeue)
		require.Equal(t, 50*time.Millisecond, *slept)
	}
}
