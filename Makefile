.PHONY: test test-core vet lint demo fuzz integration postgres-up postgres-down fmt

test:
	go test -race ./...
	cd examples/postgresstore && go test -race ./...

test-core:
	go test -race ./quota ./internal/memory

vet:
	go vet ./...
	cd examples/postgresstore && go vet ./...

lint:
	golangci-lint run ./...
	cd examples/postgresstore && golangci-lint run ./...

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
	cd examples/postgresstore && POSTGRES_TEST_DSN=postgres://quota:quota@127.0.0.1:$${QUOTA_POSTGRES_PORT:-55432}/quota?sslmode=disable go test -race -tags=integration -timeout=3m ./...

fmt:
	gofmt -w quota internal examples
