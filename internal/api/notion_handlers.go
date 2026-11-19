package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode"

	"ainote/internal/notion"
	"ainote/internal/pipeline"
)

// NotionGateway provides the Notion operations used by the gateway.
type NotionGateway interface {
	ExchangeCode(ctx context.Context, clientID, clientSecret, code, redirectURI string) (notion.OAuthToken, error)
	Search(ctx context.Context, token, query, objectFilter string) ([]notion.SearchResult, error)
}

func (s *Server) registerNotion(mux *http.ServeMux) {
	enabled := s.cfg.NotionClientID != "" && s.cfg.NotionClientSecret != "" && s.cfg.NotionRedirectURI != ""
	mux.HandleFunc("GET /auth/notion/config", func(w http.ResponseWriter, _ *http.Request) {
		cfg := map[string]any{"oauth_enabled": enabled}
		if enabled {
			cfg["client_id"] = s.cfg.NotionClientID
		}
		writeJSON(w, http.StatusOK, cfg)
	})
	if enabled {
		mux.HandleFunc("POST /auth/notion/exchange", s.exchangeNotionCode)
	}
	mux.HandleFunc("GET /notion/pages", s.listNotionPages)
}

func (s *Server) exchangeNotionCode(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		Code        string `json:"code"`
		RedirectURI string `json:"redirect_uri"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if dec.Decode(&input) != nil || dec.Decode(new(any)) != io.EOF || strings.TrimSpace(input.Code) == "" || input.RedirectURI != s.cfg.NotionRedirectURI {
		WriteError(w, http.StatusBadRequest, "OAUTH_CODE_INVALID", "invalid authorization code or redirect URI")
		return
	}
	token, err := s.deps.Notion.ExchangeCode(r.Context(), s.cfg.NotionClientID, s.cfg.NotionClientSecret, input.Code, input.RedirectURI)
	if err != nil && pipeline.ErrorCode(err) == "NOTION_BAD_REQUEST" {
		WriteError(w, http.StatusBadRequest, "OAUTH_CODE_INVALID", "authorization code rejected")
		return
	}
	if err != nil || token.AccessToken == "" {
		WriteError(w, http.StatusBadGateway, "NOTION_UNAVAILABLE", "Notion token exchange failed")
		return
	}
	writeJSON(w, http.StatusOK, token)
}

func (s *Server) listNotionPages(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok || token == "" || strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		WriteError(w, http.StatusUnauthorized, "NOTION_UNAUTHORIZED", "a bearer token is required")
		return
	}
	results, err := s.deps.Notion.Search(r.Context(), token, "", "")
	if err != nil {
		if pipeline.ErrorCode(err) == "NOTION_UNAUTHORIZED" {
			WriteError(w, http.StatusUnauthorized, "NOTION_UNAUTHORIZED", "Notion rejected the token")
			return
		}
		WriteError(w, http.StatusBadGateway, "NOTION_UNAVAILABLE", "Notion destination lookup failed")
		return
	}
	pages := make([]map[string]string, 0, len(results))
	for _, result := range results {
		pages = append(pages, map[string]string{"id": result.ID, "title": result.Title, "type": result.Type, "icon": result.Icon})
	}
	writeJSON(w, http.StatusOK, map[string]any{"pages": pages})
}
