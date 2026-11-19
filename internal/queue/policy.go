package queue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ApplyDeliveryLimitPolicy sets the jobs-work-limits policy through the
// RabbitMQ management API. A policy (not a queue argument) is used so the
// limit can change without a 406 PRECONDITION_FAILED on redeclare.
func ApplyDeliveryLimitPolicy(ctx context.Context, mgmtURL, user, pass string, limit int) error {
	body, err := json.Marshal(map[string]any{
		"pattern":    `^jobs\.work$`,
		"definition": map[string]any{"delivery-limit": limit},
		"apply-to":   "queues",
		"priority":   0,
	})
	if err != nil {
		return err
	}
	url := strings.TrimRight(mgmtURL, "/") + "/api/policies/%2F/jobs-work-limits"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(user, pass)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("apply policy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("apply policy: status %d: %s", resp.StatusCode, msg)
	}
	return nil
}
