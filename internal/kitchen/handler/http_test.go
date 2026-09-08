package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/example/avito-kitchen/internal/kitchen/client"
	"github.com/example/avito-kitchen/internal/kitchen/repository"
	"github.com/example/avito-kitchen/internal/kitchen/service"
	"github.com/google/uuid"
)

const testDatabaseURL = "postgres://kitchen:kitchen@localhost:5433/kitchen?sslmode=disable"

func testAPI(t *testing.T) *API {
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

	orders := service.NewOrders(repo, client.NewWebhook())

	return New(repo, orders, nil)
}

func cleanDatabase(t *testing.T, api *API) {
	t.Helper()

	_, err := api.r.Pool.Exec(
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

func prepareTestProduct(t *testing.T, api *API) (uuid.UUID, uuid.UUID) {
	t.Helper()

	ctx := context.Background()

	establishmentID := uuid.MustParse(
		"11111111-1111-1111-1111-111111111111",
	)
	categoryID := uuid.New()
	productID := uuid.New()

	_, err := api.r.Pool.Exec(
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

	_, err = api.r.Pool.Exec(
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

	_, err = api.r.Pool.Exec(
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

func TestHealth(t *testing.T) {
	api := testAPI(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected response: %s", rec.Body.String())
	}
}

func TestGetEstablishments(t *testing.T) {
	api := testAPI(t)
	cleanDatabase(t, api)

	establishmentID := uuid.MustParse(
		"11111111-1111-1111-1111-111111111111",
	)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/establishments",
		nil,
	)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var establishments []struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}

	if err := json.NewDecoder(rec.Body).Decode(&establishments); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(establishments) != 1 {
		t.Fatalf("expected 1 establishment, got %d", len(establishments))
	}

	if establishments[0].ID != establishmentID {
		t.Fatalf(
			"expected establishment %s, got %s",
			establishmentID,
			establishments[0].ID,
		)
	}

	if establishments[0].Name != "Demo Bistro" {
		t.Fatalf(
			"expected Demo Bistro, got %q",
			establishments[0].Name,
		)
	}
}

func TestGetEstablishment(t *testing.T) {
	api := testAPI(t)
	cleanDatabase(t, api)

	id := "11111111-1111-1111-1111-111111111111"

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/establishments/"+id,
		nil,
	)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var establishment struct {
		ID             uuid.UUID `json:"id"`
		Name           string    `json:"name"`
		Description    string    `json:"description"`
		MinOrderAmount int64     `json:"min_order_amount"`
	}

	if err := json.NewDecoder(rec.Body).Decode(&establishment); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if establishment.Name != "Demo Bistro" {
		t.Fatalf("expected Demo Bistro, got %q", establishment.Name)
	}

	if establishment.MinOrderAmount != 50000 {
		t.Fatalf(
			"expected minimum order 50000, got %d",
			establishment.MinOrderAmount,
		)
	}
}

func TestGetEstablishmentNotFound(t *testing.T) {
	api := testAPI(t)
	cleanDatabase(t, api)

	id := uuid.NewString()

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/establishments/"+id,
		nil,
	)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	var response map[string]string

	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if response["code"] != "not_found" {
		t.Fatalf(
			"expected code not_found, got %q",
			response["code"],
		)
	}
}

func TestGetMenu(t *testing.T) {
	api := testAPI(t)
	cleanDatabase(t, api)

	establishmentID, productID := prepareTestProduct(t, api)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/establishments/"+establishmentID.String()+"/menu",
		nil,
	)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var menu struct {
		Categories []struct {
			Name     string `json:"name"`
			Products []struct {
				ID          uuid.UUID `json:"id"`
				Name        string    `json:"name"`
				Price       int64     `json:"price"`
				IsAvailable bool      `json:"is_available"`
			} `json:"products"`
		} `json:"categories"`
	}

	if err := json.NewDecoder(rec.Body).Decode(&menu); err != nil {
		t.Fatalf("decode menu: %v", err)
	}

	if len(menu.Categories) != 1 {
		t.Fatalf(
			"expected 1 category, got %d",
			len(menu.Categories),
		)
	}

	if len(menu.Categories[0].Products) != 1 {
		t.Fatalf(
			"expected 1 product, got %d",
			len(menu.Categories[0].Products),
		)
	}

	product := menu.Categories[0].Products[0]

	if product.ID != productID {
		t.Fatalf(
			"expected product %s, got %s",
			productID,
			product.ID,
		)
	}

	if product.Name != "Margherita" {
		t.Fatalf(
			"expected Margherita, got %q",
			product.Name,
		)
	}

	if product.Price != 60000 {
		t.Fatalf(
			"expected price 60000, got %d",
			product.Price,
		)
	}
}

func TestCreateOrderHTTP(t *testing.T) {
	api := testAPI(t)
	cleanDatabase(t, api)

	establishmentID, productID := prepareTestProduct(t, api)
	userID := uuid.New()

	body := `{
		"establishment_id": "` + establishmentID.String() + `",
		"items": [
			{
				"product_id": "` + productID.String() + `",
				"quantity": 2
			}
		]
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/orders",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())

	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf(
			"expected 201, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	var order struct {
		ID              uuid.UUID `json:"id"`
		EstablishmentID uuid.UUID `json:"establishment_id"`
		UserID          uuid.UUID `json:"user_id"`
		Status          string    `json:"status"`
		TotalAmount     int64     `json:"total_amount"`
	}

	if err := json.NewDecoder(rec.Body).Decode(&order); err != nil {
		t.Fatalf("decode order: %v", err)
	}

	if order.ID == uuid.Nil {
		t.Fatal("order ID is empty")
	}

	if order.EstablishmentID != establishmentID {
		t.Fatalf("unexpected establishment ID")
	}

	if order.UserID != userID {
		t.Fatalf("unexpected user ID")
	}

	if order.Status != "created" {
		t.Fatalf(
			"expected created, got %q",
			order.Status,
		)
	}

	if order.TotalAmount != 120000 {
		t.Fatalf(
			"expected total 120000, got %d",
			order.TotalAmount,
		)
	}
}

func TestCreateOrderInvalidUser(t *testing.T) {
	api := testAPI(t)
	cleanDatabase(t, api)

	body := `{
		"establishment_id": "11111111-1111-1111-1111-111111111111",
		"items": []
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/orders",
		strings.NewReader(body),
	)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "not-a-uuid")

	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected 400, got %d",
			rec.Code,
		)
	}

	var response map[string]string

	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if response["code"] != "invalid_user" {
		t.Fatalf(
			"expected invalid_user, got %q",
			response["code"],
		)
	}
}

func TestCreateOrderValidationError(t *testing.T) {
	api := testAPI(t)
	cleanDatabase(t, api)

	userID := uuid.New()

	body := `{
		"establishment_id": "11111111-1111-1111-1111-111111111111",
		"items": []
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/orders",
		strings.NewReader(body),
	)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())

	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"expected 400, got %d",
			rec.Code,
		)
	}
}

func TestPartnerUnauthorized(t *testing.T) {
	api := testAPI(t)
	cleanDatabase(t, api)

	req := httptest.NewRequest(
		http.MethodGet,
		"/partner/orders",
		nil,
	)

	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf(
			"expected 401, got %d",
			rec.Code,
		)
	}

	var response map[string]string

	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if response["code"] != "unauthorized" {
		t.Fatalf(
			"expected unauthorized, got %q",
			response["code"],
		)
	}
}

func TestPartnerOrders(t *testing.T) {
	api := testAPI(t)
	cleanDatabase(t, api)

	establishmentID, productID := prepareTestProduct(t, api)

	userID := uuid.New()

	body := `{
		"establishment_id": "` + establishmentID.String() + `",
		"items": [
			{
				"product_id": "` + productID.String() + `",
				"quantity": 1
			}
		]
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/orders",
		strings.NewReader(body),
	)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())

	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf(
			"create order: expected 201, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	req = httptest.NewRequest(
		http.MethodGet,
		"/partner/orders",
		nil,
	)

	req.Header.Set("X-API-Key", "demo-api-key")

	rec = httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"partner orders: expected 200, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	var orders []struct {
		ID              uuid.UUID `json:"id"`
		EstablishmentID uuid.UUID `json:"establishment_id"`
		UserID          uuid.UUID `json:"user_id"`
		Status          string    `json:"status"`
		TotalAmount     int64     `json:"total_amount"`
	}

	if err := json.NewDecoder(rec.Body).Decode(&orders); err != nil {
		t.Fatalf("decode partner orders: %v", err)
	}

	if len(orders) != 1 {
		t.Fatalf(
			"expected 1 order, got %d",
			len(orders),
		)
	}

	if orders[0].Status != "created" {
		t.Fatalf(
			"expected created, got %q",
			orders[0].Status,
		)
	}
}

func TestCancelOrderHTTP(t *testing.T) {
	api := testAPI(t)
	cleanDatabase(t, api)

	establishmentID, productID := prepareTestProduct(t, api)

	userID := uuid.New()

	body := `{
		"establishment_id": "` + establishmentID.String() + `",
		"items": [
			{
				"product_id": "` + productID.String() + `",
				"quantity": 1
			}
		]
	}`

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/orders",
		strings.NewReader(body),
	)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID.String())

	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf(
			"create order: expected 201, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	var created struct {
		ID uuid.UUID `json:"id"`
	}

	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created order: %v", err)
	}

	cancelBody := `{
		"reason": "customer changed their mind"
	}`

	req = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/orders/"+created.ID.String()+"/cancel",
		strings.NewReader(cancelBody),
	)

	rec = httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf(
			"cancel order: expected 200, got %d: %s",
			rec.Code,
			rec.Body.String(),
		)
	}

	var status struct {
		Status string `json:"status"`
	}

	if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
		t.Fatalf("decode cancellation response: %v", err)
	}

	if status.Status != "cancelled" {
		t.Fatalf(
			"expected cancelled, got %q",
			status.Status,
		)
	}
}
