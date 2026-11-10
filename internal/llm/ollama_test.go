package llm

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOllamaSendsSchemaAndImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Format   map[string]any
			Stream   bool
			Messages []struct {
				Role   string
				Images []string
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path != "/api/chat" || b.Stream || b.Format["type"] != "object" || len(b.Messages) != 2 || len(b.Messages[1].Images) != 1 || b.Messages[1].Images[0] != "AQI=" {
			t.Errorf("unexpected request %+v", b)
		}
		_, _ = io.WriteString(w, `{"message":{"content":"{}"},"prompt_eval_count":4,"eval_count":2}`)
	}))
	defer srv.Close()
	out, err := NewOllama(srv.URL, "model", time.Second).GenerateJSON(context.Background(), JSONRequest{System: "system", Images: []Image{{Label: "frame", JPEG: []byte{1, 2}}}, Schema: map[string]any{"type": "object"}})
	require.NoError(t, err)
	require.Equal(t, JSONResponse{Text: "{}", InputTokens: 4, OutputTokens: 2}, out)
}
