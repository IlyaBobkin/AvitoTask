package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/example/avito-kitchen/internal/kitchen/domain"
	"github.com/example/avito-kitchen/internal/kitchen/repository"
	"github.com/example/avito-kitchen/internal/kitchen/service"
	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type API struct {
	r *repository.Repository
	o *service.Orders
	v *validator.Validate
	l *slog.Logger
}

func New(r *repository.Repository, o *service.Orders, l *slog.Logger) *API {
	return &API{r, o, validator.New(), l}
}
func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/establishments", a.establishments)
		r.Get("/establishments/{id}", a.establishment)
		r.Get("/establishments/{id}/menu", a.menu)
		r.Post("/orders", a.create)
		r.Get("/orders/{id}", a.order)
		r.Post("/orders/{id}/cancel", a.cancel)
	})
	r.Route("/partner", func(r chi.Router) {
		r.Use(a.partner)
		r.Post("/menu/sync", a.sync)
		r.Post("/stocks", a.stocks)
		r.Get("/orders", a.partnerOrders)
		r.Get("/orders/{id}", a.partnerOrder)
		r.Patch("/orders/{id}/status", a.status)
	})
	return r
}
func id(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, "id"))
}
func (a *API) establishments(w http.ResponseWriter, r *http.Request) {
	x, e := a.r.Establishments(r.Context(), r.URL.Query().Get("q"))
	respond(w, e, x)
}
func (a *API) establishment(w http.ResponseWriter, r *http.Request) {
	id, err := id(r)
	if err != nil {
		respond(w, err, nil)
		return
	}

	est, err := a.r.Establishment(r.Context(), id)
	respond(w, err, est)
}
func (a *API) menu(w http.ResponseWriter, r *http.Request) {
	x, e := id(r)
	var v domain.Menu
	if e == nil {
		v, e = a.r.Menu(r.Context(), x)
	}
	respond(w, e, v)
}
func (a *API) create(w http.ResponseWriter, r *http.Request) {
	u, e := uuid.Parse(r.Header.Get("X-User-Id"))
	if e != nil {
		fail(w, 400, "invalid_user", "X-User-Id must be UUID")
		return
	}
	var in service.CreateOrder
	if e = decode(r, &in); e == nil {
		e = a.v.Struct(in)
	}
	if e == nil {
		var x domain.Order
		x, e = a.o.Create(r.Context(), u, in)
		if e == nil {
			write(w, 201, x)
			return
		}
	}
	respond(w, e, nil)
}
func (a *API) order(w http.ResponseWriter, r *http.Request) {
	x, e := id(r)
	var o domain.Order
	if e == nil {
		o, e = a.o.Get(r.Context(), x, nil)
	}
	respond(w, e, o)
}
func (a *API) cancel(w http.ResponseWriter, r *http.Request) {
	x, e := id(r)
	var b struct {
		Reason string `json:"reason"`
	}
	if e == nil {
		e = decode(r, &b)
	}
	if e == nil {
		e = a.o.Change(r.Context(), x, nil, "cancelled", b.Reason)
	}
	respond(w, e, map[string]string{"status": "cancelled"})
}
func (a *API) partner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		est, err := a.r.ByKey(r.Context(), r.Header.Get("X-API-Key"))
		if err != nil {
			fail(w, 401, "unauthorized", "invalid API key")
			return
		}
		r = withEst(r, est) // ← просто так
		next.ServeHTTP(w, r)
	})
}

type key struct{}

func withEst(r *http.Request, e domain.Establishment) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), key{}, e))
}
func est(r *http.Request) domain.Establishment {
	return r.Context().Value(key{}).(domain.Establishment)
}
func (a *API) sync(w http.ResponseWriter, r *http.Request) {
	var m domain.Menu
	e := decode(r, &m)
	if e == nil {
		e = a.r.SyncMenu(r.Context(), est(r).ID, m)
	}
	respond(w, e, map[string]string{"status": "synced"})
}
func (a *API) stocks(w http.ResponseWriter, r *http.Request) {
	var x []repository.StockInput
	e := decode(r, &x)
	if e == nil {
		for _, z := range x {
			if e = a.v.Struct(z); e != nil {
				break
			}
		}
	}
	if e == nil {
		e = a.r.UpdateStocks(r.Context(), est(r).ID, x)
	}
	respond(w, e, map[string]string{"status": "updated"})
}
func (a *API) partnerOrders(w http.ResponseWriter, r *http.Request) {
	rows, e := a.r.Pool.Query(r.Context(), "SELECT id,establishment_id,user_id,status,total_amount,NULL FROM orders WHERE establishment_id=$1 ORDER BY created_at DESC", est(r).ID)
	if e != nil {
		respond(w, e, nil)
		return
	}
	defer rows.Close()
	out := []domain.Order{}
	for rows.Next() {
		var o domain.Order
		_ = rows.Scan(&o.ID, &o.EstablishmentID, &o.UserID, &o.Status, &o.TotalAmount, &o.CancellationReason)
		out = append(out, o)
	}
	write(w, 200, out)
}
func (a *API) partnerOrder(w http.ResponseWriter, r *http.Request) {
	x, e := id(r)
	var o domain.Order
	if e == nil {
		ee := est(r).ID
		o, e = a.o.Get(r.Context(), x, &ee)
	}
	respond(w, e, o)
}
func (a *API) status(w http.ResponseWriter, r *http.Request) {
	x, e := id(r)
	var b struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if e == nil {
		e = decode(r, &b)
	}
	if e == nil {
		ee := est(r).ID
		e = a.o.Change(r.Context(), x, &ee, b.Status, b.Reason)
	}
	respond(w, e, map[string]string{"status": "updated"})
}
func decode(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }
func write(w http.ResponseWriter, s int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, s int, c, m string) {
	write(w, s, map[string]string{"code": c, "message": m})
}
func respond(w http.ResponseWriter, e error, v any) {
	if e == nil {
		write(w, 200, v)
		return
	}
	if errors.Is(e, service.ErrNotFound) {
		fail(w, 404, "not_found", e.Error())
		return
	}
	if errors.Is(e, service.ErrRule) {
		fail(w, 422, "validation_error", e.Error())
		return
	}
	fail(w, 400, "bad_request", e.Error())
}
