package service

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/example/avito-kitchen/internal/kitchen/domain"

	"github.com/example/avito-kitchen/internal/kitchen/client"
	"github.com/example/avito-kitchen/internal/kitchen/repository"
	"github.com/google/uuid"
)

const testDatabaseURL = "postgres://kitchen:kitchen@localhost:5433/kitchen?sslmode=disable"

func testRepository(t *testing.T) *repository.Repository {
	t.Helper()

	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = testDatabaseURL
	}

	repo, err := repository.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect database: %v", err)
	}

	t.Cleanup(func() {
		repo.Pool.Close()
	})

	if err := repo.Migrate(ctx); err != nil {
		t.Fatalf("migrate database: %v", err)
	}

	return repo
}

func cleanDatabase(t *testing.T, repo *repository.Repository) {
	t.Helper()

	_, err := repo.Pool.Exec(
		context.Background(),
		`TRUNCATE
			order_status_history,
			order_item_options,
			order_items,
			orders,
			stocks,
			modifier_options,
			modifiers,
			products,
			categories
			RESTART IDENTITY CASCADE`,
	)
	if err != nil {
		t.Fatalf("clean database: %v", err)
	}
}

func prepareProduct(t *testing.T, repo *repository.Repository) (
	establishmentID uuid.UUID,
	productID uuid.UUID,
) {
	t.Helper()

	ctx := context.Background()

	establishmentID = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	categoryID := uuid.New()
	productID = uuid.New()

	_, err := repo.Pool.Exec(
		ctx,
		`INSERT INTO categories(id, establishment_id, name, position)
		 VALUES($1, $2, $3, $4)`,
		categoryID,
		establishmentID,
		"Pizza",
		1,
	)
	if err != nil {
		t.Fatalf("insert category: %v", err)
	}

	_, err = repo.Pool.Exec(
		ctx,
		`INSERT INTO products(
			id,
			establishment_id,
			category_id,
			name,
			description,
			price,
			is_available
		)
		VALUES($1, $2, $3, $4, $5, $6, $7)`,
		productID,
		establishmentID,
		categoryID,
		"Margherita",
		"Classic pizza",
		60000,
		true,
	)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}

	_, err = repo.Pool.Exec(
		ctx,
		`INSERT INTO stocks(id, product_id, quantity)
		 VALUES($1, $2, $3)`,
		uuid.New(),
		productID,
		10,
	)
	if err != nil {
		t.Fatalf("insert stock: %v", err)
	}

	return establishmentID, productID
}

func TestCreateOrder(t *testing.T) {
	repo := testRepository(t)
	cleanDatabase(t, repo)

	establishmentID, productID := prepareProduct(t, repo)

	orders := NewOrders(repo, client.NewWebhook())

	userID := uuid.New()

	order, err := orders.Create(
		context.Background(),
		userID,
		CreateOrder{
			EstablishmentID: establishmentID,
			Items: []CreateItem{
				{
					ProductID: productID,
					Quantity:  2,
				},
			},
		},
	)

	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	if order.ID == uuid.Nil {
		t.Fatal("order ID is empty")
	}

	if order.Status != "created" {
		t.Fatalf("expected status created, got %q", order.Status)
	}

	if order.UserID != userID {
		t.Fatalf("expected user ID %s, got %s", userID, order.UserID)
	}

	if order.EstablishmentID != establishmentID {
		t.Fatalf(
			"expected establishment ID %s, got %s",
			establishmentID,
			order.EstablishmentID,
		)
	}

	if order.TotalAmount != 120000 {
		t.Fatalf("expected total 120000, got %d", order.TotalAmount)
	}

	if len(order.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(order.Items))
	}

	item := order.Items[0]

	if item.ProductID != productID {
		t.Fatalf("expected product ID %s, got %s", productID, item.ProductID)
	}

	if item.Quantity != 2 {
		t.Fatalf("expected quantity 2, got %d", item.Quantity)
	}

	if item.UnitPrice != 60000 {
		t.Fatalf("expected unit price 60000, got %d", item.UnitPrice)
	}

	if item.TotalPrice != 120000 {
		t.Fatalf("expected item total 120000, got %d", item.TotalPrice)
	}

	// Проверяем orders.
	var status string
	var total int64

	err = repo.Pool.QueryRow(
		context.Background(),
		`SELECT status, total_amount
		 FROM orders
		 WHERE id = $1`,
		order.ID,
	).Scan(&status, &total)

	if err != nil {
		t.Fatalf("query order: %v", err)
	}

	if status != "created" {
		t.Fatalf("expected DB status created, got %q", status)
	}

	if total != 120000 {
		t.Fatalf("expected DB total 120000, got %d", total)
	}

	// Проверяем order_items.
	var quantity int
	var itemTotal int64

	err = repo.Pool.QueryRow(
		context.Background(),
		`SELECT quantity, total_price
		 FROM order_items
		 WHERE order_id = $1`,
		order.ID,
	).Scan(&quantity, &itemTotal)

	if err != nil {
		t.Fatalf("query order item: %v", err)
	}

	if quantity != 2 {
		t.Fatalf("expected DB quantity 2, got %d", quantity)
	}

	if itemTotal != 120000 {
		t.Fatalf("expected DB item total 120000, got %d", itemTotal)
	}

	// Проверяем историю статусов.
	var historyStatus string

	err = repo.Pool.QueryRow(
		context.Background(),
		`SELECT status
		 FROM order_status_history
		 WHERE order_id = $1`,
		order.ID,
	).Scan(&historyStatus)

	if err != nil {
		t.Fatalf("query order history: %v", err)
	}

	if historyStatus != "created" {
		t.Fatalf("expected history status created, got %q", historyStatus)
	}

	// Проверяем списание товара со склада.
	var stockQuantity int

	err = repo.Pool.QueryRow(
		context.Background(),
		`SELECT quantity
		 FROM stocks
		 WHERE product_id = $1`,
		productID,
	).Scan(&stockQuantity)

	if err != nil {
		t.Fatalf("query stock: %v", err)
	}

	if stockQuantity != 8 {
		t.Fatalf("expected stock 8, got %d", stockQuantity)
	}
}

func TestCreateOrderInsufficientStock(t *testing.T) {
	repo := testRepository(t)
	cleanDatabase(t, repo)

	establishmentID, productID := prepareProduct(t, repo)

	orders := NewOrders(repo, client.NewWebhook())

	_, err := orders.Create(
		context.Background(),
		uuid.New(),
		CreateOrder{
			EstablishmentID: establishmentID,
			Items: []CreateItem{
				{
					ProductID: productID,
					Quantity:  11,
				},
			},
		},
	)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, ErrRule) {
		t.Fatalf("expected ErrRule, got %v", err)
	}

	// Заказ не должен сохраниться.
	var count int

	err = repo.Pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM orders",
	).Scan(&count)

	if err != nil {
		t.Fatalf("count orders: %v", err)
	}

	if count != 0 {
		t.Fatalf("expected 0 orders after rollback, got %d", count)
	}

	// Остаток товара тоже должен остаться прежним.
	var stockQuantity int

	err = repo.Pool.QueryRow(
		context.Background(),
		`SELECT quantity
		 FROM stocks
		 WHERE product_id = $1`,
		productID,
	).Scan(&stockQuantity)

	if err != nil {
		t.Fatalf("query stock: %v", err)
	}

	if stockQuantity != 10 {
		t.Fatalf("expected stock 10 after rollback, got %d", stockQuantity)
	}
}

func TestCreateOrderProductUnavailable(t *testing.T) {
	repo := testRepository(t)
	cleanDatabase(t, repo)

	establishmentID, productID := prepareProduct(t, repo)

	_, err := repo.Pool.Exec(
		context.Background(),
		"UPDATE products SET is_available = false WHERE id = $1",
		productID,
	)
	if err != nil {
		t.Fatalf("disable product: %v", err)
	}

	orders := NewOrders(repo, client.NewWebhook())

	_, err = orders.Create(
		context.Background(),
		uuid.New(),
		CreateOrder{
			EstablishmentID: establishmentID,
			Items: []CreateItem{
				{
					ProductID: productID,
					Quantity:  1,
				},
			},
		},
	)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !errors.Is(err, ErrRule) {
		t.Fatalf("expected ErrRule, got %v", err)
	}
}

func TestCreateOrderBelowMinimumAmount(t *testing.T) {
	repo := testRepository(t)
	cleanDatabase(t, repo)

	establishmentID, productID := prepareProduct(t, repo)

	orders := NewOrders(repo, client.NewWebhook())

	// Demo Bistro требует минимум 50000.
	// Товар стоит 60000, поэтому создаваемый заказ проходит.
	// Проверим ниже лимита отдельным продуктом.
	_, err := repo.Pool.Exec(
		context.Background(),
		"UPDATE products SET price = 40000 WHERE id = $1",
		productID,
	)
	if err != nil {
		t.Fatalf("update product price: %v", err)
	}

	_, err = orders.Create(
		context.Background(),
		uuid.New(),
		CreateOrder{
			EstablishmentID: establishmentID,
			Items: []CreateItem{
				{
					ProductID: productID,
					Quantity:  1,
				},
			},
		},
	)

	if err == nil {
		t.Fatal("expected minimum order error, got nil")
	}

	if !errors.Is(err, ErrRule) {
		t.Fatalf("expected ErrRule, got %v", err)
	}
}

func TestChangeOrderStatus(t *testing.T) {
	repo := testRepository(t)
	cleanDatabase(t, repo)

	establishmentID, productID := prepareProduct(t, repo)

	orders := NewOrders(repo, client.NewWebhook())

	order, err := orders.Create(
		context.Background(),
		uuid.New(),
		CreateOrder{
			EstablishmentID: establishmentID,
			Items: []CreateItem{
				{
					ProductID: productID,
					Quantity:  1,
				},
			},
		},
	)

	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	err = orders.Change(
		context.Background(),
		order.ID,
		nil,
		"confirmed",
		"",
	)

	if err != nil {
		t.Fatalf("change status: %v", err)
	}

	var status string

	err = repo.Pool.QueryRow(
		context.Background(),
		"SELECT status FROM orders WHERE id = $1",
		order.ID,
	).Scan(&status)

	if err != nil {
		t.Fatalf("query status: %v", err)
	}

	if status != "confirmed" {
		t.Fatalf("expected confirmed, got %q", status)
	}

	var historyCount int

	err = repo.Pool.QueryRow(
		context.Background(),
		`SELECT count(*)
		 FROM order_status_history
		 WHERE order_id = $1`,
		order.ID,
	).Scan(&historyCount)

	if err != nil {
		t.Fatalf("query history: %v", err)
	}

	if historyCount != 2 {
		t.Fatalf("expected 2 history records, got %d", historyCount)
	}
}

func TestChangeOrderInvalidTransition(t *testing.T) {
	repo := testRepository(t)
	cleanDatabase(t, repo)

	establishmentID, productID := prepareProduct(t, repo)

	orders := NewOrders(repo, client.NewWebhook())

	order, err := orders.Create(
		context.Background(),
		uuid.New(),
		CreateOrder{
			EstablishmentID: establishmentID,
			Items: []CreateItem{
				{
					ProductID: productID,
					Quantity:  1,
				},
			},
		},
	)

	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	err = orders.Change(
		context.Background(),
		order.ID,
		nil,
		"delivered",
		"",
	)

	if err == nil {
		t.Fatal("expected transition error, got nil")
	}

	if !errors.Is(err, ErrRule) {
		t.Fatalf("expected ErrRule, got %v", err)
	}

	var status string

	err = repo.Pool.QueryRow(
		context.Background(),
		"SELECT status FROM orders WHERE id = $1",
		order.ID,
	).Scan(&status)

	if err != nil {
		t.Fatalf("query status: %v", err)
	}

	if status != "created" {
		t.Fatalf("expected status to remain created, got %q", status)
	}
}

func TestCancelOrderRequiresReason(t *testing.T) {
	repo := testRepository(t)
	cleanDatabase(t, repo)

	establishmentID, productID := prepareProduct(t, repo)

	orders := NewOrders(repo, client.NewWebhook())

	order, err := orders.Create(
		context.Background(),
		uuid.New(),
		CreateOrder{
			EstablishmentID: establishmentID,
			Items: []CreateItem{
				{
					ProductID: productID,
					Quantity:  1,
				},
			},
		},
	)

	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	err = orders.Change(
		context.Background(),
		order.ID,
		nil,
		"cancelled",
		"",
	)

	if err == nil {
		t.Fatal("expected cancellation reason error, got nil")
	}

	if !errors.Is(err, ErrRule) {
		t.Fatalf("expected ErrRule, got %v", err)
	}
}

func TestCancelOrder(t *testing.T) {
	repo := testRepository(t)
	cleanDatabase(t, repo)

	establishmentID, productID := prepareProduct(t, repo)

	orders := NewOrders(repo, client.NewWebhook())

	order, err := orders.Create(
		context.Background(),
		uuid.New(),
		CreateOrder{
			EstablishmentID: establishmentID,
			Items: []CreateItem{
				{
					ProductID: productID,
					Quantity:  1,
				},
			},
		},
	)

	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	const reason = "customer changed their mind"

	err = orders.Change(
		context.Background(),
		order.ID,
		nil,
		"cancelled",
		reason,
	)

	if err != nil {
		t.Fatalf("cancel order: %v", err)
	}

	var status string
	var cancellationReason *string

	err = repo.Pool.QueryRow(
		context.Background(),
		`SELECT status, cancellation_reason
		 FROM orders
		 WHERE id = $1`,
		order.ID,
	).Scan(&status, &cancellationReason)

	if err != nil {
		t.Fatalf("query cancelled order: %v", err)
	}

	if status != "cancelled" {
		t.Fatalf("expected cancelled, got %q", status)
	}

	if cancellationReason == nil {
		t.Fatal("expected cancellation reason, got nil")
	}

	if *cancellationReason != reason {
		t.Fatalf(
			"expected reason %q, got %q",
			reason,
			*cancellationReason,
		)
	}
}

func TestCreateOrderConcurrency(t *testing.T) {
	repo := testRepository(t)
	cleanDatabase(t, repo)

	ctx := context.Background()

	establishmentID := uuid.MustParse(
		"11111111-1111-1111-1111-111111111111",
	)

	categoryID := uuid.New()
	productID := uuid.New()

	_, err := repo.Pool.Exec(
		ctx,
		`INSERT INTO categories(id, establishment_id, name, position)
		 VALUES($1, $2, $3, $4)`,
		categoryID,
		establishmentID,
		"Concurrency Test",
		1,
	)
	if err != nil {
		t.Fatalf("insert category: %v", err)
	}

	_, err = repo.Pool.Exec(
		ctx,
		`INSERT INTO products (
		id,
		establishment_id,
		category_id,
		name,
		description,
		price,
		is_available
	) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		productID,
		establishmentID,
		categoryID,
		"Last Pizza",
		"Only one left",
		int64(60000),
		true,
	)
	if err != nil {
		t.Fatalf("insert product: %v", err)
	}

	// На складе ровно одна единица.
	_, err = repo.Pool.Exec(
		ctx,
		`INSERT INTO stocks(id, product_id, quantity)
		 VALUES($1, $2, 1)`,
		uuid.New(),
		productID,
	)
	if err != nil {
		t.Fatalf("insert stock: %v", err)
	}

	orders := NewOrders(repo, client.NewWebhook())

	type result struct {
		order domain.Order
		err   error
	}

	results := make(chan result, 2)

	start := make(chan struct{})

	create := func() {
		<-start

		order, err := orders.Create(
			context.Background(),
			uuid.New(),
			CreateOrder{
				EstablishmentID: establishmentID,
				Items: []CreateItem{
					{
						ProductID: productID,
						Quantity:  1,
					},
				},
			},
		)

		results <- result{
			order: order,
			err:   err,
		}
	}

	go create()
	go create()

	// Одновременно запускаем оба заказа.
	close(start)

	var successful int
	var failed int

	for range 2 {
		result := <-results

		if result.err == nil {
			successful++
			continue
		}

		if errors.Is(result.err, ErrRule) {
			failed++
			continue
		}

		t.Fatalf("unexpected error: %v", result.err)
	}

	if successful != 1 {
		t.Fatalf(
			"expected exactly 1 successful order, got %d",
			successful,
		)
	}

	if failed != 1 {
		t.Fatalf(
			"expected exactly 1 failed order, got %d",
			failed,
		)
	}

	// В БД должен существовать только один заказ.
	var orderCount int

	err = repo.Pool.QueryRow(
		ctx,
		`SELECT count(*)
		 FROM orders
		 WHERE establishment_id = $1`,
		establishmentID,
	).Scan(&orderCount)

	if err != nil {
		t.Fatalf("count orders: %v", err)
	}

	if orderCount != 1 {
		t.Fatalf(
			"expected exactly 1 order in database, got %d",
			orderCount,
		)
	}

	// Остаток должен стать ровно 0, а не -1.
	var stockQuantity int

	err = repo.Pool.QueryRow(
		ctx,
		`SELECT quantity
		 FROM stocks
		 WHERE product_id = $1`,
		productID,
	).Scan(&stockQuantity)

	if err != nil {
		t.Fatalf("query stock: %v", err)
	}

	if stockQuantity != 0 {
		t.Fatalf(
			"expected stock 0, got %d",
			stockQuantity,
		)
	}
}
