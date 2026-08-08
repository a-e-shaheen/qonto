package service

import (
	"context"
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"

	"qonto-bulk-transfer/internal/transfer/model"
	"qonto-bulk-transfer/pkg/observability"
)

// HTTP status codes this service produces. Kept as named constants here (rather
// than importing net/http into a package that otherwise has no HTTP dependency) so
// the handler and tests reference the same values the service actually returns.
const (
	StatusCreated           = 204
	StatusAccountNotFound   = 404
	StatusInsufficientFunds = 422
)

// Result is the outcome of a bulk transfer submission: an HTTP status code and an
// optional pre-serialized JSON body, ready for the handler to write directly.
type Result struct {
	StatusCode int
	Body       []byte
}

type errorBody struct {
	Error string `json:"error"`
}

func encodeError(msg string) []byte {
	b, _ := json.Marshal(errorBody{Error: msg}) //nolint:errchkjson // errorBody always marshals
	return b
}

// responseBodyFor reconstructs the response body for a given status code. Every
// outcome this service produces is fully determined by its status — 204 never has
// a body, 404/422 always carry the same fixed message — so this is all a replay
// needs; nothing else is stored per idempotency key.
func responseBodyFor(status int) []byte {
	switch status {
	case StatusAccountNotFound:
		return encodeError("account not found")
	case StatusInsufficientFunds:
		return encodeError("insufficient funds")
	default:
		return nil
	}
}

// SubmitBulkTransfer runs the full check-then-act sequence for one bulk transfer
// request inside a single atomic transaction:
//
//  1. an advisory lock on the idempotency key, so two in-flight requests sharing a
//     key serialize before either resolves an account;
//  2. a replay check against the idempotency ledger — every past outcome (created,
//     denied, or account-not-found) is recorded and replayed on retry;
//  3. only on a genuinely new key: locate and row-lock the account, check funds,
//     and either write the batch + transactions + debit, or record the denial.
//
// A non-nil error here always means an infrastructure failure. Every expected
// business result is communicated through the returned Result with a nil error —
// see pkg/txn's Atomic for why that distinction matters for tracing.
//
// Each phase tags ctx with observability.WithPhase before making its repo calls,
// so pkg/database's QueryTracer prefixes that phase's query spans (e.g.
// "resolve_account: postgres.query") — a trace waterfall shows which step a query
// belongs to without a separate span level per phase.
func (s *Service) SubmitBulkTransfer(ctx context.Context, req model.BulkTransferRequest) (Result, error) {
	var result Result

	err := s.txn.Atomic(ctx, func(ctx context.Context) error {
		ctx = observability.WithPhase(ctx, "check_idempotency")
		if err := s.repo.AdvisoryLock(ctx, req.IdempotencyKey); err != nil {
			return err
		}

		status, found, err := s.repo.FindIdempotencyRecordStatus(ctx, req.IdempotencyKey)
		if err != nil {
			return err
		}
		if found {
			result = Result{StatusCode: status, Body: responseBodyFor(status)}
			observability.RecordOutcome(ctx, "replayed", attribute.Int("http.status_code", status))
			return nil
		}

		ctx = observability.WithPhase(ctx, "resolve_account")
		account, found, err := s.repo.FindAccountForUpdate(ctx, req.OrganizationIBAN, req.OrganizationBIC)
		if err != nil {
			return err
		}
		if !found {
			result = Result{StatusCode: StatusAccountNotFound, Body: responseBodyFor(StatusAccountNotFound)}
			observability.RecordOutcome(ctx, "account_not_found", attribute.Int("http.status_code", StatusAccountNotFound))
			return s.repo.RecordIdempotency(ctx, req.IdempotencyKey, result.StatusCode)
		}

		total := req.TotalCents()
		if account.BalanceCents < total {
			result = Result{StatusCode: StatusInsufficientFunds, Body: responseBodyFor(StatusInsufficientFunds)}
			observability.RecordOutcome(ctx, "insufficient_funds", attribute.Int("http.status_code", StatusInsufficientFunds))
			return s.repo.RecordIdempotency(ctx, req.IdempotencyKey, result.StatusCode)
		}

		ctx = observability.WithPhase(ctx, "persist_batch")
		batchID, err := s.repo.InsertBatch(ctx, account.ID, req.IdempotencyKey, total)
		if err != nil {
			return err
		}
		if err := s.repo.InsertTransactions(ctx, account.ID, batchID, req.CreditTransfers); err != nil {
			return err
		}
		newBalance, err := s.repo.DebitAccount(ctx, account.ID, total)
		if err != nil {
			return err
		}
		if err := checkBalanceInvariant(ctx, account.ID, newBalance); err != nil {
			return err
		}

		result = Result{StatusCode: StatusCreated, Body: nil}
		observability.RecordOutcome(ctx, "created", attribute.Int("http.status_code", StatusCreated))
		return s.repo.RecordIdempotency(ctx, req.IdempotencyKey, StatusCreated)
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}
