package publish

import (
	"ainote/internal/notion"
	"ainote/internal/pipeline"
	"context"
	"errors"
)

// DefaultParentTitle is the exact name of the shared destination page.
const DefaultParentTitle = "AI Video Notes"

// NotionAPI is the subset of the Notion client used to publish a job.
type NotionAPI interface {
	Search(context.Context, string, string, string) ([]notion.SearchResult, error)
	RetrievePage(context.Context, string, string) (notion.Page, error)
	RetrieveDatabase(context.Context, string, string) ([]string, error)
	TitlePropertyName(context.Context, string, string) (string, error)
	CreatePage(context.Context, string, map[string]any, map[string]any, string) (notion.Page, error)
	ListChildren(context.Context, string, string) ([]notion.Block, error)
	AppendChildren(context.Context, string, string, []notion.Block) error
	ArchiveBlock(context.Context, string, string) error
	QueryDataSource(context.Context, string, string, map[string]any) ([]notion.Page, error)
	UploadImage(context.Context, string, string, []byte) (string, error)
}

var _ NotionAPI = (*notion.Client)(nil)

// ResolveParent prefers the requested destination, then the configured one,
// and finally the oldest shared page with the exact template title.
func ResolveParent(ctx context.Context, n NotionAPI, token, requested, envDefault string) (resolved, kind, titleProp string, err error) {
	id := requested
	if id == "" {
		id = envDefault
	}
	if id == "" {
		pages, err := n.Search(ctx, token, DefaultParentTitle, "page")
		if err != nil {
			return "", "", "", err
		}
		var best notion.SearchResult
		for _, p := range pages {
			if p.Type == "page" && p.Title == DefaultParentTitle && (best.ID == "" || p.CreatedTime.Before(best.CreatedTime) || (p.CreatedTime.Equal(best.CreatedTime) && p.ID < best.ID)) {
				best = p
			}
		}
		if best.ID == "" {
			return "", "", "", &pipeline.FatalError{Code: "NOTION_NO_DESTINATION", Msg: "Share a page called AI Video Notes with the app."}
		}
		return best.ID, "page", "title", nil
	}
	_, err = n.RetrievePage(ctx, token, id)
	if err == nil {
		return id, "page", "title", nil
	}
	if !errors.Is(err, notion.ErrObjectNotFound) {
		return "", "", "", err
	}
	sources, err := n.RetrieveDatabase(ctx, token, id)
	if errors.Is(err, notion.ErrObjectNotFound) || (err == nil && len(sources) == 0) {
		return "", "", "", &pipeline.FatalError{Code: "NOTION_PARENT_NOT_FOUND"}
	}
	if err != nil {
		return "", "", "", err
	}
	prop, err := n.TitlePropertyName(ctx, token, sources[0])
	if err != nil {
		return "", "", "", err
	}
	return sources[0], "data_source", prop, nil
}
