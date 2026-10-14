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

## Failures and retries

The limiter returns errors to the caller. It does not automatically retry operations, cancel reservations, or recover application work. A store must save usage and reservation changes atomically; a rejected update must leave both unchanged.

| Operation | If the update is not saved | How to recover from a temporary storage error |
| --- | --- | --- |
| `Reserve` | No new reservation or usage is recorded. | Do not start work. Retry with the same key, request ID, and amount. |
| `Commit` | An existing pending reservation stays pending and still counts toward the limit. | If the work succeeded, retry only `Commit`, not the work. Committing an already committed reservation succeeds without counting it again. |
| `Cancel` | An existing pending reservation stays pending and still counts toward the limit. | Retry `Cancel` only when you know the work did not run. Cancelling an already cancelled reservation succeeds. |

A storage error does not always mean the update was rolled back. For example, PostgreSQL may commit a transaction before the connection drops, leaving the caller without confirmation. Retry using the original identity rather than creating a new request ID.

### Resuming a request

- Start new work only after `Reserve` succeeds with `Created=true`.
- A successful retry with `Created=false` returns an existing pending or committed reservation. It does not grant permission to start work again or tell you whether the work actually ran.
- Track job ownership and progress in your application so you can recover after a crash or an uncertain response. Quota reservations do not guarantee that application work runs exactly once.
- If the work succeeded but `Commit` failed, keep retrying `Commit` on temporary storage errors. Do not cancel the reservation to handle a commit failure.

Always keep the original key, including its period, for retries. `Commit` and `Cancel` still work after that period ends. `Reserve` can return an existing pending or committed reservation after the period ends, but cannot create a new one outside the period.

Pending reservations do not expire automatically. They continue to count against their original period until committed or cancelled; committing keeps the units counted, while cancelling releases them. Your application must reconcile unfinished requests. Cancelled request IDs stay closed, and committed reservations cannot be cancelled or refunded through this API.

### Errors that need a decision

Use `errors.Is` to inspect errors, including wrapped store errors. These errors need a change in input, quota, or state before a retry can succeed:

- `ErrInvalid`: fix the arguments.
- `ErrExceeded`: the requested amount does not fit within the limit. Wait for capacity or a limit change before trying again.
- `ErrOutsidePeriod`: the original period is not active for a new reservation.
- `ErrConflict`: the request ID already exists with a different amount.
- `ErrNotFound`: `Commit` or `Cancel` has no matching reservation.
- `ErrFinalized`: the requested operation conflicts with a committed or cancelled state.
- `ErrCorrupt`: stored counters or reservation state are invalid and need investigation.

## Stores

`quota.Store` is the public storage contract. An implementation must update usage and a reservation atomically. The repository includes a [GORM/PostgreSQL implementation example](examples/postgresstore/store.go) in a separate module and an internal memory store for tests and the demo. GORM and its driver are dependencies of the example only; the library module has no database driver dependency.

### PostgreSQL with GORM

The example stores one budget per row in `quota_budgets`:

| Column | Contents |
| --- | --- |
| `key` | Subject, resource, and period boundaries encoded together as JSON text |
| `reserved`, `committed` | Usage counters |
| `reservations` | A JSON object containing that budget's reservations |

Each update creates the row if needed, locks it with `FOR UPDATE`, calls the quota callback, and saves the row. All of this happens in one transaction, so concurrent requests cannot spend the same remaining capacity.

`postgresstore.New` accepts a base `*gorm.DB` connection opened with `gorm.io/driver/postgres`, not an active transaction. The application owns the connection and closes it on shutdown.

At application startup, using your context and PostgreSQL DSN:

```go
db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
if err != nil {
    return err
}
store := postgresstore.New(db)
if err := store.Init(ctx); err != nil {
    return err
}
limiter := quota.New(store)
```

`Init` uses GORM's [AutoMigrate](https://gorm.io/docs/migration.html) to create or update the table from the model. Request IDs are Base64-encoded inside the JSON object so every ID can be stored without changing its identity.

This example is intended for small reservation counts per budget and short subject/resource IDs. Every update reads and rewrites the budget's entire reservation object. For large budgets, store reservations in a separate table while keeping the same budget-row lock. Validation is handled by the quota library; the example does not add database check constraints.

This is a new storage layout. The earlier `usagequota_budgets` and `usagequota_reservations` tables are not read, migrated, or deleted. If you have existing usage, migrate it before switching to this example; otherwise the new table starts with empty budgets.

## Get started

Requires Go 1.25 or later.

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
