# Bulk transfer request flow

This is the actual logic behind
[`SubmitBulkTransfer`](../internal/transfer/service/transfer.go) — the part of
this exercise that matters most. Every step below runs inside one Postgres
transaction; see the [README](../README.md#concurrency-atomicity-and-idempotency)
for the reasoning behind each lock.

```mermaid
sequenceDiagram
    actor Client
    participant Handler
    participant Service
    participant Store
    participant Postgres

    Client->>Handler: POST /transfers/bulk
    Handler->>Handler: decode + validate (go-playground/validator)
    Handler->>Handler: resolve idempotency key<br/>(Idempotency-Key header, else SHA-256(body))
    Handler->>Service: SubmitBulkTransfer(req)

    Service->>Store: Atomic(fn)
    activate Store
    Store->>Postgres: BEGIN

    Service->>Store: AdvisoryLock(key)
    Store->>Postgres: SELECT pg_advisory_xact_lock(hashtext(key))
    Note right of Postgres: serializes any two requests<br/>sharing this key, before either<br/>has resolved an account

    Service->>Store: FindIdempotencyRecordStatus(key)
    Store->>Postgres: SELECT response_status<br/>FROM idempotency_keys WHERE key=$1

    alt key already recorded (replay)
        Postgres-->>Service: status (204 / 422 / 404)
        Note over Service: body reconstructed from status alone —<br/>bank_accounts is never touched
    else key is new
        Service->>Store: FindAccountForUpdate(iban, bic)
        Store->>Postgres: SELECT ... FROM bank_accounts<br/>WHERE iban=$1 AND bic=$2 FOR UPDATE
        Note right of Postgres: row lock — serializes any two<br/>DIFFERENT keys targeting this account,<br/>regardless of instance count

        alt account not found
            Service->>Store: RecordIdempotency(key, 404)
            Note over Service: result = 404
        else balance < total
            Service->>Store: RecordIdempotency(key, 422)
            Note over Service: result = 422, nothing else written
        else balance >= total
            Service->>Store: InsertBatch(accountID, key, total)
            Store->>Postgres: INSERT INTO bulk_transfer_batches ... RETURNING id
            Service->>Store: InsertTransactions(accountID, batchID, transfers)
            Store->>Postgres: COPY transactions FROM STDIN
            Note right of Postgres: negative amount_cents per transfer
            Service->>Store: DebitAccount(accountID, total)
            Store->>Postgres: UPDATE bank_accounts<br/>SET balance_cents = balance_cents - total<br/>RETURNING balance_cents
            Service->>Service: checkBalanceInvariant(newBalance)
            Note over Service: newBalance < 0 would abort the whole<br/>transaction here — see balance_invariant_violations_total
            Service->>Store: RecordIdempotency(key, 204)
            Note over Service: result = 204
        end
    end

    alt fn returned nil (any of the outcomes above)
        Store->>Postgres: COMMIT
        Note right of Postgres: span status = Ok<br/>(422/404 are expected outcomes, not errors)
    else fn returned an error (infra failure, not a business outcome)
        Store->>Postgres: ROLLBACK
        Note right of Postgres: span status = Error
    end
    deactivate Store

    Service-->>Handler: Result{StatusCode, Body}
    Handler-->>Client: HTTP response
```

The two `alt` blocks that matter for correctness under concurrency:

- **Same idempotency key, concurrent** → both requests hit the advisory lock
  first; the second waits for the first to commit, then sees the recorded
  status in `FindIdempotencyRecordStatus` and replays it — the batch is applied
  exactly once. Proven by
  [`TestSubmitBulkTransfer_ConcurrentDuplicateRequestsShareOneOutcome`](../internal/transfer/service/service_integration_test.go).
- **Different idempotency keys, same account, concurrent** → both pass the
  advisory lock (different keys, no conflict there), then serialize on the
  `FOR UPDATE` row lock instead; each sees the balance as it stood after the
  previous one committed. Proven by
  [`TestSubmitBulkTransfer_ConcurrentRequestsNeverOverdraw`](../internal/transfer/service/service_integration_test.go).
