-- The transfer ledger. Partitioned by created_at (monthly, via pg_partman) because it's
-- the one table here with an unbounded, append-only growth pattern. Partition key must
-- be part of every unique constraint on the table, hence the composite primary key.
CREATE TABLE IF NOT EXISTS transactions (
    id BIGSERIAL,
    counterparty_name TEXT NOT NULL,
    counterparty_iban TEXT NOT NULL,
    counterparty_bic TEXT NOT NULL,
    amount_cents BIGINT NOT NULL,
    amount_currency TEXT NOT NULL DEFAULT 'EUR',
    bank_account_id BIGINT NOT NULL REFERENCES bank_accounts (id),
    description TEXT,
    batch_id BIGINT REFERENCES bulk_transfer_batches (id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

-- Covers the pagination endpoint's cursor query (bank_account_id, created_at, id) and
-- lets Postgres prune to the relevant partitions.
CREATE INDEX IF NOT EXISTS idx_transactions_account_created_at
    ON transactions (bank_account_id, created_at, id);

CREATE INDEX IF NOT EXISTS idx_transactions_batch_id
    ON transactions (batch_id)
    WHERE batch_id IS NOT NULL;

-- Catch-all for any row outside the ranges pg_partman has pre-created (e.g. right
-- after a fresh migration, before the background worker's first run).
CREATE TABLE IF NOT EXISTS transactions_default PARTITION OF transactions DEFAULT;

-- pg_partman 5.x only supports native partitioning (this is what PARTITION BY RANGE
-- above already gives us), so p_type is no longer a parameter here at all.
SELECT partman.create_parent(
    p_parent_table := 'public.transactions',
    p_control := 'created_at',
    p_interval := '1 month',
    p_default_table := false
);

UPDATE partman.part_config
SET infinite_time_partitions = true
WHERE parent_table = 'public.transactions';
