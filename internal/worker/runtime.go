package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"ainote/internal/crypto"
	"ainote/internal/jobs"
	"ainote/internal/pipeline"
	"ainote/internal/queue"
)

// finishTimeout bounds the bookkeeping that must complete even after the
// delivery context is cancelled (state commits, publishes, lease release).
const finishTimeout = 15 * time.Second

// Config tunes a Runtime.
type Config struct {
	WorkerID      string
	Prefetch      int
	LeaseTTL      time.Duration
	ShutdownGrace time.Duration

	MaxAttempts         int // failure budget per job (SRS FR-RET)
	MaxRateLimitRetries int // rate-limit budget, separate from MaxAttempts
	MaxDeferrals        int // "job is busy elsewhere" budget
	DeliveryLimit       int // broker redelivery cap, enforced here one early

	RetryDelays []time.Duration
	DeferDelay  time.Duration // only recorded as the job's next_attempt_at on a defer
}

// Runtime consumes jobs.work and drives each job through its stages.
type Runtime struct {
	cfg     Config
	repo    *jobs.Repo
	pub     *queue.Publisher
	rdb     *redis.Client
	sealer  *crypto.Sealer
	stages  map[pipeline.Status]pipeline.Stage
	limiter pipeline.Limiter
	faults  pipeline.Faults
	log     *slog.Logger
}

// New builds a Runtime. stages is keyed by the status in which each stage runs.
func New(cfg Config, repo *jobs.Repo, pub *queue.Publisher, rdb *redis.Client, sealer *crypto.Sealer,
	stages map[pipeline.Status]pipeline.Stage, limiter pipeline.Limiter, faults pipeline.Faults, log *slog.Logger) *Runtime {
	return &Runtime{cfg: cfg, repo: repo, pub: pub, rdb: rdb, sealer: sealer,
		stages: stages, limiter: limiter, faults: faults, log: log}
}

// Run consumes jobs.work until ctx is cancelled (the shutdown signal). Jobs
// already running get ShutdownGrace to finish; after that their context is
// cancelled and their messages requeued. Run returns once all handlers have.
func (rt *Runtime) Run(ctx context.Context, conn *queue.Conn) error {
	hard, cancelHard := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelHard()
	go func() {
		select {
		case <-hard.Done():
		case <-ctx.Done():
			t := time.NewTimer(rt.cfg.ShutdownGrace)
			defer t.Stop()
			select {
			case <-t.C:
				cancelHard()
			case <-hard.Done():
			}
		}
	}()
	return queue.Consume(ctx, conn, queue.QueueWork, rt.cfg.Prefetch, func(_ context.Context, d queue.Delivery) {
		if ctx.Err() != nil { // shutting down: take no new work
			rt.settle(d, nack, rt.log)
			return
		}
		rt.Handle(hard, d)
	})
}

type action int

const (
	ack action = iota
	nack
)

// settle acknowledges or requeues d.
func (rt *Runtime) settle(d queue.Delivery, a action, log *slog.Logger) {
	var err error
	if a == ack {
		err = d.Ack()
	} else {
		err = d.NackRequeue()
	}
	if err != nil {
		log.Warn("settling delivery failed", "err", err, "ack", a == ack)
	}
}

func finishCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), finishTimeout)
}

// Handle processes one delivery. It is the only place a job moves between
// stages. DB state is committed before any publish, and a publish is
// confirmed before the delivery is acked (FR-RET-3, FR-MQ-3). ctx cancelled
// means shutdown (grace exceeded): the delivery is requeued.
func (rt *Runtime) Handle(ctx context.Context, d queue.Delivery) {
	log := rt.log.With("job_id", d.Msg.JobID, "worker_id", rt.cfg.WorkerID)

	if d.DeliveryCount() >= rt.cfg.DeliveryLimit-1 {
		log.Warn("delivery limit reached", "deliveries", d.DeliveryCount())
		rt.settle(d, rt.deadLetter(d.Msg, "DELIVERY_LIMIT_EXCEEDED", "message redelivered too many times", log), log)
		return
	}

	cctx, cancel := finishCtx()
	job, outcome, err := rt.repo.Claim(cctx, d.Msg.JobID, rt.cfg.WorkerID, rt.cfg.LeaseTTL,
		func(owner string) (bool, error) { return IsAlive(cctx, rt.rdb, owner) })
	cancel()
	if err != nil {
		log.Error("claim failed", "err", err)
		rt.settle(d, nack, log)
		return
	}
	switch outcome {
	case jobs.Terminal, jobs.NotFound:
		log.Info("nothing to do", "outcome", int(outcome))
		rt.settle(d, ack, log)
		return
	case jobs.Busy:
		rt.settle(d, rt.deferJob(d.Msg, log), log)
		return
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	var lost atomic.Bool
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		rt.renewLoop(runCtx, cancelRun, d.Msg.JobID, &lost, log)
	}()

	a := rt.process(ctx, runCtx, &lost, d, job, log)

	cancelRun()
	<-renewDone
	// Release before settling so a requeued or retried message never finds our
	// lease still held. It is a no-op when another worker has taken over.
	rctx, rcancel := finishCtx()
	if err := rt.repo.ReleaseLease(rctx, d.Msg.JobID, rt.cfg.WorkerID); err != nil {
		log.Warn("release lease failed", "err", err)
	}
	rcancel()
	rt.settle(d, a, log)
}

// renewLoop extends the lease every LeaseTTL/3. If the lease is lost it flags
// it and cancels the job's context so the stage stops.
func (rt *Runtime) renewLoop(ctx context.Context, cancelRun context.CancelFunc, id uuid.UUID, lost *atomic.Bool, log *slog.Logger) {
	t := time.NewTicker(max(rt.cfg.LeaseTTL/3, 10*time.Millisecond))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := rt.repo.RenewLease(ctx, id, rt.cfg.WorkerID, rt.cfg.LeaseTTL)
			switch {
			case err == nil:
			case errors.Is(err, jobs.ErrLeaseLost):
				log.Warn("lease lost, abandoning job")
				lost.Store(true)
				cancelRun()
				return
			case ctx.Err() == nil:
				log.Warn("lease renewal failed", "err", err)
			}
		}
	}
}

// process runs the stage loop for a claimed job and says how to settle d.
func (rt *Runtime) process(ctx, runCtx context.Context, lost *atomic.Bool, d queue.Delivery, job pipeline.Job, log *slog.Logger) action {
	id := job.ID
	if job.Status == pipeline.StatusQueued {
		fctx, cancel := finishCtx()
		defer cancel()
		if err := rt.repo.Transition(fctx, id, pipeline.StatusTranscribing, rt.cfg.WorkerID, nil); err != nil {
			log.Error("transition to TRANSCRIBING failed", "err", err)
			return nack
		}
		var err error
		if job, err = rt.repo.GetJob(fctx, id); err != nil {
			log.Error("reload job failed", "err", err)
			return nack
		}
	}

	for {
		if ctx.Err() != nil {
			return nack
		}
		if lost.Load() {
			return ack
		}
		cur := job.Status
		stage, ok := rt.stages[cur]
		if !ok {
			return rt.fail(id, pipeline.StatusFailed, "INTERNAL", "no stage registered for "+string(cur), log)
		}
		token := ""
		if cur == pipeline.StatusPublishing { // the only stage that needs the Notion token
			var err error
			if token, err = rt.sealer.Open(d.Msg.NotionTokenEnc); err != nil {
				return rt.fail(id, pipeline.StatusFailed, "INTERNAL", "notion token could not be decrypted", log)
			}
		}
		slg := log.With("stage", string(cur))
		deps := rt.buildDeps(runCtx, job, token, slg)

		slg.Info("stage starting")
		res, err := stage.Run(runCtx, &job, deps)
		if err != nil {
			return rt.stageError(ctx, lost, d, err, slg)
		}
		if lost.Load() {
			return ack // another worker owns the job now; it resumes from checkpoints
		}

		fctx, cancel := finishCtx()
		next, err := rt.repo.CompleteStage(fctx, id, cur, res, rt.cfg.WorkerID)
		if errors.Is(err, jobs.ErrIllegalTransition) {
			cancel()
			slg.Warn("stage already completed elsewhere", "err", err)
			return ack
		}
		if err != nil {
			cancel()
			slg.Error("complete stage failed", "err", err)
			return nack
		}
		slg.Info("stage completed", "next", string(next))
		if next.IsTerminal() {
			cancel()
			return ack
		}
		job, err = rt.repo.GetJob(fctx, id)
		cancel()
		if err != nil {
			slg.Error("reload job failed", "err", err)
			return nack
		}
	}
}

// stageError decides between requeue, retry, dead-letter and fail (steps 6-7).
func (rt *Runtime) stageError(ctx context.Context, lost *atomic.Bool, d queue.Delivery, err error, log *slog.Logger) action {
	if ctx.Err() != nil {
		log.Info("shutdown during stage, requeueing", "err", err)
		return nack
	}
	if lost.Load() {
		return ack
	}
	id := d.Msg.JobID
	log.Warn("stage failed", "err", err, "code", pipeline.ErrorCode(err))
	switch pipeline.Classify(err) {
	case pipeline.ClassFatal:
		return rt.fail(id, pipeline.StatusFailed, pipeline.ErrorCode(err), err.Error(), log)
	case pipeline.ClassRateLimited:
		var rl *pipeline.RateLimitedError
		errors.As(err, &rl)
		return rt.retry(d, jobs.RetryRateLimit, rl.Provider, rl.RetryAfter, rt.cfg.MaxRateLimitRetries,
			"RATE_LIMITED", "RATE_LIMIT_RETRIES_EXHAUSTED", log)
	default:
		return rt.retry(d, jobs.RetryFailure, "", 0, rt.cfg.MaxAttempts,
			pipeline.ErrorCode(err), "RETRIES_EXHAUSTED", log)
	}
}

// retry records the retry in Postgres, then publishes to a delay queue.
func (rt *Runtime) retry(d queue.Delivery, kind jobs.RetryKind, provider string, retryAfter time.Duration,
	budget int, errCode, exhaustedCode string, log *slog.Logger) action {
	fctx, cancel := finishCtx()
	defer cancel()
	dec, err := rt.repo.ScheduleRetry(fctx, d.Msg.JobID, kind, provider, rt.cfg.RetryDelays, retryAfter, budget, errCode)
	if err != nil {
		log.Error("schedule retry failed", "err", err)
		return nack
	}
	if dec.Exhausted {
		return rt.deadLetter(d.Msg, exhaustedCode, "retry budget exhausted", log)
	}
	if err := rt.pub.PublishRetry(fctx, d.Msg, dec.Delay); err != nil {
		log.Error("publish retry failed", "err", err)
		return nack
	}
	log.Info("retry scheduled", "kind", string(kind), "n", dec.N, "delay", dec.Delay)
	return ack
}

// deferJob handles a job another live worker holds: back off briefly, or
// dead-letter once MaxDeferrals is spent.
func (rt *Runtime) deferJob(m queue.Message, log *slog.Logger) action {
	fctx, cancel := finishCtx()
	defer cancel()
	dec, err := rt.repo.ScheduleRetry(fctx, m.JobID, jobs.RetryDefer, "", []time.Duration{rt.cfg.DeferDelay}, 0, rt.cfg.MaxDeferrals, "")
	if err != nil {
		if errors.Is(err, jobs.ErrIllegalTransition) { // finished meanwhile
			return ack
		}
		log.Error("schedule defer failed", "err", err)
		return nack
	}
	if dec.Exhausted {
		return rt.deadLetter(m, "RETRIES_EXHAUSTED", "job stayed busy on another worker", log)
	}
	if err := rt.pub.PublishDefer(fctx, m); err != nil {
		log.Error("publish defer failed", "err", err)
		return nack
	}
	log.Info("job busy elsewhere, deferred", "n", dec.N)
	return ack
}

// fail marks the job terminal (FAILED or DEAD_LETTERED) without publishing.
func (rt *Runtime) fail(id uuid.UUID, terminal pipeline.Status, code, msg string, log *slog.Logger) action {
	fctx, cancel := finishCtx()
	defer cancel()
	err := rt.repo.Fail(fctx, id, terminal, code, msg)
	if err != nil && !errors.Is(err, jobs.ErrIllegalTransition) {
		log.Error("fail job failed", "err", err)
		return nack
	}
	log.Warn("job failed", "status", string(terminal), "code", code)
	return ack
}

// deadLetter marks the job DEAD_LETTERED, then publishes it to jobs.dlx.
func (rt *Runtime) deadLetter(m queue.Message, code, msg string, log *slog.Logger) action {
	fctx, cancel := finishCtx()
	defer cancel()
	err := rt.repo.Fail(fctx, m.JobID, pipeline.StatusDeadLettered, code, msg)
	if errors.Is(err, jobs.ErrIllegalTransition) {
		return ack // already terminal (or gone): nothing to dead-letter
	}
	if err != nil {
		log.Error("dead-letter state change failed", "err", err)
		return nack
	}
	if err := rt.pub.PublishDeadLetter(fctx, m, code); err != nil {
		log.Error("publish dead letter failed", "err", err)
		return nack
	}
	log.Warn("job dead-lettered", "code", code)
	return ack
}
