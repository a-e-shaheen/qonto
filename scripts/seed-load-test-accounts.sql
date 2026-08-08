-- Seeds :accounts distinct bank accounts for `make load-test`, each with a large
-- balance, so a realistic load test spreads across many accounts instead of
-- hammering the one fixture account seeded from qonto_accounts.sqlite — which
-- can only ever satisfy a single full-size request before every subsequent one
-- correctly gets 422.
--
-- Idempotent: re-running with a larger :accounts adds the new ones; existing
-- ones get their balance topped back up via ON CONFLICT, so repeated load-test
-- runs don't gradually starve themselves of funds.
--
-- Invoked by `make load-test-seed` with -v accounts=N -v balance_cents=N.
INSERT INTO bank_accounts (organization_name, balance_cents, iban, bic)
SELECT
    'Load Test Account ' || i,
    :balance_cents,
    'FRLOADTEST' || lpad(i::text, 17, '0'),
    'LOADTST' || lpad(i::text, 4, '0')
FROM generate_series(1, :accounts) AS i
ON CONFLICT (iban, bic) DO UPDATE SET balance_cents = EXCLUDED.balance_cents;
