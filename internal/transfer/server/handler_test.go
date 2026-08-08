package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"qonto-bulk-transfer/internal/transfer/server/mocks"
	"qonto-bulk-transfer/internal/transfer/service"
)

const validBody = `{
	"organization_bic": "OIVUSCLQXXX",
	"organization_iban": "FR10474608000002006107XXXXX",
	"credit_transfers": [
		{
			"amount": "14.50",
			"currency": "EUR",
			"counterparty_name": "Bip Bip",
			"counterparty_bic": "CRLYFRPPTOU",
			"counterparty_iban": "EE383680981021245685",
			"description": "Wonderland/4410"
		}
	]
}`

func postBulkTransfer(t *testing.T, h *Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/transfers/bulk", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)
	return rec
}

func TestHandleSubmitBulkTransfer_Success(t *testing.T) {
	svc := mocks.NewMockService(t)
	svc.EXPECT().
		SubmitBulkTransfer(mock.Anything, mock.Anything).
		Return(service.Result{StatusCode: 204}, nil)

	rec := postBulkTransfer(t, New(svc), validBody, nil)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.Bytes())
}

func TestHandleSubmitBulkTransfer_ServiceDenial(t *testing.T) {
	svc := mocks.NewMockService(t)
	svc.EXPECT().
		SubmitBulkTransfer(mock.Anything, mock.Anything).
		Return(service.Result{StatusCode: 422, Body: []byte(`{"error":"insufficient funds"}`)}, nil)

	rec := postBulkTransfer(t, New(svc), validBody, nil)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.JSONEq(t, `{"error":"insufficient funds"}`, rec.Body.String())
}

func TestHandleSubmitBulkTransfer_ServiceErrorMapsTo500(t *testing.T) {
	svc := mocks.NewMockService(t)
	svc.EXPECT().
		SubmitBulkTransfer(mock.Anything, mock.Anything).
		Return(service.Result{}, errors.New("db unreachable"))

	rec := postBulkTransfer(t, New(svc), validBody, nil)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestHandleSubmitBulkTransfer_MalformedJSON(t *testing.T) {
	svc := mocks.NewMockService(t)
	rec := postBulkTransfer(t, New(svc), `{not json`, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleSubmitBulkTransfer_ValidationFailures(t *testing.T) {
	tests := map[string]string{
		"missing organization_bic": `{"organization_iban":"FR1","credit_transfers":[{"amount":"1","currency":"EUR","counterparty_name":"x","counterparty_bic":"x","counterparty_iban":"x"}]}`,
		"empty credit_transfers":   `{"organization_bic":"x","organization_iban":"FR1","credit_transfers":[]}`,
		"bad amount format":        `{"organization_bic":"x","organization_iban":"FR1","credit_transfers":[{"amount":"1.234","currency":"EUR","counterparty_name":"x","counterparty_bic":"x","counterparty_iban":"x"}]}`,
		"negative amount":          `{"organization_bic":"x","organization_iban":"FR1","credit_transfers":[{"amount":"-5","currency":"EUR","counterparty_name":"x","counterparty_bic":"x","counterparty_iban":"x"}]}`,
		"non-EUR currency":         `{"organization_bic":"x","organization_iban":"FR1","credit_transfers":[{"amount":"5","currency":"USD","counterparty_name":"x","counterparty_bic":"x","counterparty_iban":"x"}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			svc := mocks.NewMockService(t) // no expectations set: SubmitBulkTransfer must not be called
			rec := postBulkTransfer(t, New(svc), body, nil)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestToDomainRequest_IdempotencyKeyFromHeaderTakesPriority(t *testing.T) {
	var dto bulkTransferRequestDTO
	require.NoError(t, json.Unmarshal([]byte(validBody), &dto))

	req, err := toDomainRequest(dto, "client-supplied-key", []byte(validBody))

	require.NoError(t, err)
	assert.Equal(t, "client-supplied-key", req.IdempotencyKey)
}

func TestToDomainRequest_IdempotencyKeyFallsBackToBodyHash(t *testing.T) {
	var dto bulkTransferRequestDTO
	require.NoError(t, json.Unmarshal([]byte(validBody), &dto))

	req1, err := toDomainRequest(dto, "", []byte(validBody))
	require.NoError(t, err)
	req2, err := toDomainRequest(dto, "", []byte(validBody))
	require.NoError(t, err)
	assert.NotEmpty(t, req1.IdempotencyKey)
	assert.Equal(t, req1.IdempotencyKey, req2.IdempotencyKey, "identical bodies must hash to the same key")

	req3, err := toDomainRequest(dto, "", []byte(validBody+" "))
	require.NoError(t, err)
	assert.NotEqual(t, req1.IdempotencyKey, req3.IdempotencyKey, "different bodies must hash to different keys")
}

func TestHandleListTransactionHistory_Success(t *testing.T) {
	svc := mocks.NewMockService(t)
	svc.EXPECT().
		ListTransactionHistory(mock.Anything, int64(1), "", 20).
		Return(service.TransactionHistoryPage{HasMore: false}, nil)

	req := httptest.NewRequest(http.MethodGet, "/accounts/1/transactions", nil)
	rec := httptest.NewRecorder()
	New(svc).Routes().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var got transactionHistoryResponseDTO
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Empty(t, got.Data)
	assert.False(t, got.Pagination.HasMore)
}

func TestHandleListTransactionHistory_InvalidAccountID(t *testing.T) {
	svc := mocks.NewMockService(t)
	req := httptest.NewRequest(http.MethodGet, "/accounts/not-a-number/transactions", nil)
	rec := httptest.NewRecorder()
	New(svc).Routes().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleListTransactionHistory_InvalidLimit(t *testing.T) {
	svc := mocks.NewMockService(t)
	req := httptest.NewRequest(http.MethodGet, "/accounts/1/transactions?limit=0", nil)
	rec := httptest.NewRecorder()
	New(svc).Routes().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
