package publish

import (
	"ainote/internal/notion"
	"ainote/internal/pipeline"
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type fakeNotion struct {
	NotionAPI
	results                    []notion.SearchResult
	db                         bool
	children                   []notion.Block
	body                       []notion.Block
	matches                    []notion.Page
	creates, uploads, archives int
	createErr, appendErr       error
	lookup                     map[string]any
}

func (f *fakeNotion) Search(context.Context, string, string, string) ([]notion.SearchResult, error) {
	return f.results, nil
}
func (f *fakeNotion) RetrievePage(_ context.Context, _ string, id string) (notion.Page, error) {
	if id == "missing" || id == "db" {
		return notion.Page{}, notion.ErrObjectNotFound
	}
	return notion.Page{ID: id, URL: "https://example.org/" + id}, nil
}
func (f *fakeNotion) RetrieveDatabase(context.Context, string, string) ([]string, error) {
	if f.db {
		return []string{"src", "other"}, nil
	}
	return nil, notion.ErrObjectNotFound
}
func (*fakeNotion) TitlePropertyName(context.Context, string, string) (string, error) {
	return "Lecture", nil
}
func TestResolveDestinations(t *testing.T) {
	ctx := context.Background()
	f := &fakeNotion{db: true, results: []notion.SearchResult{{ID: "wrong", Type: "page", Title: "Other"}, {ID: "new", Type: "page", Title: DefaultParentTitle, CreatedTime: time.Unix(100, 0)}, {ID: "old", Type: "page", Title: DefaultParentTitle, CreatedTime: time.Unix(50, 0)}}}
	for _, tc := range []struct{ requested, env, id, kind, prop string }{{"page", "db", "page", "page", "title"}, {"", "db", "src", "data_source", "Lecture"}, {"", "", "old", "page", "title"}} {
		id, k, p, e := ResolveParent(ctx, f, "token", tc.requested, tc.env)
		require.NoError(t, e)
		require.Equal(t, tc.id, id)
		require.Equal(t, tc.kind, k)
		require.Equal(t, tc.prop, p)
	}
	f.results = nil
	_, _, _, e := ResolveParent(ctx, f, "token", "", "")
	require.Equal(t, "NOTION_NO_DESTINATION", pipeline.ErrorCode(e))
	f.db = false
	_, _, _, e = ResolveParent(ctx, f, "token", "missing", "")
	require.Equal(t, "NOTION_PARENT_NOT_FOUND", pipeline.ErrorCode(e))
}
