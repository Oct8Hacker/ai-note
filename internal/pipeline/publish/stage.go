package publish

import (
	"ainote/internal/notion"
	"ainote/internal/pipeline"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"strings"
	"time"
)

// Stage publishes one job's notes and checkpoints its destination, uploads and page.
type Stage struct {
	Notion          NotionAPI
	DefaultParentID string
	UploadReuse     time.Duration
	MaxImageBytes   int
	Now             func() time.Time
}

// Name identifies the publishing stage.
func (*Stage) Name() pipeline.Status { return pipeline.StatusPublishing }
func (s *Stage) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Run rebuilds the body of the job's checkpointed Notion page.
func (s *Stage) Run(ctx context.Context, j *pipeline.Job, d pipeline.Deps) (pipeline.StageResult, error) {
	var empty pipeline.StageResult
	if d.NotionToken == "" {
		return empty, &pipeline.FatalError{Code: "NOTION_UNAUTHORIZED"}
	}
	var syn pipeline.Synthesis
	found, err := d.Store.LoadArtifact(ctx, j.ID, "synthesis", &syn)
	if err != nil {
		return empty, err
	}
	if !found {
		return empty, errors.New("missing synthesis checkpoint")
	}
	if j.NotionResolvedParent == "" {
		id, kind, prop, err := ResolveParent(ctx, s.Notion, d.NotionToken, j.NotionParentID, s.DefaultParentID)
		if err != nil {
			return empty, err
		}
		if err = d.Store.SetNotionParent(ctx, j.ID, id, kind, prop); err != nil {
			return empty, err
		}
		j.NotionResolvedParent, j.NotionParentKind, j.NotionTitleProp = id, kind, prop
	}
	frames, err := d.Store.ListFrames(ctx, j.ID)
	if err != nil {
		return empty, err
	}
	uploads, err := s.uploadFrames(ctx, j, d, frames)
	if err != nil {
		return empty, err
	}
	warnings, err := d.Store.Warnings(ctx, j.ID)
	if err != nil {
		return empty, err
	}
	blocks := BuildBlocks(*j, syn, frames, uploads, warnings, s.now())
	if err := s.ensurePage(ctx, j, d); err != nil {
		return empty, err
	}
	existing, err := s.Notion.ListChildren(ctx, d.NotionToken, j.NotionPageID)
	if err != nil {
		return empty, err
	}
	for _, b := range existing {
		id, _ := b["id"].(string)
		if id == "" {
			return empty, errors.New("child block missing ID")
		}
		if err := s.Notion.ArchiveBlock(ctx, d.NotionToken, id); err != nil {
			return empty, err
		}
	}
	if err := s.Notion.AppendChildren(ctx, d.NotionToken, j.NotionPageID, blocks); err != nil {
		return empty, err
	}
	count := 0
	for _, b := range blocks {
		if b["type"] == "image" {
			count++
		}
	}
	d.Progress(100)
	return pipeline.StageResult{FramesPublished: &count, PageURL: j.NotionPageURL}, nil
}

func (s *Stage) uploadFrames(ctx context.Context, j *pipeline.Job, d pipeline.Deps, frames []pipeline.Frame) (map[int]string, error) {
	reuse := s.UploadReuse
	if reuse <= 0 {
		reuse = 50 * time.Minute
	}
	limit := s.MaxImageBytes
	if limit <= 0 {
		limit = 4500000
	}
	ids := map[int]string{}
	for i, f := range frames {
		if f.Status != "captured" || f.CapturedSec == nil || strings.TrimSpace(f.Caption) == "" {
			continue
		}
		recent := f.NotionUploadedAt != nil && s.now().Sub(*f.NotionUploadedAt) < reuse
		// A replay may retain an attached upload after local JPEG retention expires.
		if f.NotionFileUploadID != "" && (recent || len(f.ImageJPEG) == 0) {
			ids[f.Idx] = f.NotionFileUploadID
			continue
		}
		data, err := fitJPEG(f.ImageJPEG, limit)
		if err != nil {
			return nil, err
		}
		id, err := s.Notion.UploadImage(ctx, d.NotionToken, fmt.Sprintf("%s-%d.jpg", j.ID, f.Idx), data)
		if err != nil {
			return nil, err
		}
		if id == "" {
			return nil, errors.New("empty file upload ID")
		}
		stamp := s.now()
		f.NotionFileUploadID, f.NotionUploadedAt = id, &stamp
		if err := d.Store.UpsertFrame(ctx, f); err != nil {
			return nil, err
		}
		ids[f.Idx] = id
		d.Progress(70 * (i + 1) / len(frames))
	}
	return ids, nil
}
func fitJPEG(data []byte, limit int) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("captured frame has no image bytes")
	}
	if len(data) <= limit {
		return data, nil
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	for _, quality := range []int{80, 70, 60} {
		out.Reset()
		if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
		if out.Len() <= limit {
			return out.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("frame exceeds %d bytes after JPEG compression", limit)
}

func (s *Stage) ensurePage(ctx context.Context, j *pipeline.Job, d pipeline.Deps) error {
	if j.NotionPageID != "" {
		return nil
	}
	videoTitle := j.VideoTitle
	if strings.TrimSpace(videoTitle) == "" {
		videoTitle = "YouTube video " + j.VideoID
	}
	title := PageTitle(videoTitle, ShortID(j.ID))
	var page notion.Page
	var err error
	if j.NotionCreateStartedAt != nil {
		page, err = s.findPage(ctx, d.NotionToken, j, title)
		if err != nil {
			return err
		}
	}
	if page.ID == "" {
		if err := d.Store.MarkNotionCreateStarted(ctx, j.ID); err != nil {
			return err
		}
		stamp := s.now()
		j.NotionCreateStartedAt = &stamp
		parentType := j.NotionParentKind + "_id"
		parent := map[string]any{"type": parentType, parentType: j.NotionResolvedParent}
		properties := map[string]any{j.NotionTitleProp: map[string]any{"type": "title", "title": richText(title, "")}}
		page, err = s.Notion.CreatePage(ctx, d.NotionToken, parent, properties, "📺")
		if errors.Is(err, notion.ErrObjectNotFound) {
			return &pipeline.FatalError{Code: "NOTION_PARENT_NOT_FOUND"}
		}
		if err != nil {
			return err
		}
	}
	if page.ID == "" || page.URL == "" {
		return errors.New("page response missing ID or URL")
	}
	if err := d.Store.SetNotionPage(ctx, j.ID, page.ID, page.URL); err != nil {
		return err
	}
	j.NotionPageID, j.NotionPageURL = page.ID, page.URL
	return nil
}
func (s *Stage) findPage(ctx context.Context, token string, j *pipeline.Job, title string) (notion.Page, error) {
	// A six-character ID is only a search hint. Verify the full generated title
	// before replacing any candidate page's children.
	var matches []notion.Page
	if j.NotionParentKind == "data_source" {
		filter := map[string]any{"property": j.NotionTitleProp, "title": map[string]any{"contains": ShortID(j.ID)}}
		pages, err := s.Notion.QueryDataSource(ctx, token, j.NotionResolvedParent, filter)
		if err != nil {
			return notion.Page{}, err
		}
		for _, p := range pages {
			if p.Title == title {
				matches = append(matches, p)
			}
		}
	} else {
		children, err := s.Notion.ListChildren(ctx, token, j.NotionResolvedParent)
		if err != nil {
			return notion.Page{}, err
		}
		for _, b := range children {
			child, _ := b["child_page"].(map[string]any)
			if b["type"] == "child_page" && child["title"] == title {
				id, _ := b["id"].(string)
				if id == "" {
					return notion.Page{}, errors.New("child page missing ID")
				}
				matches = append(matches, notion.Page{ID: id})
			}
		}
	}
	switch len(matches) {
	case 0:
		return notion.Page{}, nil
	case 1:
		if matches[0].URL == "" {
			return s.Notion.RetrievePage(ctx, token, matches[0].ID)
		}
		return matches[0], nil
	default:
		return notion.Page{}, errors.New("multiple Notion pages match this job title")
	}
}
