# Design notes

A budget is identified by a subject, a resource, and a half-open time period. The caller supplies the limit for each reservation. Pending and committed usage both count against it.

Replaying a pending or committed reservation with the same request ID and amount does not charge twice. `Commit` moves pending usage to committed usage; `Cancel` releases it. A completed or cancelled request ID cannot be reused for new work. `quota.Consume` is a separate calculation for counters that charge every attempt; the caller must read and save the counter in one transaction.

Stores update usage and a reservation atomically. The memory store is local to one process. The PostgreSQL store locks the budget row during updates, so requests for the same budget run in sequence. It does not join an application's existing transaction.

The library does not execute jobs, resolve an uncertain external result, expire reservations, or delete old records. Keep the original budget key for retries and finalization. Retain reservation records for as long as duplicate requests must be recognized.
