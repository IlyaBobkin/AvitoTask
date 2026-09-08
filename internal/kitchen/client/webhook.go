package client

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/example/avito-kitchen/internal/kitchen/domain"
	"net/http"
	"time"
)

type Webhook struct{ http *http.Client }

func NewWebhook() *Webhook { return &Webhook{&http.Client{Timeout: 5 * time.Second}} }
func (w *Webhook) Send(ctx context.Context, url string, o domain.Order) error {
	b, e := json.Marshal(map[string]any{"event": "order.created", "order_id": o.ID, "created_at": time.Now().UTC(), "order": o})
	if e != nil {
		return e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	res, e := w.http.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return &statusError{res.StatusCode}
	}
	return nil
}

type statusError struct{ n int }

func (e *statusError) Error() string { return "webhook returned non-success" }
