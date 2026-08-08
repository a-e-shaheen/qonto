package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"qonto-bulk-transfer/internal/transfer/model"
	"qonto-bulk-transfer/internal/transfer/service/mocks"
)

func TestListTransactionHistory_NoMorePages(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	txnMock := mocks.NewMockTransactor(t)

	items := []model.TransactionHistoryItem{
		{ID: 2, CreatedAt: time.Now()},
		{ID: 1, CreatedAt: time.Now().Add(-time.Hour)},
	}
	// Asking for limit=20 fetches 21 — the +1 lookahead row that tells us whether
	// there's a next page without a second COUNT query.
	repo.EXPECT().
		TransactionHistoryPage(mock.Anything, int64(1), model.Cursor{}, 21).
		Return(items, nil)

	svc := New(repo, txnMock)
	page, err := svc.ListTransactionHistory(context.Background(), 1, "", 20)

	require.NoError(t, err)
	assert.Len(t, page.Items, 2)
	assert.False(t, page.HasMore)
	assert.Empty(t, page.NextCursor)
}

func TestListTransactionHistory_HasMorePage(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	txnMock := mocks.NewMockTransactor(t)

	now := time.Now()
	items := make([]model.TransactionHistoryItem, 3) // limit=2, so 3 rows come back
	for i := range items {
		items[i] = model.TransactionHistoryItem{ID: int64(3 - i), CreatedAt: now.Add(-time.Duration(i) * time.Hour)}
	}
	repo.EXPECT().
		TransactionHistoryPage(mock.Anything, int64(1), model.Cursor{}, 3).
		Return(items, nil)

	svc := New(repo, txnMock)
	page, err := svc.ListTransactionHistory(context.Background(), 1, "", 2)

	require.NoError(t, err)
	assert.Len(t, page.Items, 2, "the extra lookahead row must be trimmed off")
	assert.True(t, page.HasMore)
	require.NotEmpty(t, page.NextCursor)

	decoded, err := model.DecodeCursor(page.NextCursor)
	require.NoError(t, err)
	assert.Equal(t, page.Items[len(page.Items)-1].ID, decoded.ID, "cursor must point at the last item actually returned")
}

func TestListTransactionHistory_InvalidCursorRejectedWithoutTouchingRepo(t *testing.T) {
	repo := mocks.NewMockRepository(t)
	txnMock := mocks.NewMockTransactor(t)

	svc := New(repo, txnMock)
	_, err := svc.ListTransactionHistory(context.Background(), 1, "not-a-valid-cursor!!", 20)

	assert.Error(t, err)
	repo.AssertNotCalled(t, "TransactionHistoryPage", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}
