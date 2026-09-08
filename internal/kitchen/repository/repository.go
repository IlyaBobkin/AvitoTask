package repository

import (
	"context"
	"embed"
	"fmt"

	"github.com/example/avito-kitchen/internal/kitchen/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Repository struct {
	Pool *pgxpool.Pool
}

func New(ctx context.Context, url string) (*Repository, error) {
	p, e := pgxpool.New(ctx, url)
	if e != nil {
		return nil, e
	}

	return &Repository{p}, p.Ping(ctx)
}

func (r *Repository) Migrate(ctx context.Context) error {
	b, e := migrationFS.ReadFile("migrations/00001_init.sql")
	if e != nil {
		return e
	}

	_, e = r.Pool.Exec(
		ctx,
		"CREATE TABLE IF NOT EXISTS goose_db_version(version_id bigint primary key, is_applied boolean not null)",
	)
	if e != nil {
		return e
	}

	var n int
	_ = r.Pool.QueryRow(
		ctx,
		"SELECT count(*) FROM goose_db_version WHERE version_id=1 AND is_applied",
	).Scan(&n)

	if n > 0 {
		return nil
	}

	up := string(b)
	up = up[:len(up)-len("-- +goose Down\nDROP TABLE IF EXISTS order_status_history, order_item_options, order_items, orders, stocks, modifier_options, modifiers, products, categories, establishments;\n")]

	if _, e = r.Pool.Exec(ctx, up); e != nil {
		return e
	}

	_, e = r.Pool.Exec(ctx, "INSERT INTO goose_db_version VALUES(1,true)")
	return e
}

func (r *Repository) Establishments(ctx context.Context, q string) ([]domain.Establishment, error) {
	rows, e := r.Pool.Query(
		ctx,
		"SELECT id,name,description,min_order_amount,webhook_url FROM establishments WHERE is_active AND (name ILIKE '%'||$1||'%' OR $1='') ORDER BY name",
		q,
	)
	if e != nil {
		return nil, e
	}
	defer rows.Close()

	out := []domain.Establishment{}

	for rows.Next() {
		var x domain.Establishment

		e = rows.Scan(
			&x.ID,
			&x.Name,
			&x.Description,
			&x.MinOrderAmount,
			&x.WebhookURL,
		)
		if e != nil {
			return nil, e
		}

		out = append(out, x)
	}

	return out, rows.Err()
}

func (r *Repository) Establishment(ctx context.Context, id uuid.UUID) (domain.Establishment, error) {
	var x domain.Establishment

	e := r.Pool.QueryRow(
		ctx,
		"SELECT id,name,description,min_order_amount,webhook_url FROM establishments WHERE id=$1 AND is_active",
		id,
	).Scan(
		&x.ID,
		&x.Name,
		&x.Description,
		&x.MinOrderAmount,
		&x.WebhookURL,
	)

	return x, e
}

func (r *Repository) ByKey(ctx context.Context, key string) (domain.Establishment, error) {
	var x domain.Establishment

	e := r.Pool.QueryRow(
		ctx,
		"SELECT id,name,description,min_order_amount,webhook_url FROM establishments WHERE api_key=$1 AND is_active",
		key,
	).Scan(
		&x.ID,
		&x.Name,
		&x.Description,
		&x.MinOrderAmount,
		&x.WebhookURL,
	)

	return x, e
}

func (r *Repository) Menu(ctx context.Context, eid uuid.UUID) (domain.Menu, error) {
	rows, e := r.Pool.Query(
		ctx,
		"SELECT id,name,position FROM categories WHERE establishment_id=$1 ORDER BY position,name",
		eid,
	)
	if e != nil {
		return domain.Menu{}, e
	}
	defer rows.Close()

	out := domain.Menu{}

	for rows.Next() {
		var c domain.Category

		if e = rows.Scan(&c.ID, &c.Name, &c.Position); e != nil {
			return out, e
		}

		ps, e := r.Pool.Query(
			ctx,
			"SELECT id,name,description,price,is_available FROM products WHERE category_id=$1 ORDER BY name",
			c.ID,
		)
		if e != nil {
			return out, e
		}

		for ps.Next() {
			var p domain.Product

			if e = ps.Scan(
				&p.ID,
				&p.Name,
				&p.Description,
				&p.Price,
				&p.IsAvailable,
			); e != nil {
				ps.Close()
				return out, e
			}

			ms, e := r.Pool.Query(
				ctx,
				"SELECT id,name,is_required FROM modifiers WHERE product_id=$1 ORDER BY name",
				p.ID,
			)
			if e != nil {
				ps.Close()
				return out, e
			}

			for ms.Next() {
				var m domain.Modifier

				if e = ms.Scan(
					&m.ID,
					&m.Name,
					&m.IsRequired,
				); e != nil {
					ms.Close()
					ps.Close()
					return out, e
				}

				os, e := r.Pool.Query(
					ctx,
					"SELECT id,name,price_delta,is_available FROM modifier_options WHERE modifier_id=$1 ORDER BY name",
					m.ID,
				)
				if e != nil {
					ms.Close()
					ps.Close()
					return out, e
				}

				for os.Next() {
					var o domain.Option

					if e = os.Scan(
						&o.ID,
						&o.Name,
						&o.PriceDelta,
						&o.IsAvailable,
					); e != nil {
						os.Close()
						ms.Close()
						ps.Close()
						return out, e
					}

					m.Options = append(m.Options, o)
				}

				os.Close()
				p.Modifiers = append(p.Modifiers, m)
			}

			ms.Close()
			c.Products = append(c.Products, p)
		}

		ps.Close()
		out.Categories = append(out.Categories, c)
	}

	return out, rows.Err()
}

func (r *Repository) SyncMenu(ctx context.Context, eid uuid.UUID, m domain.Menu) error {
	tx, e := r.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)

	// Полностью заменяем меню ресторана.
	// Благодаря CASCADE старые stocks тоже удаляются.
	if _, e = tx.Exec(
		ctx,
		"DELETE FROM categories WHERE establishment_id=$1",
		eid,
	); e != nil {
		return e
	}

	for _, c := range m.Categories {
		cid := c.ID

		if cid == uuid.Nil {
			cid = uuid.New()
		}

		if _, e = tx.Exec(
			ctx,
			"INSERT INTO categories(id,establishment_id,name,position) VALUES($1,$2,$3,$4)",
			cid,
			eid,
			c.Name,
			c.Position,
		); e != nil {
			return e
		}

		for _, p := range c.Products {
			pid := p.ID

			if pid == uuid.Nil {
				pid = uuid.New()
			}

			if _, e = tx.Exec(
				ctx,
				"INSERT INTO products VALUES($1,$2,$3,$4,$5,$6,$7)",
				pid,
				eid,
				cid,
				p.Name,
				p.Description,
				p.Price,
				p.IsAvailable,
			); e != nil {
				return e
			}

			// Начальный остаток продукта.
			if _, e = tx.Exec(
				ctx,
				"INSERT INTO stocks(id,product_id,quantity) VALUES($1,$2,$3)",
				uuid.New(),
				pid,
				10,
			); e != nil {
				return e
			}

			for _, m := range p.Modifiers {
				mid := m.ID

				if mid == uuid.Nil {
					mid = uuid.New()
				}

				if _, e = tx.Exec(
					ctx,
					"INSERT INTO modifiers VALUES($1,$2,$3,$4)",
					mid,
					pid,
					m.Name,
					m.IsRequired,
				); e != nil {
					return e
				}

				for _, o := range m.Options {
					oid := o.ID

					if oid == uuid.Nil {
						oid = uuid.New()
					}

					if _, e = tx.Exec(
						ctx,
						"INSERT INTO modifier_options VALUES($1,$2,$3,$4,$5)",
						oid,
						mid,
						o.Name,
						o.PriceDelta,
						o.IsAvailable,
					); e != nil {
						return e
					}

					// Начальный остаток option.
					if _, e = tx.Exec(
						ctx,
						"INSERT INTO stocks(id,option_id,quantity) VALUES($1,$2,$3)",
						uuid.New(),
						oid,
						10,
					); e != nil {
						return e
					}
				}
			}
		}
	}

	return tx.Commit(ctx)
}

func (r *Repository) UpdateStocks(ctx context.Context, eid uuid.UUID, items []StockInput) error {
	for _, x := range items {
		var ok bool

		e := r.Pool.QueryRow(
			ctx,
			`SELECT
				EXISTS(
					SELECT 1
					FROM products
					WHERE id=$1 AND establishment_id=$2
				)
				OR EXISTS(
					SELECT 1
					FROM modifier_options o
					JOIN modifiers m ON m.id=o.modifier_id
					JOIN products p ON p.id=m.product_id
					WHERE o.id=$1 AND p.establishment_id=$2
				)`,
			x.ID,
			eid,
		).Scan(&ok)

		if e != nil {
			return e
		}

		if !ok {
			return fmt.Errorf("stock object does not belong to establishment")
		}

		var stockID uuid.UUID

		if x.Kind == "product" {
			e = r.Pool.QueryRow(
				ctx,
				"SELECT id FROM stocks WHERE product_id=$1",
				x.ID,
			).Scan(&stockID)

			if e == pgx.ErrNoRows {
				_, e = r.Pool.Exec(
					ctx,
					"INSERT INTO stocks(id,product_id,quantity) VALUES($1,$2,$3)",
					uuid.New(),
					x.ID,
					x.Quantity,
				)
			} else if e == nil {
				_, e = r.Pool.Exec(
					ctx,
					"UPDATE stocks SET quantity=$1 WHERE id=$2",
					x.Quantity,
					stockID,
				)
			}
		} else {
			e = r.Pool.QueryRow(
				ctx,
				"SELECT id FROM stocks WHERE option_id=$1",
				x.ID,
			).Scan(&stockID)

			if e == pgx.ErrNoRows {
				_, e = r.Pool.Exec(
					ctx,
					"INSERT INTO stocks(id,option_id,quantity) VALUES($1,$2,$3)",
					uuid.New(),
					x.ID,
					x.Quantity,
				)
			} else if e == nil {
				_, e = r.Pool.Exec(
					ctx,
					"UPDATE stocks SET quantity=$1 WHERE id=$2",
					x.Quantity,
					stockID,
				)
			}
		}

		if e != nil {
			return e
		}
	}

	return nil
}

type StockInput struct {
	ID       uuid.UUID `json:"id" validate:"required"`
	Kind     string    `json:"kind" validate:"oneof=product option"`
	Quantity int       `json:"quantity" validate:"gte=0"`
}

func (r *Repository) Tx(ctx context.Context) (pgx.Tx, error) {
	return r.Pool.Begin(ctx)
}
