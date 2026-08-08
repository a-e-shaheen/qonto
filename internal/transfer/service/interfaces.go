// Package service holds the business rules for the bulk-transfer feature: request
// validation the handler doesn't already cover, the decoupled idempotency guard,
// the account/funds check, and orchestrating all of it atomically. Nothing here
// knows about HTTP or SQL syntax — it depends on the two interfaces below, both
// satisfied by concrete types elsewhere in the repo (store.Repository, txn.Transactor)
// and mocked here for unit tests.
package service

import (
	"context"

	"qonto-bulk-transfer/internal/transfer/model"
)

// Repository is everything the service needs from the data layer. Implemented by
// *store.Repository.
type Repository interface {
	AdvisoryLock(ctx context.Context, key string) error
	FindIdempotencyRecordStatus(ctx context.Context, key string) (int, bool, error)
	RecordIdempotency(ctx context.Context, key string, status int) error
	FindAccountForUpdate(ctx context.Context, iban, bic string) (model.Account, bool, error)
	DebitAccount(ctx context.Context, accountID, amountCents int64) (int64, error)
	InsertBatch(ctx context.Context, accountID int64, idempotencyKey string, totalCents int64) (int64, error)
	InsertTransactions(ctx context.Context, accountID, batchID int64, transfers []model.CreditTransfer) error
	TransactionHistoryPage(ctx context.Context, accountID int64, cursor model.Cursor, limit int) ([]model.TransactionHistoryItem, error)
}

// Transactor runs a function atomically. Implemented by *txn.Transactor.
type Transactor interface {
	Atomic(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service implements the bulk-transfer business rules against a Repository and
// Transactor.
type Service struct {
	repo Repository
	txn  Transactor
}

// New builds a Service.
func New(repo Repository, txn Transactor) *Service {
	return &Service{repo: repo, txn: txn}
}
