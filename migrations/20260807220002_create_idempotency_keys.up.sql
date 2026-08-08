-- Generic replay ledger: deliberately has no foreign key to bank_accounts or
-- bulk_transfer_batches. It doesn't know about the bulk-transfer domain at all,
-- so it can guard any future write endpoint the same way.
-- Not partitioned: a replay request carries only `key`, never a created_at, so the
-- unique constraint must stay a plain single-column global index (see migrations for
-- the transactions table for why partitioning would break that guarantee).
-- No response body stored: every response this service produces is fully
-- determined by its status code (204 always has no body, 404/422 always carry the
-- same fixed message), so the status code alone is enough to replay the response —
-- see service.responseBodyFor. Anything worth recording beyond that belongs on the
-- request's trace span (see observability.RecordOutcome), not in this row.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    id BIGSERIAL PRIMARY KEY,
    key TEXT NOT NULL,
    response_status SMALLINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_idempotency_keys_key UNIQUE (key)
);
