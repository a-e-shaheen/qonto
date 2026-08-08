package server

import (
	"net/http"
	"strconv"

	"qonto-bulk-transfer/internal/transfer/model"
	"qonto-bulk-transfer/internal/transfer/service"
	"qonto-bulk-transfer/pkg/observability"
)

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
)

type transactionHistoryItemDTO struct {
	ID               int64  `json:"id"`
	CounterpartyName string `json:"counterparty_name"`
	CounterpartyIBAN string `json:"counterparty_iban"`
	CounterpartyBIC  string `json:"counterparty_bic"`
	Amount           string `json:"amount"`
	Currency         string `json:"currency"`
	Description      string `json:"description"`
	CreatedAt        string `json:"created_at"`
}

type paginationDTO struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

type transactionHistoryResponseDTO struct {
	Data       []transactionHistoryItemDTO `json:"data"`
	Pagination paginationDTO               `json:"pagination"`
}

func (h *Handler) handleListTransactionHistory(w http.ResponseWriter, r *http.Request) {
	// The request-root span was already started by pkgserver.LoggingMiddleware;
	// r.Context() carries it.
	ctx := r.Context()

	accountID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid account id")
		return
	}

	limit := defaultHistoryLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > maxHistoryLimit {
			writeError(w, http.StatusBadRequest, "limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}

	page, err := h.svc.ListTransactionHistory(ctx, accountID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		observability.RecordError(ctx, err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, toHistoryResponseDTO(page))
}

func toHistoryResponseDTO(page service.TransactionHistoryPage) transactionHistoryResponseDTO {
	items := make([]transactionHistoryItemDTO, len(page.Items))
	for i, item := range page.Items {
		items[i] = toHistoryItemDTO(item)
	}
	return transactionHistoryResponseDTO{
		Data: items,
		Pagination: paginationDTO{
			NextCursor: page.NextCursor,
			HasMore:    page.HasMore,
		},
	}
}

func toHistoryItemDTO(item model.TransactionHistoryItem) transactionHistoryItemDTO {
	return transactionHistoryItemDTO{
		ID:               item.ID,
		CounterpartyName: item.CounterpartyName,
		CounterpartyIBAN: item.CounterpartyIBAN,
		CounterpartyBIC:  item.CounterpartyBIC,
		Amount:           model.FormatAmountCents(item.AmountCents),
		Currency:         item.Currency,
		Description:      item.Description,
		CreatedAt:        item.CreatedAt.Format("2006-01-02T15:04:05.000Z07:00"),
	}
}
