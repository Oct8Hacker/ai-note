package api

import (
	"ainote/internal/config"
	"ainote/internal/notion"
	"ainote/internal/pipeline"
	"bytes"
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeNotionGateway struct {
	err   error
	args  []string
	pages []notion.SearchResult
}

func (f *fakeNotionGateway) ExchangeCode(_ context.Context, id, secret, code, uri string) (notion.OAuthToken, error) {
	f.args = []string{id, secret, code, uri}
	return notion.OAuthToken{AccessToken: "returned-token"}, f.err
}
func (f *fakeNotionGateway) Search(_ context.Context, token, q, filter string) ([]notion.SearchResult, error) {
	f.args = []string{token, q, filter}
	return f.pages, f.err
}

var oauthCfg = config.Gateway{NotionClientID: "client", NotionClientSecret: "secret", NotionRedirectURI: "https://ext.chromiumapp.org/notion"}

const goodExchange = `{"code":"valid","redirect_uri":"https://ext.chromiumapp.org/notion"}`

func notionRequest(cfg config.Gateway, n NotionGateway, method, path, body, auth string) *httptest.ResponseRecorder {
	h := NewServer(cfg, ServerDeps{Notion: n}).Handler()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestConfigDisabled(t *testing.T) {
	f := &fakeNotionGateway{}
	w := notionRequest(config.Gateway{}, f, "GET", "/auth/notion/config", "", "")
	require.JSONEq(t, `{"oauth_enabled":false}`, w.Body.String())
	require.Equal(t, 404, notionRequest(config.Gateway{}, f, "POST", "/auth/notion/exchange", goodExchange, "").Code)
	require.Equal(t, 404, notionRequest(oauthCfg, nil, "GET", "/auth/notion/config", "", "").Code)
}
func TestExchangeOK(t *testing.T) {
	f := &fakeNotionGateway{}
	require.JSONEq(t, `{"oauth_enabled":true,"client_id":"client"}`, notionRequest(oauthCfg, f, "GET", "/auth/notion/config", "", "").Body.String())
	w := notionRequest(oauthCfg, f, "POST", "/auth/notion/exchange", goodExchange, "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "returned-token")
	require.Equal(t, []string{"client", "secret", "valid", oauthCfg.NotionRedirectURI}, f.args)
}
func TestExchangeRejectsForeignRedirect(t *testing.T) {
	f := &fakeNotionGateway{}
	w := notionRequest(oauthCfg, f, "POST", "/auth/notion/exchange", strings.Replace(goodExchange, "ext.chromiumapp.org", "other.invalid", 1), "")
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "OAUTH_CODE_INVALID")
	require.Empty(t, f.args)
}
func TestExchangeBadCode(t *testing.T) {
	f := &fakeNotionGateway{err: &pipeline.RetryableError{Code: "NOTION_BAD_REQUEST"}}
	w := notionRequest(oauthCfg, f, "POST", "/auth/notion/exchange", goodExchange, "")
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "OAUTH_CODE_INVALID")
}
func TestPagesUnauthorized(t *testing.T) {
	f := &fakeNotionGateway{err: &pipeline.FatalError{Code: "NOTION_UNAUTHORIZED"}}
	w := notionRequest(oauthCfg, f, "GET", "/notion/pages", "", "Bearer invalid")
	require.Equal(t, 401, w.Code)
	require.Contains(t, w.Body.String(), "NOTION_UNAUTHORIZED")
}
func TestPagesMissingBearer(t *testing.T) {
	f := &fakeNotionGateway{}
	for _, h := range []string{"", "Basic abc", "Bearer ", "Bearer a b"} {
		require.Equal(t, 401, notionRequest(oauthCfg, f, "GET", "/notion/pages", "", h).Code)
	}
	require.Empty(t, f.args)
}
func TestPagesWithIntegrationToken(t *testing.T) {
	f := &fakeNotionGateway{pages: []notion.SearchResult{{ID: "p", Title: "Notes", Type: "page", Icon: "📘"}}}
	w := notionRequest(config.Gateway{}, f, "GET", "/notion/pages", "", "Bearer pasted")
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"pages":[{"id":"p","title":"Notes","type":"page","icon":"📘"}]}`, w.Body.String())
	require.Equal(t, []string{"pasted", "", ""}, f.args)
}
func TestNoTokenInLogs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	f := &fakeNotionGateway{err: errors.New("test-credential")}
	w := notionRequest(oauthCfg, f, "GET", "/notion/pages", "", "Bearer test-credential")
	require.Equal(t, 502, w.Code)
	require.NotContains(t, w.Body.String()+buf.String(), "test-credential")
}
