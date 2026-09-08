package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync"
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
	go a.sync()
	m := http.NewServeMux()
	m.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	m.HandleFunc("/webhook/order", a.webhook)
	m.HandleFunc("/menu", a.getMenu)
	m.HandleFunc("/sync", a.syncHTTP)
	m.HandleFunc("/orders", a.getOrders)
	slog.Info("restaurant started")
	slog.Error("server", "error", http.ListenAndServe(":"+port(), m))
}
func port() string {
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8081"
}
func (a *app) sync() {
	b, _ := json.Marshal(a.menu)

	req, _ := http.NewRequest(
		http.MethodPost,
		a.kitchen+"/partner/menu/sync",
		bytes.NewReader(b),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "demo-api-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("menu sync failed", "error", err)
		return
	}
	resp.Body.Close()

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

	sb, _ := json.Marshal(stocks)

	req, _ = http.NewRequest(
		http.MethodPost,
		a.kitchen+"/partner/stocks",
		bytes.NewReader(sb),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "demo-api-key")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("stock sync failed", "error", err)
		return
	}
	resp.Body.Close()
}
func (a *app) syncHTTP(w http.ResponseWriter, r *http.Request) { a.sync(); w.WriteHeader(204) }
func (a *app) webhook(w http.ResponseWriter, r *http.Request) {
	var x json.RawMessage
	if json.NewDecoder(r.Body).Decode(&x) == nil {
		a.mu.Lock()
		a.orders = append(a.orders, x)
		a.mu.Unlock()
	}
	w.WriteHeader(204)
}
func (a *app) getMenu(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(a.menu) }
func (a *app) getOrders(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	json.NewEncoder(w).Encode(a.orders)
}
