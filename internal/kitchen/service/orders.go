package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/example/avito-kitchen/internal/kitchen/client"
	"github.com/example/avito-kitchen/internal/kitchen/domain"
	"github.com/example/avito-kitchen/internal/kitchen/repository"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrNotFound = errors.New("not found")
var ErrRule = errors.New("business rule violation")

type CreateOrder struct {
	EstablishmentID uuid.UUID    `json:"establishment_id" validate:"required"`
	Items           []CreateItem `json:"items" validate:"required,min=1,dive"`
}
type CreateItem struct {
	ProductID uuid.UUID   `json:"product_id" validate:"required"`
	Quantity  int         `json:"quantity" validate:"gte=1"`
	OptionIDs []uuid.UUID `json:"option_ids"`
}
type Orders struct {
	repo *repository.Repository
	hook *client.Webhook
}

func NewOrders(r *repository.Repository, h *client.Webhook) *Orders { return &Orders{r, h} }
func (s *Orders) Create(ctx context.Context, user uuid.UUID, in CreateOrder) (domain.Order, error) {
	e, eerr := s.repo.Establishment(ctx, in.EstablishmentID)
	if eerr != nil {
		return domain.Order{}, ErrNotFound
	}
	tx, er := s.repo.Tx(ctx)
	if er != nil {
		return domain.Order{}, er
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	o := domain.Order{ID: uuid.New(), EstablishmentID: in.EstablishmentID, UserID: user, Status: "created"}
	for _, it := range in.Items {
		var p domain.Product
		var category uuid.UUID
		er = tx.QueryRow(ctx, "SELECT id,name,description,price,is_available,category_id FROM products WHERE id=$1 AND establishment_id=$2 FOR UPDATE", it.ProductID, in.EstablishmentID).Scan(&p.ID, &p.Name, &p.Description, &p.Price, &p.IsAvailable, &category)
		if er != nil {
			return domain.Order{}, ErrRule
		}
		if !p.IsAvailable {
			return domain.Order{}, fmt.Errorf("%w: product unavailable", ErrRule)
		}
		var q int
		_ = tx.QueryRow(ctx, "SELECT quantity FROM stocks WHERE product_id=$1 FOR UPDATE", p.ID).Scan(&q)
		if q < it.Quantity {
			return domain.Order{}, fmt.Errorf("%w: product out of stock", ErrRule)
		}
		item := domain.OrderItem{ID: uuid.New(), ProductID: p.ID, ProductName: p.Name, UnitPrice: p.Price, Quantity: it.Quantity}
		rows, er := tx.Query(
			ctx,
			`SELECT
				m.id,
				m.is_required,
				o.id,
				o.name,
				o.price_delta,
				o.is_available
			FROM modifiers m
			JOIN modifier_options o ON o.modifier_id=m.id
			WHERE m.product_id=$1`,
			p.ID,
		)
		if er != nil {
			return domain.Order{}, er
		}

		selected := make(map[uuid.UUID]bool)
		for _, x := range it.OptionIDs {
			selected[x] = true
		}

		required := make(map[uuid.UUID]bool)

		// Сначала полностью читаем результат запроса.
		var options []struct {
			modifierID uuid.UUID
			required   bool
			option     domain.Option
		}

		for rows.Next() {
			var mid uuid.UUID
			var req bool
			var op domain.Option

			if er = rows.Scan(
				&mid,
				&req,
				&op.ID,
				&op.Name,
				&op.PriceDelta,
				&op.IsAvailable,
			); er != nil {
				rows.Close()
				return domain.Order{}, er
			}

			options = append(options, struct {
				modifierID uuid.UUID
				required   bool
				option     domain.Option
			}{
				modifierID: mid,
				required:   req,
				option:     op,
			})
		}

		if er = rows.Err(); er != nil {
			rows.Close()
			return domain.Order{}, er
		}

		rows.Close()

		// Теперь rows закрыт, поэтому можно выполнять новые запросы
		// через тот же transaction connection.
		for _, x := range options {
			if x.required {
				required[x.modifierID] = true
			}

			if !selected[x.option.ID] {
				continue
			}

			if !x.option.IsAvailable {
				return domain.Order{}, fmt.Errorf(
					"%w: option unavailable",
					ErrRule,
				)
			}

			var optionQuantity int

			er = tx.QueryRow(
				ctx,
				"SELECT quantity FROM stocks WHERE option_id=$1 FOR UPDATE",
				x.option.ID,
			).Scan(&optionQuantity)

			if er != nil {
				if errors.Is(er, pgx.ErrNoRows) {
					return domain.Order{}, fmt.Errorf(
						"%w: option out of stock",
						ErrRule,
					)
				}

				return domain.Order{}, er
			}

			if optionQuantity < it.Quantity {
				return domain.Order{}, fmt.Errorf(
					"%w: option out of stock",
					ErrRule,
				)
			}

			item.UnitPrice += x.option.PriceDelta

			item.Options = append(
				item.Options,
				domain.OrderItemOption{
					OptionID:   x.option.ID,
					OptionName: x.option.Name,
					PriceDelta: x.option.PriceDelta,
				},
			)

			delete(required, x.modifierID)
		}

		if len(required) > 0 {
			return domain.Order{}, fmt.Errorf(
				"%w: required modifier is not selected",
				ErrRule,
			)
		}

		item.TotalPrice = item.UnitPrice * int64(it.Quantity)
		o.TotalAmount += item.TotalPrice
		o.Items = append(o.Items, item)
	}
	if o.TotalAmount < e.MinOrderAmount {
		return domain.Order{}, fmt.Errorf("%w: minimum order amount is %d", ErrRule, e.MinOrderAmount)
	}
	_, er = tx.Exec(ctx, "INSERT INTO orders(id,establishment_id,user_id,status,total_amount) VALUES($1,$2,$3,$4,$5)", o.ID, o.EstablishmentID, o.UserID, o.Status, o.TotalAmount)
	if er != nil {
		return domain.Order{}, er
	}
	for _, x := range o.Items {
		_, er = tx.Exec(ctx, "INSERT INTO order_items VALUES($1,$2,$3,$4,$5,$6,$7)", x.ID, o.ID, x.ProductID, x.ProductName, x.UnitPrice, x.Quantity, x.TotalPrice)
		if er != nil {
			return domain.Order{}, er
		}
		_, er = tx.Exec(ctx, "UPDATE stocks SET quantity=quantity-$2 WHERE product_id=$1", x.ProductID, x.Quantity)
		if er != nil {
			return domain.Order{}, er
		}
		for _, z := range x.Options {
			_, er = tx.Exec(ctx, "INSERT INTO order_item_options VALUES($1,$2,$3,$4,$5)", uuid.New(), x.ID, z.OptionID, z.OptionName, z.PriceDelta)
			if er != nil {
				return domain.Order{}, er
			}
			_, er = tx.Exec(ctx, "UPDATE stocks SET quantity=quantity-$2 WHERE option_id=$1", z.OptionID, x.Quantity)
			if er != nil {
				return domain.Order{}, er
			}
		}
	}
	_, er = tx.Exec(ctx, "INSERT INTO order_status_history(id,order_id,status) VALUES($1,$2,'created')", uuid.New(), o.ID)
	if er != nil {
		return domain.Order{}, er
	}
	if er = tx.Commit(ctx); er != nil {
		return domain.Order{}, er
	}
	o.History = []domain.StatusHistory{{Status: "created", CreatedAt: time.Now().UTC().Format(time.RFC3339)}}
	_ = s.hook.Send(ctx, e.WebhookURL, o)
	return o, nil
}
func (s *Orders) Get(ctx context.Context, id uuid.UUID, eid *uuid.UUID) (domain.Order, error) {
	q := "SELECT id,establishment_id,user_id,status,total_amount,cancellation_reason FROM orders WHERE id=$1"
	a := []any{id}
	if eid != nil {
		q += " AND establishment_id=$2"
		a = append(a, *eid)
	}
	var o domain.Order
	er := s.repo.Pool.QueryRow(ctx, q, a...).Scan(&o.ID, &o.EstablishmentID, &o.UserID, &o.Status, &o.TotalAmount, &o.CancellationReason)
	if errors.Is(er, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	if er != nil {
		return o, er
	}
	rows, er := s.repo.Pool.Query(ctx, "SELECT id,product_id,product_name,unit_price,quantity,total_price FROM order_items WHERE order_id=$1", id)
	if er != nil {
		return o, er
	}
	defer rows.Close()
	for rows.Next() {
		var x domain.OrderItem
		if er = rows.Scan(&x.ID, &x.ProductID, &x.ProductName, &x.UnitPrice, &x.Quantity, &x.TotalPrice); er != nil {
			return o, er
		}
		os, _ := s.repo.Pool.Query(ctx, "SELECT option_id,option_name,price_delta FROM order_item_options WHERE order_item_id=$1", x.ID)
		for os.Next() {
			var z domain.OrderItemOption
			_ = os.Scan(&z.OptionID, &z.OptionName, &z.PriceDelta)
			x.Options = append(x.Options, z)
		}
		os.Close()
		o.Items = append(o.Items, x)
	}
	hs, _ := s.repo.Pool.Query(ctx, "SELECT status,reason,created_at::text FROM order_status_history WHERE order_id=$1 ORDER BY created_at", id)
	defer hs.Close()
	for hs.Next() {
		var h domain.StatusHistory
		_ = hs.Scan(&h.Status, &h.Reason, &h.CreatedAt)
		o.History = append(o.History, h)
	}
	return o, nil
}
func (s *Orders) Change(ctx context.Context, id uuid.UUID, eid *uuid.UUID, to, reason string) error {
	allowed := map[string][]string{"created": {"confirmed", "cancelled"}, "confirmed": {"cooking", "cancelled"}, "cooking": {"ready"}, "ready": {"delivering"}, "delivering": {"delivered"}}
	var old string
	q := "SELECT status FROM orders WHERE id=$1"
	a := []any{id}
	if eid != nil {
		q += " AND establishment_id=$2"
		a = append(a, *eid)
	}
	er := s.repo.Pool.QueryRow(ctx, q, a...).Scan(&old)
	if errors.Is(er, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if er != nil {
		return er
	}
	ok := false
	for _, v := range allowed[old] {
		ok = ok || v == to
	}
	if !ok || (to == "cancelled" && reason == "") {
		return fmt.Errorf("%w: invalid status transition", ErrRule)
	}
	_, er = s.repo.Pool.Exec(ctx, "UPDATE orders SET status=$2,cancellation_reason=CASE WHEN $2='cancelled' THEN $3 ELSE NULL END,updated_at=now() WHERE id=$1", id, to, reason)
	if er != nil {
		return er
	}
	_, er = s.repo.Pool.Exec(ctx, "INSERT INTO order_status_history(id,order_id,status,reason) VALUES($1,$2,$3,NULLIF($4,''))", uuid.New(), id, to, reason)
	return er
}
