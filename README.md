# go-usagequota

A Go library for usage limits that remain consistent under concurrent requests and retries.

## Use cases

- **Reserve before work:** Hold capacity for a job, commit it after success, or cancel it when the job definitely did not run. Reusing a request ID does not charge twice.
- **Charge each attempt:** Use `quota.Consume` inside an existing database transaction when an attempt counts even if later work fails.
- **Set time-based limits:** Use `quota.Month`, `quota.Day`, or a custom `quota.Period` to scope a budget to an account and resource.

## Stores

`quota.Store` is the public storage contract. An implementation must update usage and a reservation atomically. The repository includes a [PostgreSQL implementation example](examples/postgresstore/store.go) in a separate module and an internal memory store for tests and the demo. The library module has no database driver dependency.

## Get started

Requires Go 1.24 or later.

```sh
go get github.com/jeorp/go-usagequota@latest
```

See [the runnable reservation example](examples/reservation/main.go) for `Reserve`, `Commit`, and `Cancel`. `Created` indicates a new reservation; the caller remains responsible for running and recovering the job. Cancel only when work definitely did not run.

## Check the project

```sh
make test
make vet
make postgres-up
make integration
make postgres-down
```

See [design notes](docs/design.md) for guarantees and storage limits.
