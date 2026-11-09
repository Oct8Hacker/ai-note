package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ainote/internal/pipeline"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ClaimOutcome is the result of a Claim attempt.
type ClaimOutcome int

const (
	// Claimed means the caller now holds the lease.
	Claimed ClaimOutcome = iota
	// Busy means another live worker holds a valid lease.
	Busy
	// Terminal means the job is already finished.
	Terminal
	// NotFound means the job does not exist.
	NotFound
)

// ErrLeaseLost is returned by RenewLease when the caller no longer owns the lease.
var ErrLeaseLost = errors.New("lease lost")

// Claim takes the job's lease for workerID. The lease is taken unless another
// owner holds an unexpired lease AND ownerAlive(owner) is true. All time
// comparisons use the database clock. The conditional UPDATE on the observed
// owner makes concurrent claims race-free: the loser gets Busy.
func (r *Repo) Claim(ctx context.Context, id uuid.UUID, workerID string, ttl time.Duration,
	ownerAlive func(owner string) (bool, error)) (pipeline.Job, ClaimOutcome, error) {
	var (
		owner  *string
		valid  bool
		status string
	)
	err := r.pool.QueryRow(ctx,
		`SELECT lease_owner, COALESCE(lease_until > now(), false), status FROM jobs WHERE id=$1`, id).
		Scan(&owner, &valid, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return pipeline.Job{}, NotFound, nil
	}
	if err != nil {
		return pipeline.Job{}, Busy, fmt.Errorf("claim read: %w", err)
	}
	if pipeline.Status(status).IsTerminal() {
		return pipeline.Job{}, Terminal, nil
	}
	if owner != nil && *owner != workerID && valid {
		ok, err := ownerAlive(*owner)
		if err != nil {
			return pipeline.Job{}, Busy, err
		}
		if ok {
			return pipeline.Job{}, Busy, nil
		}
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE jobs SET lease_owner=$2, lease_until=now() + $3::interval
		 WHERE id=$1 AND status = ANY($4) AND lease_owner IS NOT DISTINCT FROM $5`,
		id, workerID, fmt.Sprintf("%d microseconds", ttl.Microseconds()), statusStrings(nonTerminal), owner)
	if err != nil {
		return pipeline.Job{}, Busy, fmt.Errorf("claim update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pipeline.Job{}, Busy, nil
	}
	j, err := r.GetJob(ctx, id)
	if err != nil {
		return pipeline.Job{}, Busy, err
	}
	return j, Claimed, nil
}

// RenewLease extends the lease; ErrLeaseLost if workerID no longer owns it.
func (r *Repo) RenewLease(ctx context.Context, id uuid.UUID, workerID string, ttl time.Duration) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE jobs SET lease_until=now() + $3::interval WHERE id=$1 AND lease_owner=$2`,
		id, workerID, fmt.Sprintf("%d microseconds", ttl.Microseconds()))
	if err != nil {
		return fmt.Errorf("renew lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// ReleaseLease clears the lease if workerID owns it; otherwise a no-op.
func (r *Repo) ReleaseLease(ctx context.Context, id uuid.UUID, workerID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE jobs SET lease_owner=NULL, lease_until=NULL WHERE id=$1 AND lease_owner=$2`, id, workerID)
	if err != nil {
		return fmt.Errorf("release lease: %w", err)
	}
	return nil
}
