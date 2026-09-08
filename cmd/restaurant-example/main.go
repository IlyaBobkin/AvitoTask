package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

type app struct {
	mu      sync.Mutex
	menu    any
	orders  []json.RawMessage
	kitchen string
}

func main() {
	a := &app{kitchen: os.Getenv("KITCHEN_URL")}
	if a.kitchen == "" {
		a.kitchen = "http://localhost:8080"
	}

	a.menu = map[string]any{
		"categories": []any{
			map[string]any{
				"id":       "b1111111-1111-1111-1111-111111111111",
				"name":     "Popular",
				"position": 1,
				"products": []any{
					map[string]any{
						"id":           "a9f8378f-ed76-4664-9562-949129e9cc9a",
						"name":         "Burger",
						"description":  "Demo burger",
						"price":        60000,
						"is_available": true,
						"modifiers": []any{
							map[string]any{
								"id":          "c2833d65-627e-46f6-bf35-972cccd5bc00",
								"name":        "Sauce",
								"is_required": true,
								"options": []any{
									map[string]any{
										"id":           "a2833d65-627e-46f6-bf35-972cccd5bc00",
										"name":         "Cheese",
										"price_delta":  10000,
										"is_available": true,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	go a.syncWithRetry()

	m := http.NewServeMux()

	m.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
			slog.Error("failed to encode health response", "error", err)
		}
	})

	m.HandleFunc("/webhook/order", a.webhook)
	m.HandleFunc("/menu", a.getMenu)
	m.HandleFunc("/sync", a.syncHTTP)
	m.HandleFunc("/orders", a.getOrders)

	slog.Info("restaurant started")

	slog.Error(
		"server",
		"error",
		http.ListenAndServe(":"+port(), m),
	)
}

func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}

	return "8081"
}

func (a *app) syncWithRetry() {
	const (
		maxAttempts = 10
		retryDelay  = 2 * time.Second
	)

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := a.sync()
		if err == nil {
			slog.Info("restaurant synchronized with kitchen-api")
			return
		}

		slog.Warn(
			"restaurant synchronization failed",
			"attempt",
			attempt,
			"max_attempts",
			maxAttempts,
			"error",
			err,
		)

		if attempt < maxAttempts {
			time.Sleep(retryDelay)
		}
	}

	slog.Error("restaurant synchronization failed after all attempts")
}

func (a *app) sync() error {
	b, err := json.Marshal(a.menu)
	if err != nil {
		return fmt.Errorf("marshal menu: %w", err)
	}

	req, err := http.NewRequest(
		http.MethodPost,
		a.kitchen+"/partner/menu/sync",
		bytes.NewReader(b),
	)
	if err != nil {
		return fmt.Errorf("create menu sync request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "demo-api-key")

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("menu sync request: %w", err)
	}

	if err := resp.Body.Close(); err != nil {
		slog.Warn("failed to close menu sync response body", "error", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("menu sync returned status %s", resp.Status)
	}

	stocks := []map[string]any{
		{
			"id":       "a9f8378f-ed76-4664-9562-949129e9cc9a",
			"kind":     "product",
			"quantity": 10,
		},
		{
			"id":       "a2833d65-627e-46f6-bf35-972cccd5bc00",
			"kind":     "option",
			"quantity": 10,
		},
	}

	sb, err := json.Marshal(stocks)
	if err != nil {
		return fmt.Errorf("marshal stocks: %w", err)
	}

	req, err = http.NewRequest(
		http.MethodPost,
		a.kitchen+"/partner/stocks",
		bytes.NewReader(sb),
	)
	if err != nil {
		return fmt.Errorf("create stock sync request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "demo-api-key")

	resp, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("stock sync request: %w", err)
	}

	if err := resp.Body.Close(); err != nil {
		slog.Warn("failed to close stock sync response body", "error", err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("stock sync returned status %s", resp.Status)
	}

	return nil
}

func (a *app) syncHTTP(w http.ResponseWriter, _ *http.Request) {
	if err := a.sync(); err != nil {
		slog.Error("manual synchronization failed", "error", err)
		http.Error(w, "synchronization failed", http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *app) webhook(w http.ResponseWriter, r *http.Request) {
	var x json.RawMessage

	if json.NewDecoder(r.Body).Decode(&x) == nil {
		a.mu.Lock()
		a.orders = append(a.orders, x)
		a.mu.Unlock()
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *app) getMenu(w http.ResponseWriter, _ *http.Request) {
	if err := json.NewEncoder(w).Encode(a.menu); err != nil {
		slog.Error("failed to encode menu", "error", err)
	}
}

func (a *app) getOrders(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := json.NewEncoder(w).Encode(a.orders); err != nil {
		slog.Error("failed to encode orders", "error", err)
	}
}
