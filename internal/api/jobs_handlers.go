package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"time"

	"ainote/internal/jobs"
	"ainote/internal/pipeline"
	"ainote/internal/queue"

	"github.com/google/uuid"
)

const (
	maxBodyBytes  = 16 * 1024
	maxIdemKeyLen = 200
)

type postJobRequest struct {
	YouTubeURL     string `json:"youtube_url"`
	NotionToken    string `json:"notion_token"`
	NotionParentID string `json:"notion_parent_id"`
}

func (s *Server) postJob(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		WriteError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json")
		return
	}
	idem := r.Header.Get("Idempotency-Key")
	if len(idem) > maxIdemKeyLen {
		WriteError(w, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must be at most 200 characters")
		return
	}

	var req postJobRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "request body must be at most 16 KB")
			return
		}
		WriteError(w, http.StatusBadRequest, "INVALID_JSON", "request body is not valid JSON")
		return
	}

	videoID, err := ParseVideoID(req.YouTubeURL)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_URL", "youtube_url must be a YouTube video link")
		return
	}
	if req.NotionToken == "" {
		WriteError(w, http.StatusBadRequest, "MISSING_NOTION_TOKEN", "notion_token is required")
		return
	}
	parent := ""
	if req.NotionParentID != "" {
		if parent, err = ParseNotionID(req.NotionParentID); err != nil {
			WriteError(w, http.StatusBadRequest, "INVALID_NOTION_PARENT", "notion_parent_id must be a Notion page or database ID")
			return
		}
	}

	enc, err := s.deps.Sealer.Seal(req.NotionToken)
	if err != nil {
		internalError(w, r, fmt.Errorf("seal token: %w", err))
		return
	}
	id := uuid.New()
	payload, err := json.Marshal(queue.Message{V: 1, JobID: id, NotionTokenEnc: enc, EnqueuedAt: time.Now().UTC(), Reason: "new"})
	if err != nil {
		internalError(w, r, err)
		return
	}

	job, created, err := s.deps.Jobs.CreateJob(r.Context(), jobs.NewJob{
		ID: id, VideoID: videoID, YouTubeURL: "https://www.youtube.com/watch?v=" + videoID, NotionParentID: parent, IdempotencyKey: idem,
	}, payload)
	if err != nil {
		internalError(w, r, fmt.Errorf("create job: %w", err))
		return
	}
	if created && s.deps.Outbox != nil {
		s.deps.Outbox.Kick()
	}

	w.Header().Set("Location", "/jobs/"+job.ID.String())
	writeJSON(w, http.StatusAccepted, map[string]string{
		"job_id": job.ID.String(), "status": string(job.Status), "video_id": job.VideoID,
	})
}

// loadView fetches the job view for the {job_id} path value, writing the
// error response itself and returning false on failure.
func (s *Server) loadView(w http.ResponseWriter, r *http.Request) (jobs.JobView, bool) {
	id, err := uuid.Parse(r.PathValue("job_id"))
	if err != nil {
		WriteError(w, http.StatusNotFound, "JOB_NOT_FOUND", "job not found")
		return jobs.JobView{}, false
	}
	v, err := s.deps.Jobs.GetJobView(r.Context(), id)
	if errors.Is(err, jobs.ErrNotFound) {
		WriteError(w, http.StatusNotFound, "JOB_NOT_FOUND", "job not found")
		return jobs.JobView{}, false
	}
	if err != nil {
		internalError(w, r, fmt.Errorf("get job view: %w", err))
		return jobs.JobView{}, false
	}
	return v, true
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.loadView(w, r); ok {
		if v.Warnings == nil {
			v.Warnings = []pipeline.Warning{}
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func (s *Server) getResults(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadView(w, r)
	if !ok {
		return
	}
	if !v.Status.IsTerminal() {
		WriteError(w, http.StatusConflict, "JOB_NOT_FINISHED", fmt.Sprintf("job is not finished yet (status %s)", v.Status))
		return
	}
	warnings := v.Warnings
	if warnings == nil {
		warnings = []pipeline.Warning{}
	}
	if v.Status == pipeline.StatusFailed || v.Status == pipeline.StatusDeadLettered {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": v.Status, "result": nil, "warnings": warnings, "error": v.Error,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": v.Status, "result": v.Result, "warnings": warnings})
}
