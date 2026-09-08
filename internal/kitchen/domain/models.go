package domain

import "github.com/google/uuid"

type Establishment struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	MinOrderAmount int64     `json:"min_order_amount"`
	WebhookURL     string    `json:"-"`
}
type Menu struct {
	Categories []Category `json:"categories"`
}
type Category struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Position int       `json:"position"`
	Products []Product `json:"products"`
}
type Product struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Price       int64      `json:"price"`
	IsAvailable bool       `json:"is_available"`
	Modifiers   []Modifier `json:"modifiers"`
}
type Modifier struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	IsRequired bool      `json:"is_required"`
	Options    []Option  `json:"options"`
}
type Option struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	PriceDelta  int64     `json:"price_delta"`
	IsAvailable bool      `json:"is_available"`
}
type Order struct {
	ID                 uuid.UUID       `json:"id"`
	EstablishmentID    uuid.UUID       `json:"establishment_id"`
	UserID             uuid.UUID       `json:"user_id"`
	Status             string          `json:"status"`
	TotalAmount        int64           `json:"total_amount"`
	CancellationReason *string         `json:"cancellation_reason,omitempty"`
	Items              []OrderItem     `json:"items"`
	History            []StatusHistory `json:"history"`
}
type OrderItem struct {
	ID          uuid.UUID         `json:"id"`
	ProductID   uuid.UUID         `json:"product_id"`
	ProductName string            `json:"product_name"`
	UnitPrice   int64             `json:"unit_price"`
	Quantity    int               `json:"quantity"`
	TotalPrice  int64             `json:"total_price"`
	Options     []OrderItemOption `json:"options"`
}
type OrderItemOption struct {
	OptionID   uuid.UUID `json:"option_id"`
	OptionName string    `json:"option_name"`
	PriceDelta int64     `json:"price_delta"`
}
type StatusHistory struct {
	Status    string  `json:"status"`
	Reason    *string `json:"reason,omitempty"`
	CreatedAt string  `json:"created_at"`
}
