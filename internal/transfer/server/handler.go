package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-playground/validator/v10"

	"qonto-bulk-transfer/internal/transfer/model"
	"qonto-bulk-transfer/internal/transfer/service"
	"qonto-bulk-transfer/pkg/observability"
	pkgserver "qonto-bulk-transfer/pkg/server"
)

// Service is everything the handler needs from the business layer. Implemented by
// *service.Service.
type Service interface {
	SubmitBulkTransfer(ctx context.Context, req model.BulkTransferRequest) (service.Result, error)
	ListTransactionHistory(ctx context.Context, accountID int64, cursor string, limit int) (service.TransactionHistoryPage, error)
}

// Handler serves the bulk-transfer HTTP API.
type Handler struct {
	svc      Service
	validate *validator.Validate
}

// New builds a Handler bound to svc.
func New(svc Service) *Handler {
	return &Handler{svc: svc, validate: newValidator()}
}

// Routes returns the ServeMux for this feature's endpoints, ready to be mounted
// directly or nested under a parent mux.
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("POST /transfers/bulk",
		pkgserver.MetricsMiddleware("POST /transfers/bulk", http.HandlerFunc(h.handleSubmitBulkTransfer)))
	mux.Handle("GET /accounts/{id}/transactions",
		pkgserver.MetricsMiddleware("GET /accounts/{id}/transactions", http.HandlerFunc(h.handleListTransactionHistory)))
	return mux
}

func (h *Handler) handleSubmitBulkTransfer(w http.ResponseWriter, r *http.Request) {
	// The request-root span was already started by pkgserver.LoggingMiddleware;
	// r.Context() carries it.
	ctx := r.Context()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		recordRejection(ctx)
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}

	var dto bulkTransferRequestDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		recordRejection(ctx)
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}

	if err := h.validate.Struct(dto); err != nil {
		recordRejection(ctx)
		msg, details := formatValidationError(err)
		writeError(w, http.StatusBadRequest, msg, details...)
		return
	}

	req, err := toDomainRequest(dto, r.Header.Get("Idempotency-Key"), body)
	if err != nil {
		recordRejection(ctx)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := h.svc.SubmitBulkTransfer(ctx, req)
	recordOutcome(ctx, req, result, err)
	if err != nil {
		observability.RecordError(ctx, err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if result.Body != nil {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(result.StatusCode)
	if result.Body != nil {
		_, _ = w.Write(result.Body)
	}
}

// toDomainRequest converts a validated DTO into the domain request the service
// expects, parsing every amount to integer cents and resolving the idempotency
// key: the client's Idempotency-Key header if sent, otherwise a hash of the raw
// request body. Hashing the raw bytes (not a re-serialized/canonicalized form)
// only catches byte-identical retries — good enough for "the client resent the
// same request", which is the case that actually happens in practice (a network
// glitch or crash-and-retry resends the same bytes; it doesn't reformat JSON
// in between).
func toDomainRequest(dto bulkTransferRequestDTO, headerKey string, rawBody []byte) (model.BulkTransferRequest, error) {
	transfers := make([]model.CreditTransfer, len(dto.CreditTransfers))
	for i, t := range dto.CreditTransfers {
		cents, err := model.ParseAmountCents(t.Amount)
		if err != nil {
			return model.BulkTransferRequest{}, err
		}
		transfers[i] = model.CreditTransfer{
			AmountCents:      cents,
			Currency:         t.Currency,
			CounterpartyName: t.CounterpartyName,
			CounterpartyBIC:  t.CounterpartyBIC,
			CounterpartyIBAN: t.CounterpartyIBAN,
			Description:      t.Description,
		}
	}

	key := headerKey
	if key == "" {
		key = hashBody(rawBody)
	}

	return model.BulkTransferRequest{
		OrganizationBIC:  dto.OrganizationBIC,
		OrganizationIBAN: dto.OrganizationIBAN,
		CreditTransfers:  transfers,
		IdempotencyKey:   key,
	}, nil
}

func hashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
