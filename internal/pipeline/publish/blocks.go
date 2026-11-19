package publish

import (
	"ainote/internal/notion"
	"ainote/internal/pipeline"
	"ainote/internal/pipeline/synthesis"
	"fmt"
	"net/url"
	"strings"
	"time"
)

func richText(content, link string) []notion.Block {
	spans := []notion.Block{}
	for _, part := range SplitRichText(content, 2000) {
		text := notion.Block{"content": part}
		if link != "" {
			text["link"] = notion.Block{"url": link}
		}
		spans = append(spans, notion.Block{"type": "text", "text": text})
	}
	return spans
}
func block(kind string, data notion.Block) notion.Block {
	return notion.Block{"object": "block", "type": kind, kind: data}
}
func textBlock(kind, text, link string) notion.Block {
	return block(kind, notion.Block{"rich_text": richText(text, link)})
}
func callout(emoji, text string) notion.Block {
	return block("callout", notion.Block{"icon": notion.Block{"type": "emoji", "emoji": emoji}, "rich_text": richText(text, "")})
}
func warningText(w pipeline.Warning) string {
	at := ""
	if w.TimestampSec != nil {
		at = " at " + synthesis.FormatTimestamp(*w.TimestampSec)
	}
	switch w.Code {
	case pipeline.WarnFrameExtractionFailed:
		return "1 screenshot" + at + " couldn't be captured."
	case pipeline.WarnFrameNotRelevant:
		return "A screenshot" + at + " was omitted because it didn't show the expected visual."
	case pipeline.WarnVisualDroppedInvalid:
		return "A suggested screenshot" + at + " was omitted because its details were incomplete or invalid."
	case pipeline.WarnFrameVerifySkipped:
		return "Screenshot verification was unavailable. The original captions were kept."
	default:
		return "Some notes or screenshots may be incomplete."
	}
}
func imageBlock(f pipeline.Frame, id, videoID string) notion.Block {
	sec := *f.CapturedSec
	caption := f.Caption
	if strings.TrimSpace(f.Reason) != "" {
		caption += " — Why it matters: " + f.Reason
	}
	spans := richText(caption+" ", "")
	spans = append(spans, richText("("+synthesis.FormatTimestamp(sec)+")", YouTubeLink(videoID, sec))...)
	return block("image", notion.Block{"type": "file_upload", "file_upload": notion.Block{"id": id}, "caption": spans})
}

// BuildBlocks renders metadata, notes, eligible uploaded frames and the footer in reading order.
func BuildBlocks(job pipeline.Job, syn pipeline.Synthesis, frames []pipeline.Frame, uploads map[int]string, warnings []pipeline.Warning, now time.Time) []notion.Block {
	videoLink := "https://www.youtube.com/watch?v=" + url.QueryEscape(job.VideoID)
	meta := richText("Watch on YouTube", videoLink)
	meta = append(meta, richText(" · Duration "+synthesis.FormatTimestamp(float64(job.DurationSec))+" · Generated "+now.Format(time.DateOnly), "")...)
	blocks := []notion.Block{block("paragraph", notion.Block{"rich_text": meta}), textBlock("heading_2", "Overview", ""), textBlock("paragraph", syn.Overview, "")}
	bySection := make(map[int][]notion.Block)
	for _, f := range frames {
		upload := uploads[f.Idx]
		if f.Status != "captured" || strings.TrimSpace(f.Caption) == "" || f.CapturedSec == nil || strings.TrimSpace(upload) == "" {
			continue
		}
		index := f.SectionIndex
		// A scene snap can move the actual capture across a section boundary.
		for j, section := range syn.Sections {
			if *f.CapturedSec >= section.StartSec && *f.CapturedSec < section.EndSec {
				index = j
				break
			}
		}
		bySection[index] = append(bySection[index], imageBlock(f, upload, job.VideoID))
	}
	for i, section := range syn.Sections {
		heading := fmt.Sprintf("%d. %s (%s–%s)", i+1, section.Heading, synthesis.FormatTimestamp(section.StartSec), synthesis.FormatTimestamp(section.EndSec))
		blocks = append(blocks, textBlock("heading_2", heading, YouTubeLink(job.VideoID, section.StartSec)))
		for _, bullet := range section.Bullets {
			blocks = append(blocks, textBlock("bulleted_list_item", bullet, ""))
		}
		blocks = append(blocks, bySection[i]...)
	}
	if len(warnings) > 0 {
		lines := make([]string, 0, len(warnings))
		for _, w := range warnings {
			lines = append(lines, warningText(w))
		}
		blocks = append(blocks, callout("⚠️", strings.Join(lines, "\n")))
	}
	return append(blocks, callout("ℹ️", "Screenshots are real frames from the video; captions are AI-written."))
}
