package api

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	hex32Re     = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
	dashedRe    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	trailingHex = regexp.MustCompile(`(?:^|[-/])([0-9a-fA-F]{32})$`)
	errNotionID = errors.New("not a Notion ID")
)

// ParseNotionID accepts a 32-hex ID (with or without dashes) or a notion.so
// URL ending in one, and returns the lowercase dashed UUID form.
func ParseNotionID(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	var hex string
	switch {
	case hex32Re.MatchString(s):
		hex = s
	case dashedRe.MatchString(s):
		hex = strings.ReplaceAll(s, "-", "")
	default:
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return "", errNotionID
		}
		h := strings.ToLower(u.Hostname())
		if h != "notion.so" && !strings.HasSuffix(h, ".notion.so") && h != "notion.site" && !strings.HasSuffix(h, ".notion.site") {
			return "", errNotionID
		}
		m := trailingHex.FindStringSubmatch(strings.TrimRight(u.Path, "/"))
		if m == nil {
			return "", errNotionID
		}
		hex = m[1]
	}
	hex = strings.ToLower(hex)
	return hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:], nil
}
