package notion

import (
	"context"
	"time"
)

// SearchResult identifies a destination page or parent database.
type SearchResult struct {
	ID, Title, Type, Icon string
	CreatedTime           time.Time
}

// Search enumerates accessible destinations. Data sources are exposed by parent database ID.
func (c *Client) Search(ctx context.Context, token, query, objectFilter string) ([]SearchResult, error) {
	params := map[string]any{}
	if query != "" {
		params["query"] = query
	}
	if objectFilter != "" {
		if objectFilter == "database" {
			objectFilter = "data_source"
		}
		params["filter"] = map[string]string{"property": "object", "value": objectFilter}
	}
	items, err := paginate[object](ctx, c, token, "POST", "/search", params)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var results []SearchResult
	for _, o := range items {
		id, kind := o.ID, o.Object
		switch kind {
		case "page", "database":
		case "data_source":
			id, _ = o.Parent["database_id"].(string)
			kind = "database"
		default:
			continue
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		icon := o.Icon.Emoji
		if icon == "" {
			icon = o.Icon.External.URL
		}
		if icon == "" {
			icon = o.Icon.File.URL
		}
		if icon == "" {
			icon = o.Icon.CustomEmoji.URL
		}
		results = append(results, SearchResult{id, o.page().Title, kind, icon, o.CreatedTime})
	}
	return results, nil
}
