# Авито.Кухня MVP

Monorepo with `kitchen-api` (Go/chi, PostgreSQL) and an in-memory `restaurant-example`. Money is always integer kopecks; IDs are UUIDs.

## Architecture and data
The [C4 diagram](docs/c4-containers.puml) shows the three containers. The API is layered as `domain -> service -> repository -> handler`. PostgreSQL schema migration creates establishments, menu/catalog tables, stocks, orders, immutable order snapshots, and status history. Catalog synchronization replaces a restaurant's menu atomically.

## Start
```bash
docker-compose up --build
curl http://localhost:8080/health
curl http://localhost:8080/api/v1/establishments
curl http://localhost:8080/api/v1/establishments/11111111-1111-1111-1111-111111111111/menu
```
The demo restaurant synchronizes a Burger menu on startup. Use its menu response to get generated product/option IDs, then create an order:
```bash
curl -X POST localhost:8080/api/v1/orders -H 'Content-Type: application/json' -H 'X-User-Id: 22222222-2222-2222-2222-222222222222' -d '{"establishment_id":"11111111-1111-1111-1111-111111111111","items":[{"product_id":"PRODUCT_UUID","quantity":1,"option_ids":["OPTION_UUID"]}]}'
curl -X POST localhost:8080/api/v1/orders/ORDER_UUID/cancel -H 'Content-Type: application/json' -d '{"reason":"changed my mind"}'
```
Partner API uses `X-API-Key: demo-api-key`. Restaurant demo endpoints are `GET /menu`, `POST /sync`, `GET /orders`; webhook is `/webhook/order`.

## MVP simplifications
* No real user authentication: `X-User-Id` is a UUID header.
* No payments, couriers, geolocation, delivery time slots, or multiple cities.
* Webhooks are synchronous and intentionally have no retry/outbox.
* `restaurant-example` keeps its menu and received orders in memory.
* No rate limiting or distributed tracing.
* SQL is configured for sqlc in `sqlc.yaml`; migrations are goose-compatible SQL and are applied on API startup for a self-contained demo.

## Diagrams
See [user CJM](docs/cjm-user.puml), [restaurant CJM](docs/cjm-restaurant.puml), and [C4 containers](docs/c4-containers.puml).
