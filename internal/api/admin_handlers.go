package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ainote/internal/jobs"
	"ainote/internal/pipeline"
	"ainote/internal/queue"

	"github.com/google/uuid"
)

const (
	defaultDLQLimit = 50
	maxDLQLimit     = 200
)

// AdminStore is the persistence surface of the admin endpoints.
type AdminStore interface {
	ListDeadLettered(ctx context.Context, limit int) ([]jobs.DLQEntry, error)
	ResetForReplay(ctx context.Context, id uuid.UUID, outboxPayload []byte) (pipeline.Job, error)
}

// FaultAdmin reads and sets the runtime fault-injection flags (implemented in Task A12).
type FaultAdmin interface {
	Get(ctx context.Context) (map[string]string, error)
	Set(ctx context.Context, flags map[string]string) error
}

// registerAdmin adds the admin routes. DLQ routes need deps.Admin and fault
// routes need deps.Faults; each group is skipped (404) when its dependency is nil.
func (s *Server) registerAdmin(mux *http.ServeMux) {
	if s.deps.Admin != nil {
		mux.HandleFunc("GET /admin/dlq", s.adminOnly(s.listDLQ))
		mux.HandleFunc("POST /admin/dlq/{job_id}/replay", s.adminOnly(s.replayDLQ))
	}
	if s.deps.Faults != nil {
		mux.HandleFunc("GET /admin/faults", s.adminOnly(s.getFaults))
		mux.HandleFunc("PUT /admin/faults", s.adminOnly(s.putFaults))
	}
}

// adminOnly requires `Authorization: Bearer $ADMIN_TOKEN` (NFR-SEC-4). The
// comparison is constant-time; digests make the lengths equal first. An empty
// configured token rejects every request.
func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	want := sha256.Sum256([]byte(s.cfg.AdminToken))
	return func(w http.ResponseWriter, r *http.Request) {
		got, ok := bearerToken(r.Header.Get("Authorization"))
		sum := sha256.Sum256([]byte(got))
		match := subtle.ConstantTimeCompare(sum[:], want[:]) == 1
		if !ok || got == "" || s.cfg.AdminToken == "" || !match {
			WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid admin token")
			return
		}
		next(w, r)
	}
}

// bearerToken extracts the credentials of an Authorization header whose scheme
// is "Bearer", matched case-insensitively (RFC 7235 section 2.1).
func bearerToken(h string) (string, bool) {
	const scheme = "bearer "
	if len(h) < len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return "", false
	}
	return h[len(scheme):], true
}

func (s *Server) listDLQ(w http.ResponseWriter, r *http.Request) {
	limit := defaultDLQLimit
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 {
			WriteError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be a positive integer")
			return
		}
		limit = min(n, maxDLQLimit)
	}
	entries, err := s.deps.Admin.ListDeadLettered(r.Context(), limit)
	if err != nil {
		internalError(w, r, fmt.Errorf("list dead-lettered: %w", err))
		return
	}
	if entries == nil {
		entries = []jobs.DLQEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": entries})
}

type replayRequest struct {
	NotionToken string `json:"notion_token"`
}

func (s *Server) replayDLQ(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("job_id"))
	if err != nil {
		WriteError(w, http.StatusNotFound, "JOB_NOT_FOUND", "job not found")
		return
	}
	var req replayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body must be at most 16 KB")
			return
		}
		WriteError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}
	if req.NotionToken == "" {
		WriteError(w, http.StatusBadRequest, "MISSING_NOTION_TOKEN", "notion_token is required")
		return
	}
	enc, err := s.deps.Sealer.Seal(req.NotionToken)
	if err != nil {
		internalError(w, r, fmt.Errorf("seal token: %w", err))
		return
	}
	payload, err := json.Marshal(queue.Message{V: 1, JobID: id, NotionTokenEnc: enc, EnqueuedAt: time.Now().UTC(), Reason: "replay"})
	if err != nil {
		internalError(w, r, err)
		return
	}
	job, err := s.deps.Admin.ResetForReplay(r.Context(), id, payload)
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		WriteError(w, http.StatusNotFound, "JOB_NOT_FOUND", "job not found")
		return
	case errors.Is(err, jobs.ErrNotDeadLettered):
		WriteError(w, http.StatusConflict, "JOB_NOT_DEAD_LETTERED", "only dead-lettered jobs can be replayed")
		return
	case err != nil:
		internalError(w, r, fmt.Errorf("reset for replay: %w", err))
		return
	}
	if s.deps.Outbox != nil {
		s.deps.Outbox.Kick()
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": job.ID.String(), "status": string(job.Status)})
}

func (s *Server) getFaults(w http.ResponseWriter, r *http.Request) {
	flags, err := s.deps.Faults.Get(r.Context())
	if err != nil {
		internalError(w, r, fmt.Errorf("get faults: %w", err))
		return
	}
	if flags == nil {
		flags = map[string]string{}
	}
	writeJSON(w, http.StatusOK, flags)
}

func (s *Server) putFaults(w http.ResponseWriter, r *http.Request) {
	var flags map[string]string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&flags); err != nil || flags == nil {
		WriteError(w, http.StatusBadRequest, "INVALID_JSON", "body must be a JSON object of string flags")
		return
	}
	if err := s.deps.Faults.Set(r.Context(), flags); err != nil {
		internalError(w, r, fmt.Errorf("set faults: %w", err))
		return
	}
	writeJSON(w, http.StatusOK, flags)
}
