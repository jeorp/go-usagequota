.PHONY: test test-core vet demo fuzz integration postgres-up postgres-down fmt

test:
	go test -race ./...

test-core:
	go test -race ./quota ./quota/memory

vet:
	go vet ./...

demo:
	go run ./examples/reservation

fuzz:
	go test ./quota -run '^$$' -fuzz FuzzAccounting -fuzztime=10s
	go test ./quota -run '^$$' -fuzz FuzzConsume -fuzztime=10s

postgres-up:
	docker compose up --wait postgres

postgres-down:
	docker compose down

integration:
	POSTGRES_TEST_DSN=postgres://quota:quota@127.0.0.1:$${QUOTA_POSTGRES_PORT:-55432}/quota?sslmode=disable go test -race -tags=integration -timeout=3m ./quota/postgresstore

fmt:
	gofmt -w quota internal examples
