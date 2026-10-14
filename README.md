# go-usagequota

A Go library for usage limits that remain consistent under concurrent requests and retries.

Use `quota.Month`, `quota.Day`, or a custom `quota.Period` to scope a budget to an account and resource.

## Choose how to count usage

| Need | Use | How it counts |
| --- | --- | --- |
| Every attempt counts, even if the work fails. | `Consume` | Calculates a count; your code saves it. Retries count again. |
| Usage can be finalized now. | `Spend` | Records committed usage once per request ID. |
| Work starts now and finishes later. | `Reserve` → `Commit` | Holds capacity while work is pending, then finalizes it. |

`Consume` does not read or save a counter and has no request ID. Read the counter, call `Consume`, and save the result atomically before starting work.

`Spend` checks the limit and commits usage in one store update, without a pending reservation. `Reserve` followed by `Commit` reaches the same final count in two updates. On retry, use the same request ID and amount: `Created=false` means the charge committed; a rolled-back charge can be created again. A different amount returns `ErrConflict`.

To save business data and its charge together, use the same database transaction for both. With GORM and the [PostgreSQL example store](examples/postgresstore/store.go):

```go
err := db.Transaction(func(tx *gorm.DB) error {
	limiter := quota.New(postgresstore.New(tx))
	result, err := limiter.Spend(ctx, key, requestID, amount, limit)
	if err != nil {
		return err
	}
	if !result.Created {
		return nil // The earlier transaction committed both the charge and the work.
	}
	return tx.Create(&work).Error
})
```

An error rolls back both writes. The PostgreSQL store is a [separate module](examples/postgresstore/go.mod) and uses a savepoint, so leave GORM's nested transactions enabled. See the [integration test](examples/postgresstore/store_integration_test.go).

For work that finishes later, call `Reserve` before starting, `Commit` after success, and `Cancel` only when you know the work did not run. Pending and committed usage both count toward the limit. Reusing a request ID does not reserve twice; `Commit` requires an earlier `Reserve` and can be retried safely.

## Failures and retries

The limiter does not retry operations or recover application work. Stores must save usage and reservation changes atomically; a rejected update leaves both unchanged.

| Operation | On a temporary storage error |
| --- | --- |
| `Reserve` | Do not start work. Retry with the same key, request ID, and amount. |
| `Spend` | Retry the whole transaction with the same identity. An earlier commit returns `Created=false`. |
| `Commit` | If work succeeded, retry `Commit` only; the pending amount still counts. |
| `Cancel` | Retry only when you know the work did not run; the pending amount still counts. |

A storage error does not prove a rollback: the transaction may have committed before the connection dropped. Keep the original key, request ID, and amount for retries.

### Recovering a reservation

Start new work only when `Reserve` returns `Created=true`. A retry with `Created=false` returns an existing reservation; it does not tell you whether the work ran. Track job progress in your application to recover after a crash or uncertain response.

Keep the original key, including its period. `Commit` and `Cancel` work after the period ends; `Reserve` can replay a pending or committed reservation but cannot create a new one outside the period.

Pending reservations do not expire and keep counting against their original period; reconcile them in your application. `Cancel` releases pending usage, but cancelled IDs stay closed. Committed usage cannot be refunded, and reservation records are not deleted automatically.

### Errors that need a decision

Use `errors.Is` to inspect errors, including wrapped store errors. These errors need a change in input, quota, or state before a retry can succeed:

- `ErrInvalid`: fix the arguments.
- `ErrExceeded`: the requested amount does not fit within the limit. Wait for capacity or a limit change before trying again.
- `ErrOutsidePeriod`: the original period is not active for a new charge or reservation.
- `ErrConflict`: the request ID already exists with a different amount.
- `ErrNotFound`: `Commit` or `Cancel` has no matching reservation.
- `ErrFinalized`: the requested operation conflicts with a committed or cancelled state.
- `ErrCorrupt`: stored counters or reservation state are invalid and need investigation.

## Stores

`quota.Store` is the public storage contract. An implementation must update usage and a reservation atomically. The repository includes a [PostgreSQL example](examples/postgresstore/store.go) and an internal memory store for tests and the demo. The library module has no database driver dependency.

The PostgreSQL example stores counters in `quota_budgets` and individual reservations in `quota_reservations`. Each update locks the budget and reads and writes only the requested reservation, within one transaction. Use short subject/resource and request IDs because they are indexed directly.

Earlier examples used a different storage layout. Migrate their data before switching; `Init` does not convert existing reservations.

## Get started

Requires Go 1.25 or later.

```sh
go get github.com/jeorp/go-usagequota@latest
```

See the [runnable reservation example](examples/reservation/main.go) for `Reserve`, `Commit`, and `Cancel`.

## Check the project

```sh
make test
make vet
make lint
make postgres-up
make integration
make postgres-down
```
