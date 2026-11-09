package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"ainote/internal/config"
	"ainote/internal/crypto"
	"ainote/internal/jobs"
	"ainote/internal/pipeline"
	"ainote/internal/queue"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type fakeStore struct {
	created  bool
	err      error
	gotNew   jobs.NewJob
	gotBytes []byte
	calls    int
	view     jobs.JobView
	viewErr  error
	existing uuid.UUID
}

func (f *fakeStore) CreateJob(_ context.Context, in jobs.NewJob, payload []byte) (pipeline.Job, bool, error) {
	f.calls++
	f.gotNew, f.gotBytes = in, payload
	if f.err != nil {
		return pipeline.Job{}, false, f.err
	}
	id := in.ID
	if !f.created && f.existing != uuid.Nil {
		id = f.existing
	}
	return pipeline.Job{ID: id, VideoID: in.VideoID, Status: pipeline.StatusQueued}, f.created, nil
}

func (f *fakeStore) GetJobView(_ context.Context, id uuid.UUID) (jobs.JobView, error) {
	if f.viewErr != nil {
		return jobs.JobView{}, f.viewErr
	}
	v := f.view
	v.JobID = id
	return v, nil
}

type fakeKicker struct{ n int }

func (k *fakeKicker) Kick() { k.n++ }

const ext = "chrome-extension://abcdefghijklmnop"

func newTestServer(t *testing.T, store *fakeStore) (*Server, *fakeKicker, *crypto.Sealer) {
	t.Helper()
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	sealer, err := crypto.NewSealer(base64.StdEncoding.EncodeToString(b))
	require.NoError(t, err)
	k := &fakeKicker{}
	cfg := config.Gateway{CORSAllowedOrigins: []string{ext}}
	return NewServer(cfg, ServerDeps{Jobs: store, Sealer: sealer, Outbox: k}), k, sealer
}

func do(h http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var jsonCT = map[string]string{"Content-Type": "application/json"}

const goodBody = `{"youtube_url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ","notion_token":"secret_tok"}`

func requireErr(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	require.Equal(t, status, rec.Code, rec.Body.String())
	var e struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &e))
	require.Equal(t, code, e.Error.Code)
	require.NotEmpty(t, e.Error.Message)
}

func TestPostJobAccepted(t *testing.T) {
	st := &fakeStore{created: true}
	s, k, sealer := newTestServer(t, st)
	rec := do(s.Handler(), "POST", "/jobs", goodBody, jsonCT)
	require.Equal(t, 202, rec.Code, rec.Body.String())
	var out map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "QUEUED", out["status"])
	require.Equal(t, "dQw4w9WgXcQ", out["video_id"])
	require.Equal(t, "/jobs/"+out["job_id"], rec.Header().Get("Location"))
	require.Equal(t, 1, k.n)
	require.Equal(t, out["job_id"], st.gotNew.ID.String())

	var m queue.Message
	require.NoError(t, json.Unmarshal(st.gotBytes, &m))
	require.Equal(t, 1, m.V)
	require.Equal(t, "new", m.Reason)
	require.Equal(t, st.gotNew.ID, m.JobID)
	require.NotEqual(t, "secret_tok", m.NotionTokenEnc)
	require.NotContains(t, string(st.gotBytes), "secret_tok")
	plain, err := sealer.Open(m.NotionTokenEnc)
	require.NoError(t, err)
	require.Equal(t, "secret_tok", plain)
}

func TestPostJobNotionParentNormalized(t *testing.T) {
	st := &fakeStore{created: true}
	s, _, _ := newTestServer(t, st)
	body := `{"youtube_url":"https://youtu.be/dQw4w9WgXcQ","notion_token":"t","notion_parent_id":"0123456789abcdef0123456789abcdef"}`
	rec := do(s.Handler(), "POST", "/jobs", body, jsonCT)
	require.Equal(t, 202, rec.Code)
	require.Equal(t, "01234567-89ab-cdef-0123-456789abcdef", st.gotNew.NotionParentID)
}

func TestPostJobErrors(t *testing.T) {
	big := `{"youtube_url":"https://youtu.be/dQw4w9WgXcQ","notion_token":"` + strings.Repeat("a", 20*1024) + `"}`
	cases := []struct {
		name, body string
		hdr        map[string]string
		status     int
		code       string
	}{
		{"invalid json", `{nope`, jsonCT, 400, "INVALID_JSON"},
		{"bad url", `{"youtube_url":"https://vimeo.com/1","notion_token":"t"}`, jsonCT, 400, "INVALID_URL"},
		{"missing url", `{"notion_token":"t"}`, jsonCT, 400, "INVALID_URL"},
		{"missing token", `{"youtube_url":"https://youtu.be/dQw4w9WgXcQ"}`, jsonCT, 400, "MISSING_NOTION_TOKEN"},
		{"empty token", `{"youtube_url":"https://youtu.be/dQw4w9WgXcQ","notion_token":""}`, jsonCT, 400, "MISSING_NOTION_TOKEN"},
		{"bad parent", `{"youtube_url":"https://youtu.be/dQw4w9WgXcQ","notion_token":"t","notion_parent_id":"xyz"}`, jsonCT, 400, "INVALID_NOTION_PARENT"},
		{"too large", big, jsonCT, 413, "PAYLOAD_TOO_LARGE"},
		{"text/plain", goodBody, map[string]string{"Content-Type": "text/plain"}, 415, "UNSUPPORTED_MEDIA_TYPE"},
		{"no content type", goodBody, nil, 415, "UNSUPPORTED_MEDIA_TYPE"},
		{"long idem key", goodBody, map[string]string{"Content-Type": "application/json", "Idempotency-Key": strings.Repeat("k", 201)}, 400, "INVALID_IDEMPOTENCY_KEY"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &fakeStore{created: true}
			s, k, _ := newTestServer(t, st)
			rec := do(s.Handler(), "POST", "/jobs", c.body, c.hdr)
			requireErr(t, rec, c.status, c.code)
			require.Equal(t, 0, st.calls)
			require.Equal(t, 0, k.n)
		})
	}
}

func TestPostJobContentTypeWithCharset(t *testing.T) {
	s, _, _ := newTestServer(t, &fakeStore{created: true})
	rec := do(s.Handler(), "POST", "/jobs", goodBody, map[string]string{"Content-Type": "application/json; charset=utf-8"})
	require.Equal(t, 202, rec.Code)
}

func TestPostJobStoreError(t *testing.T) {
	s, k, _ := newTestServer(t, &fakeStore{err: errors.New("boom secret detail")})
	rec := do(s.Handler(), "POST", "/jobs", goodBody, jsonCT)
	requireErr(t, rec, 500, "INTERNAL")
	require.NotContains(t, rec.Body.String(), "boom")
	require.Equal(t, 0, k.n)
}

func TestPostJobIdempotencyKey(t *testing.T) {
	existing := uuid.New()
	st := &fakeStore{created: false, existing: existing}
	s, k, _ := newTestServer(t, st)
	hdr := map[string]string{"Content-Type": "application/json", "Idempotency-Key": "key-1"}
	rec := do(s.Handler(), "POST", "/jobs", goodBody, hdr)
	require.Equal(t, 202, rec.Code)
	require.Equal(t, "key-1", st.gotNew.IdempotencyKey)
	var out map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, existing.String(), out["job_id"])
	require.Equal(t, "/jobs/"+existing.String(), rec.Header().Get("Location"))
	require.Equal(t, 0, k.n)
}

func TestPostJobNoExternalCalls(t *testing.T) {
	s, _, _ := newTestServer(t, &fakeStore{created: true})
	h := s.Handler()
	var ds []time.Duration
	for i := 0; i < 50; i++ {
		start := time.Now()
		rec := do(h, "POST", "/jobs", goodBody, jsonCT)
		ds = append(ds, time.Since(start))
		require.Equal(t, 202, rec.Code)
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	require.Less(t, ds[47], 200*time.Millisecond)
}

func TestGetJob(t *testing.T) {
	now := time.Now().UTC()
	st := &fakeStore{view: jobs.JobView{Status: pipeline.StatusSummarizing, Progress: 40, VideoID: "dQw4w9WgXcQ",
		Warnings: []pipeline.Warning{}, Attempt: 1, CreatedAt: now, UpdatedAt: now}}
	s, _, _ := newTestServer(t, st)
	rec := do(s.Handler(), "GET", "/jobs/"+uuid.NewString(), "", nil)
	require.Equal(t, 200, rec.Code)
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
	for _, f := range []string{"job_id", "status", "progress", "video_id", "video_title", "duration_sec", "warnings", "error", "result", "waiting", "attempt", "created_at", "updated_at"} {
		require.Contains(t, m, f)
	}
	require.JSONEq(t, `[]`, string(m["warnings"]))
}

func TestGetJobUnknown(t *testing.T) {
	st := &fakeStore{viewErr: jobs.ErrNotFound}
	s, _, _ := newTestServer(t, st)
	requireErr(t, do(s.Handler(), "GET", "/jobs/"+uuid.NewString(), "", nil), 404, "JOB_NOT_FOUND")
	requireErr(t, do(s.Handler(), "GET", "/jobs/not-a-uuid", "", nil), 404, "JOB_NOT_FOUND")
	requireErr(t, do(s.Handler(), "GET", "/jobs/not-a-uuid/results", "", nil), 404, "JOB_NOT_FOUND")
}

func TestGetJobInternalError(t *testing.T) {
	s, _, _ := newTestServer(t, &fakeStore{viewErr: errors.New("db down")})
	requireErr(t, do(s.Handler(), "GET", "/jobs/"+uuid.NewString(), "", nil), 500, "INTERNAL")
}

func TestResultsBeforeTerminal(t *testing.T) {
	s, _, _ := newTestServer(t, &fakeStore{view: jobs.JobView{Status: pipeline.StatusSummarizing}})
	rec := do(s.Handler(), "GET", "/jobs/"+uuid.NewString()+"/results", "", nil)
	requireErr(t, rec, 409, "JOB_NOT_FINISHED")
	require.Contains(t, rec.Body.String(), "SUMMARIZING")
}

func TestResultsCompleted(t *testing.T) {
	st := &fakeStore{view: jobs.JobView{Status: pipeline.StatusCompletedWithWarnings,
		Result:   &jobs.Result{NotionPageURL: "https://www.notion.so/Notes-1a2b3c", FramesRequested: 6, FramesPublished: 5},
		Warnings: []pipeline.Warning{{Code: "FRAME_EXTRACTION_FAILED"}}}}
	s, _, _ := newTestServer(t, st)
	rec := do(s.Handler(), "GET", "/jobs/"+uuid.NewString()+"/results", "", nil)
	require.Equal(t, 200, rec.Code)
	var out struct {
		Status   string
		Result   *jobs.Result
		Warnings []map[string]any
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "COMPLETED_WITH_WARNINGS", out.Status)
	require.True(t, strings.HasPrefix(out.Result.NotionPageURL, "https://"))
	require.Equal(t, 5, out.Result.FramesPublished)
	require.Len(t, out.Warnings, 1)
	require.NotContains(t, rec.Body.String(), `"error"`)
}

func TestResultsFailed(t *testing.T) {
	for _, status := range []pipeline.Status{pipeline.StatusFailed, pipeline.StatusDeadLettered} {
		st := &fakeStore{view: jobs.JobView{Status: status, Error: &jobs.ErrorBody{Code: "NO_TRANSCRIPT", Message: "no captions"}}}
		s, _, _ := newTestServer(t, st)
		rec := do(s.Handler(), "GET", "/jobs/"+uuid.NewString()+"/results", "", nil)
		require.Equal(t, 200, rec.Code)
		var m map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &m))
		require.JSONEq(t, `null`, string(m["result"]))
		require.JSONEq(t, `[]`, string(m["warnings"]))
		require.JSONEq(t, `{"code":"NO_TRANSCRIPT","message":"no captions"}`, string(m["error"]))
	}
}

func TestCORS(t *testing.T) {
	s, _, _ := newTestServer(t, &fakeStore{created: true})
	h := s.Handler()

	rec := do(h, "POST", "/jobs", goodBody, map[string]string{"Content-Type": "application/json", "Origin": ext})
	require.Equal(t, ext, rec.Header().Get("Access-Control-Allow-Origin"))

	rec = do(h, "OPTIONS", "/jobs", "", map[string]string{"Origin": ext, "Access-Control-Request-Method": "POST"})
	require.Equal(t, 204, rec.Code)
	require.Equal(t, ext, rec.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
	require.Contains(t, rec.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key")

	rec = do(h, "POST", "/jobs", goodBody, map[string]string{"Content-Type": "application/json", "Origin": "https://evil.com"})
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	rec = do(h, "OPTIONS", "/jobs", "", map[string]string{"Origin": "https://evil.com"})
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestUnknownRouteAndMethod(t *testing.T) {
	s, _, _ := newTestServer(t, &fakeStore{})
	requireErr(t, do(s.Handler(), "GET", "/nope", "", nil), 404, "NOT_FOUND")
	requireErr(t, do(s.Handler(), "DELETE", "/jobs", "", nil), 405, "METHOD_NOT_ALLOWED")
}

func TestCORSVary(t *testing.T) {
	s, _, _ := newTestServer(t, &fakeStore{created: true})
	h := s.Handler()

	rec := do(h, "GET", "/jobs/"+uuid.NewString(), "", map[string]string{"Origin": ext})
	require.Equal(t, ext, rec.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, rec.Header().Values("Vary"), "Origin")

	rec = do(h, "GET", "/jobs/"+uuid.NewString(), "", map[string]string{"Origin": "https://evil.com"})
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(t, rec.Header().Values("Vary"), "Origin")

	rec = do(h, "OPTIONS", "/jobs", "", map[string]string{"Origin": ext})
	vary := strings.Join(rec.Header().Values("Vary"), ",")
	require.Contains(t, vary, "Origin")
	require.Contains(t, vary, "Access-Control-Request-Method")
	require.Contains(t, vary, "Access-Control-Request-Headers")
}

func TestPostJobStoresCanonicalURL(t *testing.T) {
	st := &fakeStore{created: true}
	s, _, _ := newTestServer(t, st)
	body := `{"youtube_url":"  https://youtu.be/dQw4w9WgXcQ?si=abc ","notion_token":"t"}`
	require.Equal(t, 202, do(s.Handler(), "POST", "/jobs", body, jsonCT).Code)
	require.Equal(t, "https://www.youtube.com/watch?v=dQw4w9WgXcQ", st.gotNew.YouTubeURL)
}
