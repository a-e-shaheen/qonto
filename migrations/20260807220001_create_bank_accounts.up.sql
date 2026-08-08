CREATE TABLE IF NOT EXISTS bank_accounts (
    id BIGSERIAL PRIMARY KEY,
    organization_name TEXT NOT NULL,
    balance_cents BIGINT NOT NULL CHECK (balance_cents >= 0),
    iban TEXT NOT NULL,
    bic TEXT NOT NULL,
    CONSTRAINT uq_bank_accounts_iban_bic UNIQUE (iban, bic)
);
