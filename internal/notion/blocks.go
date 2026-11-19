package notion

import "context"

// Block represents a Notion block payload.
type Block = map[string]any

// ListChildren reads all pages of child blocks in their original order.
func (c *Client) ListChildren(ctx context.Context, token, id string) ([]Block, error) {
	return paginate[Block](ctx, c, token, "GET", resource("blocks", id)+"/children", nil)
}

// AppendChildren appends ordered batches within Notion's 100-block limit.
func (c *Client) AppendChildren(ctx context.Context, token, id string, blocks []Block) error {
	for len(blocks) > 0 {
		n := min(100, len(blocks))
		err := c.json(ctx, bearer(token), "PATCH", resource("blocks", id)+"/children", map[string]any{"children": blocks[:n]}, nil)
		if err != nil {
			return err
		}
		blocks = blocks[n:]
	}
	return nil
}

// ArchiveBlock moves the block to the trash.
func (c *Client) ArchiveBlock(ctx context.Context, token, id string) error {
	return c.json(ctx, bearer(token), "DELETE", resource("blocks", id), nil, nil)
}
