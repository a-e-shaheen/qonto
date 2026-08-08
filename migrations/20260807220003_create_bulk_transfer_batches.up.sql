-- The business record of an accepted batch, separate from the idempotency ledger.
-- Small / looked-up by account, not append-heavy-over-time, so it stays a regular
-- (non-partitioned) table.
CREATE TABLE IF NOT EXISTS bulk_transfer_batches (
    id BIGSERIAL PRIMARY KEY,
    bank_account_id BIGINT NOT NULL REFERENCES bank_accounts (id),
    idempotency_key TEXT NOT NULL,
    total_amount_cents BIGINT NOT NULL CHECK (total_amount_cents > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_bulk_transfer_batches_account_created_at
    ON bulk_transfer_batches (bank_account_id, created_at);

-- Supports looking up "which batch did this client-supplied idempotency key
-- produce" for support/debugging, without adding a foreign key back to
-- idempotency_keys (that table stays domain-agnostic, see its own migration).
CREATE INDEX IF NOT EXISTS idx_bulk_transfer_batches_idempotency_key
    ON bulk_transfer_batches (idempotency_key);
