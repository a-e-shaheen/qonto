//go:build integration

package service_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"qonto-bulk-transfer/internal/transfer/model"
	"qonto-bulk-transfer/internal/transfer/service"
	"qonto-bulk-transfer/internal/transfer/store"
	"qonto-bulk-transfer/pkg/testutil"
	"qonto-bulk-transfer/pkg/txn"
)

// newTestService wires the real store + real txn.Transactor + real service against
// a freshly-provisioned, fully-migrated, isolated database (see
// internal/testutil.OpenDB) — the actual production wiring from cmd/server/main.go,
// minus the HTTP layer, exercised against real Postgres rather than mocks.
func newTestService(t *testing.T) (*service.Service, *pgxpool.Pool) {
	pool := testutil.OpenDB(t)
	return service.New(store.New(pool), txn.New(pool)), pool
}

func seedTestAccount(t *testing.T, pool *pgxpool.Pool, balanceCents int64) model.Account {
	t.Helper()
	account := model.Account{
		OrganizationName: "Test Org",
		BalanceCents:     balanceCents,
		IBAN:             "FR0000000000000000000000099",
		BIC:              "TESTBICXXXX",
	}
	err := pool.QueryRow(context.Background(),
		`INSERT INTO bank_accounts (organization_name, balance_cents, iban, bic) VALUES ($1, $2, $3, $4) RETURNING id`,
		account.OrganizationName, account.BalanceCents, account.IBAN, account.BIC,
	).Scan(&account.ID)
	require.NoError(t, err)
	return account
}

func currentBalance(t *testing.T, pool *pgxpool.Pool, accountID int64) int64 {
	t.Helper()
	var balance int64
	err := pool.QueryRow(context.Background(), "SELECT balance_cents FROM bank_accounts WHERE id=$1", accountID).Scan(&balance)
	require.NoError(t, err)
	return balance
}

func creditTransfers(cents []int64) []model.CreditTransfer {
	transfers := make([]model.CreditTransfer, len(cents))
	for i, c := range cents {
		transfers[i] = model.CreditTransfer{
			AmountCents:      c,
			Currency:         "EUR",
			CounterpartyName: "Counterparty",
			CounterpartyBIC:  "CRLYFRPPTOU",
			CounterpartyIBAN: "EE383680981021245685",
			Description:      "integration test transfer",
		}
	}
	return transfers
}

func TestSubmitBulkTransfer_Outcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		seedBalanceCents int64
		transferCents    []int64
		iban, bic        string // empty means "use the seeded account's own identifiers"
		wantStatus       int
		wantBalanceAfter int64
	}{
		{
			name:             "sufficient funds succeeds and debits exactly the total (sample1.json)",
			seedBalanceCents: 10_000_000,
			transferCents:    []int64{1450, 6_123_800, 99_900},
			wantStatus:       204,
			wantBalanceAfter: 10_000_000 - (1450 + 6_123_800 + 99_900),
		},
		{
			name:             "insufficient funds is denied and the balance is untouched (sample2.json)",
			seedBalanceCents: 10_000_000,
			transferCents:    []int64{10_648_216},
			wantStatus:       422,
			wantBalanceAfter: 10_000_000,
		},
		{
			name:             "unknown account is denied with 404 and nothing is written",
			seedBalanceCents: 10_000_000,
			transferCents:    []int64{1450},
			iban:             "FR0000000000000000000000000",
			bic:              "UNKNOWNBICXXX",
			wantStatus:       404,
			wantBalanceAfter: 10_000_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, pool := newTestService(t)
			account := seedTestAccount(t, pool, tt.seedBalanceCents)

			iban, bic := account.IBAN, account.BIC
			if tt.iban != "" {
				iban, bic = tt.iban, tt.bic
			}

			result, err := svc.SubmitBulkTransfer(context.Background(), model.BulkTransferRequest{
				OrganizationIBAN: iban,
				OrganizationBIC:  bic,
				CreditTransfers:  creditTransfers(tt.transferCents),
				IdempotencyKey:   "test-key",
			})

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, result.StatusCode)
			assert.Equal(t, tt.wantBalanceAfter, currentBalance(t, pool, account.ID))
		})
	}
}

func TestSubmitBulkTransfer_RetryWithSameIdempotencyKeyDoesNotDoubleApply(t *testing.T) {
	t.Parallel()
	svc, pool := newTestService(t)
	account := seedTestAccount(t, pool, 10_000_000)

	req := model.BulkTransferRequest{
		OrganizationIBAN: account.IBAN,
		OrganizationBIC:  account.BIC,
		CreditTransfers:  creditTransfers([]int64{1450, 6_123_800, 99_900}),
		IdempotencyKey:   "retry-key",
	}

	first, err := svc.SubmitBulkTransfer(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, 204, first.StatusCode)

	second, err := svc.SubmitBulkTransfer(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, first.StatusCode, second.StatusCode)

	wantBalance := int64(10_000_000 - (1450 + 6_123_800 + 99_900))
	assert.Equal(t, wantBalance, currentBalance(t, pool, account.ID), "balance must be debited exactly once")

	var batchCount int
	err = pool.QueryRow(context.Background(),
		"SELECT count(*) FROM bulk_transfer_batches WHERE bank_account_id=$1", account.ID,
	).Scan(&batchCount)
	require.NoError(t, err)
	assert.Equal(t, 1, batchCount, "only one batch must exist despite two submissions")
}

// TestSubmitBulkTransfer_ConcurrentRequestsNeverOverdraw proves the FOR UPDATE row
// lock on bank_accounts (not just the advisory lock, which only protects same-key
// duplicates): five distinct requests race for a balance that only fits three of
// them, regardless of how many app instances would be handling them.
func TestSubmitBulkTransfer_ConcurrentRequestsNeverOverdraw(t *testing.T) {
	t.Parallel()
	svc, pool := newTestService(t)

	const startBalance = 10_000  // €100.00
	const transferAmount = 3_000 // €30.00 each — only floor(10000/3000)=3 can fit
	const concurrentRequests = 5

	account := seedTestAccount(t, pool, startBalance)

	results := make([]service.Result, concurrentRequests)
	errs := make([]error, concurrentRequests)
	var wg sync.WaitGroup
	for i := 0; i < concurrentRequests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.SubmitBulkTransfer(context.Background(), model.BulkTransferRequest{
				OrganizationIBAN: account.IBAN,
				OrganizationBIC:  account.BIC,
				CreditTransfers:  creditTransfers([]int64{transferAmount}),
				IdempotencyKey:   fmt.Sprintf("concurrent-%d", i),
			})
		}(i)
	}
	wg.Wait()

	var succeeded int
	for i, result := range results {
		require.NoError(t, errs[i])
		switch result.StatusCode {
		case 204:
			succeeded++
		case 422:
			// expected for whichever requests didn't fit
		default:
			t.Fatalf("unexpected status code %d", result.StatusCode)
		}
	}

	assert.Equal(t, 3, succeeded, "exactly floor(10000/3000)=3 of the 5 concurrent requests should succeed")
	assert.Equal(t, int64(startBalance-3*transferAmount), currentBalance(t, pool, account.ID))
}

// TestSubmitBulkTransfer_ConcurrentDuplicateRequestsShareOneOutcome proves the
// advisory lock: without it, concurrent requests sharing one idempotency key could
// all pass the "not yet recorded" check under READ COMMITTED before any of them
// commits, and the batch would be applied once per request instead of once total.
func TestSubmitBulkTransfer_ConcurrentDuplicateRequestsShareOneOutcome(t *testing.T) {
	t.Parallel()
	svc, pool := newTestService(t)
	const concurrentRequests = 5

	account := seedTestAccount(t, pool, 10_000_000)
	req := model.BulkTransferRequest{
		OrganizationIBAN: account.IBAN,
		OrganizationBIC:  account.BIC,
		CreditTransfers:  creditTransfers([]int64{1450, 6_123_800, 99_900}),
		IdempotencyKey:   "same-key-for-all",
	}

	results := make([]service.Result, concurrentRequests)
	errs := make([]error, concurrentRequests)
	var wg sync.WaitGroup
	for i := 0; i < concurrentRequests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = svc.SubmitBulkTransfer(context.Background(), req)
		}(i)
	}
	wg.Wait()

	for i := range results {
		require.NoError(t, errs[i])
		assert.Equal(t, 204, results[i].StatusCode)
	}

	wantBalance := int64(10_000_000 - (1450 + 6_123_800 + 99_900))
	assert.Equal(t, wantBalance, currentBalance(t, pool, account.ID),
		"the batch must be applied exactly once despite 5 concurrent identical requests")

	var batchCount int
	err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM bulk_transfer_batches WHERE bank_account_id=$1", account.ID,
	).Scan(&batchCount)
	require.NoError(t, err)
	assert.Equal(t, 1, batchCount)
}
