-- name: ListEstablishments :many
SELECT id, name, description, min_order_amount FROM establishments WHERE is_active = true ORDER BY name;
