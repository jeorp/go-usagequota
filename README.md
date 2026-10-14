# go-usagequota

A Go library for usage limits that remain consistent under concurrent requests and retries.

Use `quota.Month`, `quota.Day`, or a custom `quota.Period` to scope a budget to an account and resource.

## Choose how to count usage

Use `Consume` when **each attempt counts**, even if the work later fails. For example, count each time a user starts an API request. `Consume` calculates the new count; your code must read and save the counter in one transaction. Retrying a completed request counts again.

Use `Reserve` → `Commit` when work has separate start and completion steps:

1. Call `Reserve` before starting the work.
2. Call `Commit` after the work succeeds.
3. Call `Cancel` only if you are sure the work did not run.

Both reserved and completed work count toward the limit. Use the same request ID when retrying: `Reserve` will not create a second reservation, and calling `Commit` again is safe. `Commit` requires an earlier `Reserve`.

You can use either approach with a daily, monthly, or custom period.

## Stores

`quota.Store` is the public storage contract. An implementation must update usage and a reservation atomically. The repository includes a [PostgreSQL implementation example](examples/postgresstore/store.go) in a separate module and an internal memory store for tests and the demo. The library module has no database driver dependency.

## Get started

Requires Go 1.24 or later.

```sh
go get github.com/jeorp/go-usagequota@latest
```

See [the runnable reservation example](examples/reservation/main.go) for `Reserve`, `Commit`, and `Cancel`. `Created` indicates a new reservation; the caller remains responsible for running and recovering the job. Cancel only when work definitely did not run.

Keep the original budget key and reservation records for retries. The library does not expire reservations or delete old records automatically.

## Check the project

```sh
make test
make vet
make lint
make postgres-up
make integration
make postgres-down
```
