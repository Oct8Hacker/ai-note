package api

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var videoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

var errInvalidURL = errors.New("not a YouTube video URL")

// ParseVideoID extracts the 11-char video ID from the YouTube URL forms
// accepted by IR-API-1.
func ParseVideoID(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errInvalidURL
	}
	host := strings.ToLower(u.Hostname())
	var id string
	switch host {
	case "youtu.be":
		id = strings.Trim(u.Path, "/")
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		p := strings.Trim(u.Path, "/")
		if p == "watch" {
			id = u.Query().Get("v")
		} else if kind, rest, ok := strings.Cut(p, "/"); ok && (kind == "embed" || kind == "shorts" || kind == "live") {
			id = rest
		}
	}
	if !videoIDRe.MatchString(id) {
		return "", errInvalidURL
	}
	return id, nil
}
