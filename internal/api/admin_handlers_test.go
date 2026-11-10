package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
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

const adminTok = "admin-secret"

var adminAuth = map[string]string{"Authorization": "Bearer " + adminTok}

type fakeAdmin struct {
	list      []jobs.DLQEntry
	listErr   error
	gotLimit  int
	resetErr  error
	gotID     uuid.UUID
	gotBytes  []byte
	resetJob  pipeline.Job
	resetCall int
}

func (f *fakeAdmin) ListDeadLettered(_ context.Context, limit int) ([]jobs.DLQEntry, error) {
	f.gotLimit = limit
	return f.list, f.listErr
}

func (f *fakeAdmin) ResetForReplay(_ context.Context, id uuid.UUID, payload []byte) (pipeline.Job, error) {
	f.resetCall++
	f.gotID, f.gotBytes = id, payload
	if f.resetErr != nil {
		return pipeline.Job{}, f.resetErr
	}
	j := f.resetJob
	j.ID = id
	return j, nil
}

type fakeFaults struct {
	m   map[string]string
	set map[string]string
}

func (f *fakeFaults) Get(context.Context) (map[string]string, error) { return f.m, nil }
func (f *fakeFaults) Set(_ context.Context, m map[string]string) error {
	f.set = m
	return nil
}

func newAdminServer(t *testing.T, adm *fakeAdmin, faults FaultAdmin) (*Server, *fakeKicker, *crypto.Sealer) {
	t.Helper()
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	sealer, err := crypto.NewSealer(base64.StdEncoding.EncodeToString(b))
	require.NoError(t, err)
	k := &fakeKicker{}
	cfg := config.Gateway{AdminToken: adminTok}
	return NewServer(cfg, ServerDeps{Jobs: &fakeStore{}, Sealer: sealer, Outbox: k, Admin: adm, Faults: faults}), k, sealer
}

func TestAdminRequiresToken(t *testing.T) {
	s, _, _ := newAdminServer(t, &fakeAdmin{}, &fakeFaults{})
	h := s.Handler()
	id := uuid.NewString()
	routes := [][2]string{
		{"GET", "/admin/dlq"}, {"POST", "/admin/dlq/" + id + "/replay"},
		{"GET", "/admin/faults"}, {"PUT", "/admin/faults"},
	}
	headers := []map[string]string{
		nil,
		{"Authorization": "Bearer nope"},
		{"Authorization": "Bearer " + adminTok + "x"},
		{"Authorization": "Basic " + adminTok},
		{"Authorization": adminTok},
		{"Authorization": "Bearer "},
	}
	for _, rt := range routes {
		for _, hdr := range headers {
			rec := do(h, rt[0], rt[1], `{"notion_token":"t"}`, hdr)
			requireErr(t, rec, 401, "UNAUTHORIZED")
		}
	}
}

func TestAdminEmptyConfiguredTokenRejectsEverything(t *testing.T) {
	adm := &fakeAdmin{}
	s := NewServer(config.Gateway{}, ServerDeps{Jobs: &fakeStore{}, Admin: adm})
	rec := do(s.Handler(), "GET", "/admin/dlq", "", map[string]string{"Authorization": "Bearer "})
	requireErr(t, rec, 401, "UNAUTHORIZED")
}

func TestFaultRoutesAbsentWithoutFaultAdmin(t *testing.T) {
	s, _, _ := newAdminServer(t, &fakeAdmin{}, nil)
	h := s.Handler()
	requireErr(t, do(h, "GET", "/admin/faults", "", adminAuth), 404, "NOT_FOUND")
	requireErr(t, do(h, "PUT", "/admin/faults", "{}", adminAuth), 404, "NOT_FOUND")
}

func TestFaultRoutes(t *testing.T) {
	ff := &fakeFaults{m: map[string]string{"llm": "0.5"}}
	s, _, _ := newAdminServer(t, &fakeAdmin{}, ff)
	h := s.Handler()
	rec := do(h, "GET", "/admin/faults", "", adminAuth)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	var got map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, ff.m, got)

	rec = do(h, "PUT", "/admin/faults", `{"notion":"1"}`, adminAuth)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, map[string]string{"notion": "1"}, ff.set)

	requireErr(t, do(h, "PUT", "/admin/faults", `nope`, adminAuth), 400, "INVALID_JSON")
}

func TestListDLQ(t *testing.T) {
	id := uuid.New()
	from, to := "SUMMARIZING", "DEAD_LETTERED"
	adm := &fakeAdmin{list: []jobs.DLQEntry{{
		JobView: jobs.JobView{
			JobID: id, Status: pipeline.StatusDeadLettered, VideoID: "dQw4w9WgXcQ", Attempt: 3,
			Error:     &jobs.ErrorBody{Code: "RETRIES_EXHAUSTED", Message: "boom"},
			CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
		},
		Events: []jobs.Event{{At: time.Unix(2, 0).UTC(), Type: "transition", FromStatus: &from, ToStatus: &to,
			Detail: json.RawMessage(`{"code":"RETRIES_EXHAUSTED"}`)}},
	}}}
	s, _, _ := newAdminServer(t, adm, nil)
	rec := do(s.Handler(), "GET", "/admin/dlq?limit=5", "", adminAuth)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, 5, adm.gotLimit)
	var out struct {
		Jobs []map[string]any `json:"jobs"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Jobs, 1)
	j := out.Jobs[0]
	require.Equal(t, id.String(), j["job_id"])
	require.Equal(t, "DEAD_LETTERED", j["status"])
	require.Equal(t, "RETRIES_EXHAUSTED", j["error"].(map[string]any)["code"])
	evs := j["events"].([]any)
	require.Len(t, evs, 1)
	ev := evs[0].(map[string]any)
	require.Equal(t, "transition", ev["type"])
	require.Equal(t, "SUMMARIZING", ev["from_status"])
	require.Equal(t, "RETRIES_EXHAUSTED", ev["detail"].(map[string]any)["code"])
}

func TestListDLQEmptyAndLimits(t *testing.T) {
	adm := &fakeAdmin{}
	s, _, _ := newAdminServer(t, adm, nil)
	h := s.Handler()
	rec := do(h, "GET", "/admin/dlq", "", adminAuth)
	require.Equal(t, 200, rec.Code)
	require.JSONEq(t, `{"jobs":[]}`, rec.Body.String())
	require.Equal(t, 50, adm.gotLimit)
	requireErr(t, do(h, "GET", "/admin/dlq?limit=0", "", adminAuth), 400, "INVALID_LIMIT")
	requireErr(t, do(h, "GET", "/admin/dlq?limit=x", "", adminAuth), 400, "INVALID_LIMIT")
	do(h, "GET", "/admin/dlq?limit=100000", "", adminAuth)
	require.Equal(t, 200, adm.gotLimit)

	adm.listErr = errors.New("db down")
	requireErr(t, do(h, "GET", "/admin/dlq", "", adminAuth), 500, "INTERNAL")
}

func TestReplayRequeues(t *testing.T) {
	adm := &fakeAdmin{resetJob: pipeline.Job{Status: pipeline.StatusSummarizing, VideoID: "dQw4w9WgXcQ"}}
	s, k, sealer := newAdminServer(t, adm, nil)
	h := s.Handler()
	id := uuid.New()

	rec := do(h, "POST", "/admin/dlq/"+id.String()+"/replay", `{"notion_token":"fresh_tok"}`, adminAuth)
	require.Equal(t, 202, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "fresh_tok")
	var out map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, id.String(), out["job_id"])
	require.Equal(t, "SUMMARIZING", out["status"])
	require.Equal(t, 1, k.n)
	require.Equal(t, id, adm.gotID)

	var m queue.Message
	require.NoError(t, json.Unmarshal(adm.gotBytes, &m))
	require.Equal(t, 1, m.V)
	require.Equal(t, "replay", m.Reason)
	require.Equal(t, id, m.JobID)
	require.NotContains(t, string(adm.gotBytes), "fresh_tok")
	plain, err := sealer.Open(m.NotionTokenEnc)
	require.NoError(t, err)
	require.Equal(t, "fresh_tok", plain)

	// missing token
	adm.resetCall = 0
	requireErr(t, do(h, "POST", "/admin/dlq/"+id.String()+"/replay", `{}`, adminAuth), 400, "MISSING_NOTION_TOKEN")
	requireErr(t, do(h, "POST", "/admin/dlq/"+id.String()+"/replay", `{"notion_token":""}`, adminAuth), 400, "MISSING_NOTION_TOKEN")
	requireErr(t, do(h, "POST", "/admin/dlq/"+id.String()+"/replay", `not json`, adminAuth), 400, "INVALID_JSON")
	require.Equal(t, 0, adm.resetCall)
	require.Equal(t, 1, k.n)
}

func TestReplayErrors(t *testing.T) {
	adm := &fakeAdmin{}
	s, k, _ := newAdminServer(t, adm, nil)
	h := s.Handler()
	body := `{"notion_token":"t"}`

	requireErr(t, do(h, "POST", "/admin/dlq/not-a-uuid/replay", body, adminAuth), 404, "JOB_NOT_FOUND")

	adm.resetErr = jobs.ErrNotFound
	requireErr(t, do(h, "POST", "/admin/dlq/"+uuid.NewString()+"/replay", body, adminAuth), 404, "JOB_NOT_FOUND")

	adm.resetErr = jobs.ErrNotDeadLettered
	requireErr(t, do(h, "POST", "/admin/dlq/"+uuid.NewString()+"/replay", body, adminAuth), 409, "JOB_NOT_DEAD_LETTERED")

	adm.resetErr = errors.New("db down")
	requireErr(t, do(h, "POST", "/admin/dlq/"+uuid.NewString()+"/replay", body, adminAuth), 500, "INTERNAL")
	require.Equal(t, 0, k.n)
}
