# Bulk Transfer Service

A Go API that accepts a bulk credit-transfer request for a single Qonto account,
atomically validates the account has sufficient funds for the *entire* batch, and
either persists every transfer and debits the balance, or denies the whole request
and writes nothing.

## The contract

`POST /transfers/bulk` — request:

```json
{
  "organization_bic": "OIVUSCLQXXX",
  "organization_iban": "FR10474608000002006107XXXXX",
  "credit_transfers": [
    {
      "amount": "14.50",
      "currency": "EUR",
      "counterparty_bic": "CRLYFRPPTOU",
      "counterparty_iban": "EE383680981021245685",
      "counterparty_name": "Bip Bip",
      "description": "Wonderland/4410"
    }
  ]
}
```

- `204` — every transfer is persisted, the account's balance is debited by the total.
- `422` — the account doesn't have enough funds for the batch total; nothing is written.
- `400` — malformed JSON or a structural validation failure.
- `404` — no account matches `organization_bic` + `organization_iban` (not called out
  by the spec; documented assumption, see below).

The source assets' prose mentions `201` in one place but the OpenAPI schema in the
same document specifies `204`; the schema is the actual contract, so `204` is what
this service returns.

`GET /accounts/{id}/transactions?limit=&cursor=` is additive — cursor-paginated
transaction history, added because it made verifying the above genuinely testable
end-to-end rather than reasoning about balances only from the database directly.

## Setup & Prerequisites

1. **Container runtime**: Docker Desktop, or Colima (`brew install colima && colima start`).
2. **Install `mise`**:
   - macOS (Homebrew): `brew install mise`
   - Linux / shell: `curl https://mise.run | sh`
   - apt / dnf: `sudo apt install mise` / `sudo dnf install mise`
3. **Install the toolchain**: `mise install` (Go, golangci-lint, mockery, golang-migrate, vegeta).

## Commands

| Action | Command | Notes |
| :--- | :--- | :--- |
| **Start everything** | `make up` | Postgres + app (`:8080`) + Grafana (`:3000`), built and health-checked |
| **Run tests** | `make test` | Unit tests, then integration tests against a real Postgres (`pgtestdb`-isolated per test) |
| **Load test** | `make load-test` | Seeds N accounts, fires concurrent requests at both endpoints via `vegeta` |
| **Lint** | `make lint` | `golangci-lint run` |
| **Stop everything** | `make down` | Tears down all containers and volumes |

## Architecture

See [docs/architecture.md](docs/architecture.md) for the component diagram: a
standard handler → service → store layering, each layer only calling the one below
it. [docs/schema.md](docs/schema.md) has the table/index diagram.

## Concurrency, atomicity, and idempotency

Everything below happens inside **one Postgres transaction** per request (see
[docs/sequence-submit-bulk-transfer.md](docs/sequence-submit-bulk-transfer.md) for
the full step-by-step sequence diagram). The two requirements driving this design
are stated directly in the brief: the server runs as **multiple load-balanced
instances**, and **any process — including the client — can crash or the network
can glitch at any point**.

1. **`SELECT pg_advisory_xact_lock(hashtext($idempotency_key))`.** Transaction-scoped
   (auto-released on commit/rollback), keyed on the request's own identity rather
   than any business row. This is the piece a row lock on `bank_accounts` alone
   *can't* provide: two requests sharing an idempotency key, racing across different
   instances, haven't necessarily resolved an account yet, so nothing else
   serializes them at that point.
   - The idempotency key is the `Idempotency-Key` request header if the client
     sends one, otherwise a SHA-256 hash of the raw request body. The contract has
     no idempotency field, so a header can't be *required* — hashing the body makes
     a byte-identical retry (a crash-and-resend, the exact case the brief calls out)
     safe by default with zero client changes.
2. **`SELECT response_status FROM idempotency_keys WHERE key = $1`.** If found,
   the stored outcome is replayed and the response body is reconstructed
   deterministically from the status code alone — business logic never runs again,
   and `bank_accounts` is never touched. This covers every outcome (`204`, `422`,
   `404`), not just success: a retried denied request gets the same `422` back
   immediately rather than being re-evaluated against a balance that may have since
   changed.
3. Not found → first attempt. `SELECT ... FROM bank_accounts WHERE bic = $1 AND
   iban = $2 FOR UPDATE`. The row lock is what makes correctness independent of
   instance count: two *different* idempotency keys targeting the same account
   still serialize here.
   - Not found → record `{key, 404}`, commit, respond 404.
   - `balance_cents < total_cents` → record `{key, 422}`, commit, respond 422.
     Nothing else is written.
   - Otherwise → insert the transfer rows (`COPY FROM`, negative `amount_cents`
     each — see the sign convention below), insert one `bulk_transfer_batches` row,
     debit `bank_accounts`, record `{key, 204}`. Commit.

Crash safety falls directly out of using one transaction: if the process dies at
any point before `COMMIT`, Postgres rolls the whole thing back. There's no window
where transfers are inserted but the balance isn't debited, or vice versa.

Proven, not just asserted — two integration tests fire concurrent goroutines
against a real Postgres:
- `TestSubmitBulkTransfer_ConcurrentRequestsNeverOverdraw` — many different
  idempotency keys against one account; exactly `floor(balance/amount)` succeed,
  the rest get `422`, the balance never goes negative.
- `TestSubmitBulkTransfer_ConcurrentDuplicateRequestsShareOneOutcome` — the same
  idempotency key fired concurrently; the batch is applied exactly once.

**Sign convention**: outgoing transfers are stored as *negative* `amount_cents`
(debit). The two seeded legacy rows from the source SQLite data are `+11,000,000`
("Treasury income") and `-1,000,000` ("Bip Bip Salary"), net `10,000,000` —
matching the seeded balance exactly, which is what confirmed the convention.

**Don't lose a cent**: amounts are parsed directly from the decimal string into
integer cents (`internal/transfer/model/money.go`) — no `float64` anywhere in the
money path. `"14.50"` → `1450`; anything with more than two decimal places is
rejected at the validation layer before it reaches the service.

## Schema, indexing, and partitioning

- `bank_accounts(id, organization_name, balance_cents, iban, bic)` —
  `UNIQUE (iban, bic)`, exactly what account resolution needs.
- `idempotency_keys(id, key, response_status, created_at)` — `UNIQUE (key)`.
  Deliberately has **no foreign key** to any other table: it doesn't know the
  bulk-transfer domain exists, which is what makes it reusable if another write
  endpoint needs the same replay guard later. It also stores **no response body** —
  every response this service produces is fully determined by its status code
  (`204` always empty, `404`/`422` always the same fixed message), so the code
  alone is enough to replay the response deterministically.
- `bulk_transfer_batches(id, bank_account_id, idempotency_key, total_amount_cents,
  created_at)` — indexed on `(bank_account_id, created_at)` for per-account history,
  and on `idempotency_key` for support lookups. The business record of an accepted
  batch, kept separate from the idempotency ledger above by design.
- `transactions(id, ..., amount_cents, bank_account_id, batch_id, created_at)` —
  **partitioned `RANGE (created_at)`, monthly, via `pg_partman`**. This is the one
  table with unbounded, append-only growth, so it's the one that benefits from
  partition pruning and rolling retention.
  - Only this table is partitioned. Postgres requires the partition key in every
    unique constraint on a partitioned table — `idempotency_keys.key` needs to stay
    a true single-column global-unique lookup (a replay request only ever carries
    the key, never a `created_at` to route it), so partitioning that table would
    force weakening the exact guarantee it exists for.
  - Consequence: the primary key is composite `(id, created_at)`, a Postgres
    requirement for partitioned tables. `(bank_account_id, created_at, id)` covers
    the pagination endpoint's cursor query and prunes to the relevant partitions.

Verified against the real Postgres catalog by
[`TestMigrations_PartitioningAndIndexes`](internal/transfer/store/store_integration_test.go),
not just asserted in a migration file.

## Testing

- **Unit tests** (`make test-unit`): table-driven, mocked `Repository`/`Service`
  interfaces (`mockery`). Fixtures for the success/denial cases are
  `examples/sample1.json` (total €62,251.50, fits the seeded €100,000 balance) and
  `examples/sample2.json` (total €106,482.16, exceeds it) — the provided samples,
  not invented numbers.
- **Integration tests** (`make test-integration`, tag `integration`): real Postgres
  per test via `pgtestdb` (template-cloned, so tests run in parallel without
  clobbering each other), covering migrations/partitioning, the concurrency
  guarantees above, and idempotent replay of every outcome (`204`/`422`/`404`).
- **Load test** (`make load-test`): `vegeta`-driven, round-robins real HTTP requests
  across N seeded accounts against a running `make up` stack — the only layer that
  exercises the full stack (HTTP → handler → service → Postgres) rather than
  calling Go code directly.

## Assumptions

- **404 for an unknown account.** The spec only defines `422` for the funds case;
  an account that doesn't exist at all is treated as a distinct, documented
  extension rather than folded into `422` or `400`.
- **Idempotency key**: `Idempotency-Key` header if the client sends one, else a
  SHA-256 hash of the raw request body. Hashing the raw bytes (not a
  re-serialized/canonicalized form) only catches byte-identical retries — the case
  that actually happens in practice (a crash-and-retry resends the same bytes; it
  doesn't reformat JSON in between).
- **Currency is always `EUR`.** The source data and both sample requests are
  EUR-only; validated with `oneof=EUR` rather than built out as a real
  multi-currency ledger, since nothing in the brief calls for FX.

## Potential improvements

- **Multi-currency support** — the schema stores a currency column already, but
  nothing converts or validates cross-currency amounts; today `EUR` is enforced at
  the validation layer.
  Structured, per-field validation errors on `400` already exist; a stricter OpenAPI
  contract-test suite (schemathesis or similar) would catch drift from the spec
  automatically instead of relying on hand-written cases.
- **Bulk-transfer batch cancellation/reversal** — there's no way to reverse an
  applied batch today; a compensating-transaction endpoint would need its own
  idempotency and locking story, symmetric to the one documented above.
- **Chaos testing** — the crash-safety argument above rests on "it's one Postgres
  transaction, so a mid-flight crash always rolls back," which is sound but
  untested by an actual process-kill harness; a `SIGKILL`-mid-transaction
  integration test would turn that reasoning into direct proof, the same way the
  concurrency argument already is.

## Observability

OpenTelemetry traces, metrics, and logs, all wired through the same
`OTEL_EXPORTER_OTLP_ENDPOINT` to a bundled Grafana/Tempo/Loki/Mimir stack
(`make up` → `http://localhost:3000`, dashboard "Bulk Transfer Service"). Request
logs carry `trace_id`/`span_id` so a log line and its trace cross-reference
directly, and query spans carry the actual bound SQL parameters (`db.args`), not
just the parameterized statement text. This is beyond what the brief asks for; it's
here because reasoning about the concurrency guarantees above during development
was much faster with a real trace waterfall than by reading code alone.

## A note on AI use

This solution was built with heavy, disclosed use of Claude (Anthropic's Claude
Code). Concretely:

- **Used for**: scaffolding the layered structure (handler/service/store),
  generating the golang-migrate migrations and mockery config, writing the
  concurrency integration tests once the locking design was decided, and the
  OpenTelemetry instrumentation (tracing/metrics/logs) — mechanical, well-known
  patterns where the value was speed, not judgment.
- **Not used for**: the core design decisions — the choice to split the
  idempotency ledger from the business record, which lock serializes which race
  (advisory lock vs. row lock), the sign convention for `amount_cents`, and where
  the 422-vs-404 line falls. Those were decided first, by hand, then implemented
  with AI assistance against that decision.
- **Pushed back / iterated**: an early pass restructured the service method into
  several span-wrapped sub-methods to get clearer per-step traces; that traded away
  the original flat, easy-to-read control flow for a marginal tracing benefit, and
  was reverted in favor of tagging spans with a "phase" via context instead —
  same visibility, zero restructuring. Also caught and rejected a stray change that
  looked like it removed the advisory lock (it hadn't — it had just been moved into
  a helper method — but the restructuring itself was still the wrong trade, so the
  revert stood).
- Every change was verified by hand against a real Postgres and a real Grafana/Tempo
  stack (`make test`, `make up` + live trace/log inspection) before being accepted —
  AI-generated code was never taken on faith.
