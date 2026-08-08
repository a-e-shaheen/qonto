// Package seed loads the fixture data extracted from the exercise's
// qonto_accounts.sqlite into Postgres on first startup. It is a data-loading step,
// deliberately kept separate from schema migrations: migrations define structure,
// this defines the one known starting dataset for local/dev use.
package seed

import (
	"context"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
)

// psql is the shared squirrel statement builder, configured for Postgres's
// dollar-sign placeholders — every query in this package goes through it, including
// the seed-time count/insert, for the same reason the store layer does: no raw SQL
// string assembly anywhere in the codebase.
var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// account is the single bank_accounts row present in qonto_accounts.sqlite.
type account struct {
	OrganizationName string
	BalanceCents     int64
	IBAN             string
	BIC              string
}

// transaction is a pre-existing ledger row present in qonto_accounts.sqlite, seeded
// against the account above. These predate bulk-transfer batch tracking, so they
// carry no batch_id.
type transaction struct {
	CounterpartyName string
	CounterpartyIBAN string
	CounterpartyBIC  string
	AmountCents      int64
	AmountCurrency   string
	Description      string
}

var seedAccount = account{
	OrganizationName: "ACME Corp",
	BalanceCents:     10_000_000,
	IBAN:             "FR10474608000002006107XXXXX",
	BIC:              "OIVUSCLQXXX",
}

var seedTransactions = []transaction{
	{
		CounterpartyName: "ACME Corp. Main Account",
		CounterpartyIBAN: "EE382200221020145685",
		CounterpartyBIC:  "CCOPFRPPXXX",
		AmountCents:      11_000_000,
		AmountCurrency:   "EUR",
		Description:      "Treasury income",
	},
	{
		CounterpartyName: "Bip Bip",
		CounterpartyIBAN: "EE383680981021245685",
		CounterpartyBIC:  "CRLYFRPPTOU",
		AmountCents:      -1_000_000,
		AmountCurrency:   "EUR",
		Description:      "Bip Bip Salary",
	},
}

// Pool is the minimal capability Seeder needs — satisfied by *pgxpool.Pool, and
// small enough to fake in tests without a real database.
type Pool interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Seeder loads the fixture dataset into a Pool. Constructed explicitly (rather than
// exposing a bare package function) so it can be built against a fake Pool in tests.
type Seeder struct {
	pool Pool
}

// NewSeeder builds a Seeder bound to pool.
func NewSeeder(pool Pool) *Seeder {
	return &Seeder{pool: pool}
}

// Seed inserts the fixture account and its historical transactions if, and only if,
// bank_accounts is currently empty. Safe to call on every startup: a container
// restart against an already-seeded database is a no-op. The account and its
// transactions are always seeded together as one atomic unit — there's no partial
// or resumable seed state to reason about, unlike a large synthetic load-test
// dataset where per-row ON CONFLICT DO NOTHING would matter more than an upfront
// existence check.
func (s *Seeder) Seed(ctx context.Context) error {
	countSQL, countArgs, err := psql.Select("count(*)").From("bank_accounts").ToSql()
	if err != nil {
		return fmt.Errorf("build count query: %w", err)
	}

	var count int
	if err := s.pool.QueryRow(ctx, countSQL, countArgs...).Scan(&count); err != nil {
		return fmt.Errorf("check existing bank_accounts: %w", err)
	}
	if count > 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin seed transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	insertAccountSQL, insertAccountArgs, err := psql.Insert("bank_accounts").
		Columns("organization_name", "balance_cents", "iban", "bic").
		Values(seedAccount.OrganizationName, seedAccount.BalanceCents, seedAccount.IBAN, seedAccount.BIC).
		Suffix("RETURNING id").
		ToSql()
	if err != nil {
		return fmt.Errorf("build insert account query: %w", err)
	}

	var accountID int64
	if err := tx.QueryRow(ctx, insertAccountSQL, insertAccountArgs...).Scan(&accountID); err != nil {
		return fmt.Errorf("seed bank_accounts: %w", err)
	}

	rows := make([][]any, len(seedTransactions))
	for i, t := range seedTransactions {
		rows[i] = []any{
			t.CounterpartyName, t.CounterpartyIBAN, t.CounterpartyBIC,
			t.AmountCents, t.AmountCurrency, accountID, t.Description,
		}
	}
	// COPY is a distinct wire-protocol operation, not a builder-generated SQL
	// statement — squirrel doesn't (and can't) build this one.
	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"transactions"},
		[]string{
			"counterparty_name", "counterparty_iban", "counterparty_bic",
			"amount_cents", "amount_currency", "bank_account_id", "description",
		},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("seed transactions: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit seed transaction: %w", err)
	}
	return nil
}
