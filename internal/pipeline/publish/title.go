// Package publish builds Notion pages from completed pipeline artifacts.
package publish

import (
	"fmt"
	"github.com/google/uuid"
	"net/url"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// ShortID returns the stable six-character job suffix used for page adoption.
func ShortID(id uuid.UUID) string { return id.String()[:6] }

// PageTitle truncates only the video title, leaving the job suffix intact.
func PageTitle(videoTitle, shortID string) string {
	runes := []rune(videoTitle)
	return string(runes[:min(150, len(runes))]) + " — Notes · " + shortID
}

// YouTubeLink seeks to the whole second containing the captured moment.
func YouTubeLink(videoID string, sec float64) string {
	return fmt.Sprintf("https://www.youtube.com/watch?v=%s&t=%ds", url.QueryEscape(videoID), int(max(0, sec)))
}

// SplitRichText splits at whitespace when possible and preserves every character.
func SplitRichText(s string, maxLen int) []string {
	if maxLen <= 0 || maxLen > 2000 {
		maxLen = 2000
	}
	var result []string
	for len(s) > 0 {
		end, lastSpace, units := 0, 0, 0
		for i, r := range s {
			width := utf16.RuneLen(r)
			if units+width > maxLen && i > 0 {
				break
			}
			units += width
			_, n := utf8.DecodeRuneInString(s[i:])
			end = i + n
			if unicode.IsSpace(r) {
				lastSpace = end
			}
		}
		if end < len(s) && lastSpace > 0 {
			end = lastSpace
		}
		result = append(result, s[:end])
		s = s[end:]
	}
	return result
}
