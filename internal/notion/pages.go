package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Page contains the identifiers and display fields used by the publish stage.
type Page struct {
	ID, URL, Title string
	Parent         map[string]any
	CreatedTime    time.Time
}
type richText struct {
	Plain string `json:"plain_text"`
	Text  struct{ Content string }
}
type property struct {
	Type  string
	Name  string
	Title titleValue
}

// titleValue is a title property's value. On a page it is rich text (a JSON
// array); in a data source's schema it is a config object ({}) with no text.
type titleValue []richText

func (v *titleValue) UnmarshalJSON(b []byte) error {
	if !bytes.HasPrefix(bytes.TrimSpace(b), []byte("[")) {
		*v = nil
		return nil
	}
	return json.Unmarshal(b, (*[]richText)(v))
}

type object struct {
	ID, URL, Object string
	CreatedTime     time.Time `json:"created_time"`
	Properties      map[string]property
	Title           []richText
	Parent          map[string]any
	Icon            struct {
		Emoji          string
		External, File struct{ URL string }
		CustomEmoji    struct{ URL string } `json:"custom_emoji"`
	}
}

func (o object) page() Page {
	parts := o.Title
	if len(parts) == 0 {
		for _, p := range o.Properties {
			if p.Type == "title" {
				parts = []richText(p.Title)
				break
			}
		}
	}
	var title strings.Builder
	for _, t := range parts {
		if t.Plain != "" {
			title.WriteString(t.Plain)
		} else {
			title.WriteString(t.Text.Content)
		}
	}
	return Page{o.ID, o.URL, title.String(), o.Parent, o.CreatedTime}
}

// RetrievePage fetches a page and extracts its title property.
func (c *Client) RetrievePage(ctx context.Context, token, id string) (Page, error) {
	var o object
	err := c.json(ctx, bearer(token), "GET", resource("pages", id), nil, &o)
	return o.page(), err
}

// RetrieveDatabase returns the data sources in the database's reported order.
func (c *Client) RetrieveDatabase(ctx context.Context, token, id string) ([]string, error) {
	var db struct {
		Sources []struct{ ID string } `json:"data_sources"`
	}
	if err := c.json(ctx, bearer(token), "GET", resource("databases", id), nil, &db); err != nil {
		return nil, err
	}
	var ids []string
	for _, s := range db.Sources {
		ids = append(ids, s.ID)
	}
	return ids, nil
}

// TitlePropertyName finds the title column even when the user has renamed it.
func (c *Client) TitlePropertyName(ctx context.Context, token, id string) (string, error) {
	var o object
	if err := c.json(ctx, bearer(token), "GET", resource("data_sources", id), nil, &o); err != nil {
		return "", err
	}
	for key, p := range o.Properties {
		if p.Type == "title" {
			return key, nil
		}
	}
	return "", errors.New("data source has no title property")
}

// CreatePage creates a page below the supplied page or data-source parent.
func (c *Client) CreatePage(ctx context.Context, token string, parent, properties map[string]any, icon string) (Page, error) {
	body := map[string]any{"parent": parent, "properties": properties}
	if icon != "" {
		body["icon"] = map[string]string{"type": "emoji", "emoji": icon}
	}
	var o object
	err := c.json(ctx, bearer(token), "POST", "/pages", body, &o)
	return o.page(), err
}

// QueryDataSource lists pages matching the supplied Notion property filter.
func (c *Client) QueryDataSource(ctx context.Context, token, id string, filter map[string]any) ([]Page, error) {
	params := map[string]any{}
	if filter != nil {
		params["filter"] = filter
	}
	objects, err := paginate[object](ctx, c, token, "POST", resource("data_sources", id)+"/query", params)
	if err != nil {
		return nil, err
	}
	pages := make([]Page, 0, len(objects))
	for _, o := range objects {
		if o.Object == "page" || o.Object == "" {
			pages = append(pages, o.page())
		}
	}
	return pages, nil
}
