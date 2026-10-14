.PHONY: test test-core vet demo fuzz integration emulator-up emulator-down fmt

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

emulator-up:
	docker compose up --build --wait firestore

emulator-down:
	docker compose down

integration:
	FIRESTORE_EMULATOR_HOST=127.0.0.1:$${QUOTA_EMULATOR_PORT:-8086} go test -race -tags=integration -timeout=3m ./quota/firestorestore

fmt:
	gofmt -w quota internal examples
