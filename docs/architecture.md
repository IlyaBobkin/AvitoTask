# Architecture
`kitchen-api` follows Clean Architecture: HTTP handlers validate/translate requests, services contain order rules, repositories own PostgreSQL access, and domain contains transport-independent models. Partner requests are scoped by the establishment resolved from `X-API-Key`.

The order transaction checks availability and stock, writes immutable item/option snapshots, decrements stocks, and appends history. Webhook delivery happens after commit.
