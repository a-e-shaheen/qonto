# Schema

```mermaid
erDiagram
    bank_accounts ||--o{ bulk_transfer_batches : "debited by"
    bank_accounts ||--o{ transactions : "ledger for"
    bulk_transfer_batches ||--o{ transactions : "produces"

    bank_accounts {
        bigint id PK
        text organization_name
        bigint balance_cents "CHECK >= 0"
        text iban UK "UNIQUE(iban, bic)"
        text bic UK
    }

    idempotency_keys {
        bigint id PK
        text key UK "UNIQUE, no FK to any other table"
        smallint response_status
        timestamptz created_at
    }

    bulk_transfer_batches {
        bigint id PK
        bigint bank_account_id FK
        text idempotency_key "not a DB-level FK — see note below"
        bigint total_amount_cents "CHECK > 0"
        timestamptz created_at
    }

    transactions {
        bigint id "part of composite PK"
        text counterparty_name
        text counterparty_iban
        text counterparty_bic
        bigint amount_cents "negative = debit, positive = credit"
        text amount_currency
        bigint bank_account_id FK
        text description
        bigint batch_id FK "nullable — seeded legacy rows have none"
        timestamptz created_at PK "partition key, RANGE monthly via pg_partman"
    }
```

Notes that don't fit in a diagram:

- **`idempotency_keys` has no foreign key to anything.** It doesn't know the
  bulk-transfer domain exists — see the
  [README](../README.md#schema-indexing-and-partitioning) for why that's what
  makes it reusable, and why it's the one table that must *not* be partitioned by
  `created_at` even though it has the column.
- **`transactions.batch_id` isn't tied to `bulk_transfer_batches.idempotency_key`
  by a database constraint** — `bulk_transfer_batches` stores its own copy of the
  idempotency key value (indexed, for support/debug lookups: "which batch did
  this client-supplied key produce"), but the two tables are otherwise
  independent by design.
- **`transactions`' primary key is `(id, created_at)`, not just `id`** — a
  consequence of `PARTITION BY RANGE (created_at)`: Postgres requires the
  partition key in every unique constraint on a partitioned table.
- Every relationship and lookup here is covered by an index — see the table in
  the [README](../README.md#schema-indexing-and-partitioning), verified against
  the real Postgres catalog by
  [`TestMigrations_PartitioningAndIndexes`](../internal/transfer/store/store_integration_test.go).
