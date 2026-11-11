// Package notion implements AI-Note's versioned Notion API calls.
package notion

import (
	"ainote/internal/pipeline"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client applies shared fault and rate controls to every Notion request.
type Client struct {
	base, version string
	lim           pipeline.Limiter
	faults        pipeline.Faults
	http          *http.Client
}

// New constructs a client for the project’s pinned Notion API version.
func New(baseURL, version string, lim pipeline.Limiter, f pipeline.Faults, hc *http.Client) *Client {
	if baseURL == "" {
		baseURL = "https://api.notion.com/v1"
	}
	if version == "" {
		version = "2025-09-03"
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{strings.TrimRight(baseURL, "/"), version, lim, f, hc}
}

type auth struct{ token, clientID, secret, code string }

func bearer(token string) auth { return auth{token: token} }
func (c *Client) request(ctx context.Context, a auth, method, path, contentType string, body io.Reader, out any) error {
	if err := c.faults.Check(ctx, pipeline.FaultNotion); err != nil {
		return err
	}
	key := a.token
	if a.clientID != "" {
		key = a.clientID
	}
	if err := c.lim.Acquire(ctx, pipeline.NotionBucket(key), 1); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if a.clientID != "" {
		req.SetBasicAuth(a.clientID, a.secret)
	} else {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	req.Header.Set("Notion-Version", c.version)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return unavailable()
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(resp, a, req.Header.Get("Authorization"))
	}
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
	} else {
		err = json.NewDecoder(resp.Body).Decode(out)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return unavailable()
	}
	return nil
}
func (c *Client) json(ctx context.Context, a auth, method, path string, input, out any) error {
	var body io.Reader
	var media string
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
		media = "application/json"
	}
	return c.request(ctx, a, method, path, media, body, out)
}
func paginate[T any](ctx context.Context, c *Client, token, method, path string, params map[string]any) ([]T, error) {
	var all []T
	seen := map[string]bool{}
	cursor := ""
	for {
		endpoint := path
		var body any
		if method == http.MethodGet {
			if cursor != "" {
				endpoint += "?" + url.Values{"start_cursor": {cursor}}.Encode()
			}
		} else {
			payload := make(map[string]any, len(params)+1)
			for k, v := range params {
				payload[k] = v
			}
			if cursor != "" {
				payload["start_cursor"] = cursor
			}
			body = payload
		}
		var page struct {
			Results []T
			HasMore bool   `json:"has_more"`
			Next    string `json:"next_cursor"`
		}
		if err := c.json(ctx, bearer(token), method, endpoint, body, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Results...)
		if !page.HasMore {
			return all, nil
		}
		if page.Next == "" || seen[page.Next] {
			return nil, errors.New("invalid Notion pagination cursor")
		}
		cursor = page.Next
		seen[cursor] = true
	}
}
func resource(kind, id string) string { return fmt.Sprintf("/%s/%s", kind, url.PathEscape(id)) }
