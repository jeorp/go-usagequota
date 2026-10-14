# go-usagequota

A Go library for usage limits that remain consistent under concurrent requests and retries.

Use `quota.Month`, `quota.Day`, or a custom `quota.Period` to scope a budget to an account and resource.

## Consume vs. Reserve and Commit

`Commit` completes an earlier `Reserve`; it is not a standalone way to charge usage.

| | `Consume` | `Reserve` → `Commit` |
| --- | --- | --- |
| Use when | Every attempt counts, even if later work fails | Only completed work should count, but work in progress must hold capacity |
| Flow | Calculate the next counter value and save it in the caller's transaction | Reserve before work, commit after confirmed success, or cancel if work definitely did not run |
| Retries | No built-in deduplication | The same request ID does not reserve twice; repeating `Commit` is safe |

For example, use `Consume` for a daily draft-generation attempt limit and `Reserve` → `Commit` for a monthly completed-review limit.
Pending reservations and committed usage both count against the limit.

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
make postgres-up
make integration
make postgres-down
```
