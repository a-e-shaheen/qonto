package service

import (
	"context"
	"fmt"

	"qonto-bulk-transfer/internal/transfer/model"
	"qonto-bulk-transfer/pkg/observability"
)

// TransactionHistoryPage is one page of an account's transaction history, ready for
// the handler to serialize.
type TransactionHistoryPage struct {
	Items      []model.TransactionHistoryItem
	NextCursor string
	HasMore    bool
}

// ListTransactionHistory returns up to limit transactions for accountID, most
// recent first, starting after cursor. Read-only — deliberately not run inside
// Atomic, since pagination has no write to protect.
func (s *Service) ListTransactionHistory(ctx context.Context, accountID int64, cursorStr string, limit int) (TransactionHistoryPage, error) {
	ctx = observability.WithPhase(ctx, "list_transaction_history")

	cursor, err := model.DecodeCursor(cursorStr)
	if err != nil {
		return TransactionHistoryPage{}, fmt.Errorf("decode cursor: %w", err)
	}

	// Fetch one extra row so has_more can be answered without a second COUNT query.
	items, err := s.repo.TransactionHistoryPage(ctx, accountID, cursor, limit+1)
	if err != nil {
		return TransactionHistoryPage{}, err
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	var nextCursor string
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = model.EncodeCursor(model.Cursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}

	return TransactionHistoryPage{Items: items, NextCursor: nextCursor, HasMore: hasMore}, nil
}
