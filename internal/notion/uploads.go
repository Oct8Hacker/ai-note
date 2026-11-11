package notion

import (
	"bytes"
	"context"
	"errors"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
)

// UploadImage creates a single-part upload and sends its JPEG bytes.
func (c *Client) UploadImage(ctx context.Context, token, filename string, jpeg []byte) (string, error) {
	if strings.ContainsAny(filename, "\r\n") {
		return "", errors.New("invalid upload filename")
	}
	var upload struct{ ID string }
	if err := c.json(ctx, bearer(token), "POST", "/file_uploads", map[string]string{"mode": "single_part", "filename": filename, "content_type": "image/jpeg"}, &upload); err != nil {
		return "", err
	}
	if upload.ID == "" {
		return "", errors.New("missing Notion file upload ID")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": filename}))
	h.Set("Content-Type", "image/jpeg")
	file, err := writer.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err = file.Write(jpeg); err != nil {
		return "", err
	}
	if err = writer.Close(); err != nil {
		return "", err
	}
	if err = c.request(ctx, bearer(token), "POST", resource("file_uploads", upload.ID)+"/send", writer.FormDataContentType(), &body, nil); err != nil {
		return "", err
	}
	return upload.ID, nil
}
