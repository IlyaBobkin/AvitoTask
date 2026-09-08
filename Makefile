.PHONY: run test lint sqlc
run: ; go run ./cmd/kitchen-api
 test: ; go test ./...
lint: ; golangci-lint run
sqlc: ; sqlc generate
