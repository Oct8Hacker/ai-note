package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"ainote/internal/config"
	"ainote/internal/crypto"
	"ainote/internal/health"
	"ainote/internal/jobs"
	"ainote/internal/pipeline"

	"github.com/google/uuid"
)

// JobStore is the persistence surface the API needs.
type JobStore interface {
	CreateJob(ctx context.Context, in jobs.NewJob, outboxPayload []byte) (pipeline.Job, bool, error)
	GetJobView(ctx context.Context, id uuid.UUID) (jobs.JobView, error)
}

// OutboxKicker nudges the outbox relay after a job is committed.
type OutboxKicker interface{ Kick() }

// ServerDeps are the collaborators of Server.
type ServerDeps struct {
	Jobs   JobStore
	Sealer *crypto.Sealer
	Outbox OutboxKicker
	Health *health.Checker
	// Admin enables GET /admin/dlq and the replay route when non-nil.
	Admin AdminStore
	// Faults enables GET/PUT /admin/faults when non-nil (wired by Task A12).
	Faults FaultAdmin
	Notion NotionGateway
}

// Server is the gateway HTTP API.
type Server struct {
	cfg  config.Gateway
	deps ServerDeps
}

// NewServer returns a Server.
func NewServer(cfg config.Gateway, deps ServerDeps) *Server {
	return &Server{cfg: cfg, deps: deps}
}

// Handler returns the fully routed handler (CORS, error bodies included).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs", s.postJob)
	mux.HandleFunc("GET /jobs/{job_id}", s.getJob)
	mux.HandleFunc("GET /jobs/{job_id}/results", s.getResults)
	s.registerAdmin(mux)
	if s.deps.Notion != nil {
		s.registerNotion(mux)
	}
	if s.deps.Health != nil {
		mux.HandleFunc("GET /healthz", HealthHandler(s.deps.Health))
	}
	return withCORS(s.cfg.CORSAllowedOrigins, jsonErrors(mux))
}

// jsonErrors rewrites the mux's plain-text 404/405 replies into the standard
// error body. Our handlers only emit JSON, so a text/plain
// 404/405 identifies a mux-generated reply.
func jsonErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&errRewriter{ResponseWriter: w}, r)
	})
}

type errRewriter struct {
	http.ResponseWriter
	swallow bool
}

func (e *errRewriter) WriteHeader(status int) {
	if strings.HasPrefix(e.Header().Get("Content-Type"), "text/plain") && (status == http.StatusNotFound || status == http.StatusMethodNotAllowed) {
		e.swallow = true
		h := e.Header()
		h.Del("X-Content-Type-Options")
		h.Del("Content-Type")
		if status == http.StatusNotFound {
			WriteError(e.ResponseWriter, status, "NOT_FOUND", "no such endpoint")
		} else {
			WriteError(e.ResponseWriter, status, "METHOD_NOT_ALLOWED", "method not allowed for this endpoint")
		}
		return
	}
	e.ResponseWriter.WriteHeader(status)
}

func (e *errRewriter) Write(b []byte) (int, error) {
	if e.swallow {
		return len(b), nil
	}
	return e.ResponseWriter.Write(b)
}

func internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	WriteError(w, http.StatusInternalServerError, "INTERNAL", "internal error")
}
