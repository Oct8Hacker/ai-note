package transcript

import (
	"strings"

	"ainote/internal/youtube"
)

// PickLanguage prefers English, then falls back to the first available manual or automatic language.
func PickLanguage(m youtube.Metadata) (lang string, auto bool, ok bool) {
	bestRank := 6
	for index, tracks := range []map[string]bool{m.Subtitles, m.AutoCaptions} {
		for name, available := range tracks {
			if !available || name == "" {
				continue
			}
			rank := 4 + index
			switch {
			case name == "en":
				rank = 2 * index
			case strings.HasPrefix(name, "en-"):
				rank = 2*index + 1
			}
			if rank < bestRank || (rank == bestRank && name < lang) {
				bestRank = rank
				lang = name
				auto = index == 1
				ok = true
			}
		}
	}
	return lang, auto, ok
}
