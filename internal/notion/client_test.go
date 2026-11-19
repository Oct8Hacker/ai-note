package notion

import (
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/pipelinetest"
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type injectedNotionFault struct{ pipelinetest.NoFaults }

func (injectedNotionFault) Check(_ context.Context, point pipeline.FaultPoint) error {
	if point == pipeline.FaultNotion {
		return &pipeline.RateLimitedError{Provider: "notion"}
	}
	return nil
}

func TestLimiterBucketPerToken(t *testing.T) {
	c, m := server(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"id":"p"}`) })
	for _, token := range []string{"first", "second"} {
		_, err := c.RetrievePage(context.Background(), token, "p")
		require.NoError(t, err)
	}
	c.faults = injectedNotionFault{}
	_, err := c.RetrievePage(context.Background(), "third", "p")
	require.Equal(t, "RATE_LIMITED", pipeline.ErrorCode(err))
	require.Equal(t, []string{pipeline.NotionBucket("first"), pipeline.NotionBucket("second")}, m.buckets)
}

func TestDatabasePublishOperations(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /databases/db":
			_, _ = io.WriteString(w, `{"data_sources":[{"id":"ds"}]}`)
		case "GET /data_sources/ds":
			// A data source's properties are a schema: "title" is a config object, not rich text.
			_, _ = io.WriteString(w, `{"object":"data_source","id":"ds","title":[{"plain_text":"Lectures"}],"properties":{"Lecture name":{"id":"title","name":"Lecture name","type":"title","title":{}},"Tags":{"id":"t1","name":"Tags","type":"multi_select","multi_select":{"options":[]}}}}`)
		case "POST /pages":
			var b struct {
				Parent     map[string]string
				Properties map[string]any
				Icon       struct{ Emoji string }
			}
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Parent["data_source_id"] != "ds" || b.Properties["Lecture name"] == nil || b.Icon.Emoji != "📺" {
				t.Error("invalid create page payload")
			}
			_, _ = io.WriteString(w, `{"id":"p","url":"https://example.org/p","created_time":"2026-01-01T00:00:00Z"}`)
		case "POST /data_sources/ds/query":
			var q struct {
				Filter map[string]any
				Cursor string `json:"start_cursor"`
			}
			if err := json.NewDecoder(r.Body).Decode(&q); err != nil || q.Filter["property"] != "Lecture name" {
				t.Error("missing filter")
			}
			switch q.Cursor {
			case "":
				_, _ = io.WriteString(w, `{"results":[],"has_more":true,"next_cursor":"next"}`)
			case "next":
				_, _ = io.WriteString(w, `{"results":[{"object":"page","id":"p"}]}`)
			default:
				t.Error("invalid cursor")
			}
		case "DELETE /blocks/block":
			_, _ = io.WriteString(w, "{}")
		default:
			t.Error(r.Method, r.URL.Path)
		}
	})
	ctx := context.Background()
	ids, err := c.RetrieveDatabase(ctx, "t", "db")
	require.NoError(t, err)
	require.Equal(t, []string{"ds"}, ids)
	title, err := c.TitlePropertyName(ctx, "t", "ds")
	require.NoError(t, err)
	require.Equal(t, "Lecture name", title)
	p, err := c.CreatePage(ctx, "t", map[string]any{"data_source_id": "ds"}, map[string]any{title: map[string]any{"title": []any{}}}, "📺")
	require.NoError(t, err)
	require.Equal(t, "p", p.ID)
	require.False(t, p.CreatedTime.IsZero())
	pages, err := c.QueryDataSource(ctx, "t", "ds", map[string]any{"property": title})
	require.NoError(t, err)
	require.Len(t, pages, 1)
	require.Equal(t, "p", pages[0].ID)
	require.NoError(t, c.ArchiveBlock(ctx, "t", "block"))
}

type meter struct{ buckets []string }

func (m *meter) Acquire(_ context.Context, b string, n int) error {
	for range n {
		m.buckets = append(m.buckets, b)
	}
	return nil
}
func server(t *testing.T, h http.HandlerFunc) (*Client, *meter) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	m := &meter{}
	return New(srv.URL, "2025-09-03", m, pipelinetest.NoFaults{}, srv.Client()), m
}
func TestAppendBatches(t *testing.T) {
	var sizes []int
	c, m := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/blocks/page/children" || r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Notion-Version") != "2025-09-03" {
			t.Error("incorrect append request")
		}
		var data struct{ Children []Block }
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			t.Error(err)
		}
		sizes = append(sizes, len(data.Children))
		_, _ = io.WriteString(w, "{}")
	})
	require.NoError(t, c.AppendChildren(context.Background(), "secret", "page", make([]Block, 250)))
	require.Equal(t, []int{100, 100, 50}, sizes)
	b := pipeline.NotionBucket("secret")
	require.Equal(t, []string{b, b, b}, m.buckets)
}
func TestPaginationChildren(t *testing.T) {
	var cursors []string
	c, m := server(t, func(w http.ResponseWriter, r *http.Request) {
		cur := r.URL.Query().Get("start_cursor")
		cursors = append(cursors, cur)
		if cur == "" {
			_, _ = io.WriteString(w, `{"results":[{"id":"a"}],"has_more":true,"next_cursor":"next"}`)
		} else {
			_, _ = io.WriteString(w, `{"results":[{"id":"b"}]}`)
		}
	})
	rows, err := c.ListChildren(context.Background(), "t", "p")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, []string{"", "next"}, cursors)
	require.Len(t, m.buckets, 2)
}
func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   string
	}{{400, "NOTION_BAD_REQUEST"}, {401, "NOTION_UNAUTHORIZED"}, {403, "NOTION_UNAUTHORIZED"}, {404, ""}, {429, "RATE_LIMITED"}, {502, "NOTION_UNAVAILABLE"}} {
		c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, `{"message":"bad property for secret-token"}`)
		})
		_, err := c.RetrievePage(context.Background(), "secret-token", "page")
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret-token")
		if tc.status == 404 {
			require.ErrorIs(t, err, ErrObjectNotFound)
			continue
		}
		require.Equal(t, tc.code, pipeline.ErrorCode(err))
		if tc.status == 400 {
			require.Contains(t, err.Error(), "bad property")
		}
		if tc.status == 429 {
			var rate *pipeline.RateLimitedError
			require.ErrorAs(t, err, &rate)
			require.Equal(t, 2*time.Second, rate.RetryAfter)
		}
	}
}
func TestUploadImageTwoStep(t *testing.T) {
	calls := 0
	c, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			if r.URL.Path != "/file_uploads" {
				t.Error(r.URL.Path)
			}
			_, _ = io.WriteString(w, `{"id":"u"}`)
			return
		}
		if r.URL.Path != "/file_uploads/u/send" {
			t.Error(r.URL.Path)
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = f.Close() }()
		b, err := io.ReadAll(f)
		if err != nil || string(b) != "jpeg" || h.Filename != "slide.jpg" || h.Header.Get("Content-Type") != "image/jpeg" {
			t.Error("incorrect multipart file")
		}
		_, _ = io.WriteString(w, `{"id":"u","status":"uploaded"}`)
	})
	id, err := c.UploadImage(context.Background(), "token", "slide.jpg", []byte("jpeg"))
	require.NoError(t, err)
	require.Equal(t, "u", id)
	require.Equal(t, 2, calls)
}
func TestExchangeCodeBasicAuth(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		var b map[string]string
		err := json.NewDecoder(r.Body).Decode(&b)
		if !ok || id != "id" || secret != "key" || err != nil || b["code"] != "code" || b["grant_type"] != "authorization_code" || b["redirect_uri"] != "redirect" || r.URL.Path != "/oauth/token" {
			t.Error("incorrect OAuth exchange")
		}
		_, _ = io.WriteString(w, `{"access_token":"access","workspace_name":"Demo"}`)
	})
	result, err := c.ExchangeCode(context.Background(), "id", "key", "code", "redirect")
	require.NoError(t, err)
	require.Equal(t, "access", result.AccessToken)
}
func TestSearchDestinations(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"object":"page","id":"p","properties":{"Name":{"type":"title","title":[{"plain_text":"A"},{"plain_text":" B"}]}}},{"object":"data_source","id":"source","parent":{"type":"database_id","database_id":"database"},"title":[{"plain_text":"Notes"}],"icon":{"type":"emoji","emoji":"📓"},"properties":{"Name":{"id":"title","name":"Name","type":"title","title":{}}}}]}`)
	})
	got, err := c.Search(context.Background(), "t", "", "")
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "A B", got[0].Title)
	require.Equal(t, "database", got[1].ID)
	require.Equal(t, "Notes", got[1].Title)
	require.Equal(t, "database", got[1].Type)
	require.Equal(t, "📓", got[1].Icon)
}
