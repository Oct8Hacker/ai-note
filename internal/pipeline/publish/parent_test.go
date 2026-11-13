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

// badRequestNotion answers like the real API (#38): GET /pages with a database
// ID is 400 validation_error ("is a database, not a page"), not 404.
type badRequestNotion struct{ NotionAPI }

func (badRequestNotion) RetrievePage(_ context.Context, _ string, id string) (notion.Page, error) {
	if id == "busy" {
		return notion.Page{}, &pipeline.RateLimitedError{Provider: "notion", RetryAfter: time.Second}
	}
	return notion.Page{}, &pipeline.RetryableError{Code: "NOTION_BAD_REQUEST"}
}
func (badRequestNotion) RetrieveDatabase(_ context.Context, _ string, id string) ([]string, error) {
	if id == "db" {
		return []string{"src"}, nil
	}
	return nil, &pipeline.RetryableError{Code: "NOTION_BAD_REQUEST"}
}
func (badRequestNotion) TitlePropertyName(context.Context, string, string) (string, error) {
	return "Name", nil
}

func TestResolveDatabaseAfterPageBadRequest(t *testing.T) {
	ctx := context.Background()
	f := badRequestNotion{}

	id, kind, prop, err := ResolveParent(ctx, f, "token", "db", "")
	require.NoError(t, err, "a database destination must resolve when /pages answers 400")
	require.Equal(t, []string{"src", "data_source", "Name"}, []string{id, kind, prop})

	_, _, _, err = ResolveParent(ctx, f, "token", "block", "")
	require.Equal(t, "NOTION_PARENT_NOT_FOUND", pipeline.ErrorCode(err), "neither a page nor a database: fail, don't retry forever")
	require.Equal(t, pipeline.ClassFatal, pipeline.Classify(err))

	_, _, _, err = ResolveParent(ctx, f, "token", "busy", "")
	var rate *pipeline.RateLimitedError
	require.ErrorAs(t, err, &rate, "other page-lookup errors pass through unchanged")
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
