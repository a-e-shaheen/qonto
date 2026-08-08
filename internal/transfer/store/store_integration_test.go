//go:build integration

package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"qonto-bulk-transfer/internal/transfer/model"
	"qonto-bulk-transfer/internal/transfer/store"
	"qonto-bulk-transfer/pkg/testutil"
)

func TestMigrations_PartitioningAndIndexes(t *testing.T) {
	t.Parallel()
	pool := testutil.OpenDB(t)
	ctx := context.Background()

	t.Run("transactions has pg_partman-created partitions", func(t *testing.T) {
		var partitionCount int
		err := pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_inherits WHERE inhparent = 'public.transactions'::regclass`,
		).Scan(&partitionCount)
		require.NoError(t, err)
		assert.Greater(t, partitionCount, 0, "transactions should have at least one partition")
	})

	indexes := []struct {
		table string
		index string
	}{
		{"bank_accounts", "uq_bank_accounts_iban_bic"},
		{"idempotency_keys", "uq_idempotency_keys_key"},
		{"bulk_transfer_batches", "idx_bulk_transfer_batches_account_created_at"},
		{"bulk_transfer_batches", "idx_bulk_transfer_batches_idempotency_key"},
	}
	for _, tt := range indexes {
		t.Run("index "+tt.index+" exists on "+tt.table, func(t *testing.T) {
			var exists bool
			err := pool.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE tablename = $1 AND indexname = $2)`,
				tt.table, tt.index,
			).Scan(&exists)
			require.NoError(t, err)
			assert.True(t, exists)
		})
	}
}

func TestTransactionHistoryPage(t *testing.T) {
	t.Parallel()
	pool := testutil.OpenDB(t)
	ctx := context.Background()
	repo := store.New(pool)

	accountID := insertTestAccount(t, pool, "TESTORGXXX", "FR0000000000000000000000001", 1_000_000)
	insertTestTransactions(t, pool, accountID, 5) // ids 1..5, oldest to newest

	tests := []struct {
		name      string
		limit     int
		wantCount int
	}{
		{name: "first page smaller than total rows", limit: 2, wantCount: 2},
		{name: "limit larger than available rows returns everything", limit: 10, wantCount: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := repo.TransactionHistoryPage(ctx, accountID, model.Cursor{}, tt.limit)
			require.NoError(t, err)
			assert.Len(t, items, tt.wantCount)
		})
	}

	t.Run("paging through with the returned cursor visits every row exactly once, most recent first", func(t *testing.T) {
		var cursor model.Cursor
		var seenIDs []int64
		for {
			items, err := repo.TransactionHistoryPage(ctx, accountID, cursor, 2)
			require.NoError(t, err)
			if len(items) == 0 {
				break
			}
			for _, item := range items {
				seenIDs = append(seenIDs, item.ID)
			}
			last := items[len(items)-1]
			cursor = model.Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
			if len(items) < 2 {
				break
			}
		}
		assert.Equal(t, []int64{5, 4, 3, 2, 1}, seenIDs)
	})
}

func insertTestAccount(t *testing.T, pool *pgxpool.Pool, bic, iban string, balanceCents int64) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO bank_accounts (organization_name, balance_cents, iban, bic) VALUES ($1, $2, $3, $4) RETURNING id`,
		"Test Org", balanceCents, iban, bic,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertTestTransactions(t *testing.T, pool *pgxpool.Pool, accountID int64, count int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < count; i++ {
		_, err := pool.Exec(ctx,
			`INSERT INTO transactions (counterparty_name, counterparty_iban, counterparty_bic, amount_cents, amount_currency, bank_account_id, description)
			 VALUES ($1, $2, $3, $4, 'EUR', $5, $6)`,
			"Counterparty", "EE383680981021245685", "CRLYFRPPTOU", 1000, accountID, "test row",
		)
		require.NoError(t, err)
	}
}
