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

`quota.Store` is the public storage contract. An implementation must update usage and a reservation atomically. The repository includes a [PostgreSQL example](examples/postgresstore/store.go) in a separate module and an internal memory store for tests and the demo. The library module has no database driver dependency.

The PostgreSQL example stores counters in `quota_budgets` and individual reservations in `quota_reservations`. Each update locks the budget and reads and writes only the requested reservation, within one transaction. Use short subject/resource and request IDs because they are indexed directly.

Earlier examples used a different storage layout. Migrate their data before switching; `Init` does not convert existing reservations.

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
