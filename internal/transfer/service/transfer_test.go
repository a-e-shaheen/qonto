package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"qonto-bulk-transfer/internal/transfer/model"
	"qonto-bulk-transfer/internal/transfer/service/mocks"
)

// runAtomicDirectly wires the mocked Transactor to invoke fn immediately with the
// same context, standing in for pkg/txn's real transaction wrapping — that wrapping
// is exercised for real in the integration tests, not here.
func runAtomicDirectly(txnMock *mocks.MockTransactor) {
	txnMock.EXPECT().
		Atomic(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})
}

func sampleTransfer(cents int64) model.CreditTransfer {
	return model.CreditTransfer{
		AmountCents:      cents,
		Currency:         "EUR",
		CounterpartyName: "Bip Bip",
		CounterpartyBIC:  "CRLYFRPPTOU",
		CounterpartyIBAN: "EE383680981021245685",
		Description:      "Wonderland/4410",
	}
}

var testAccount = model.Account{
	ID:           1,
	BalanceCents: 10_000_000, // the seeded €100,000 balance
	IBAN:         "FR10474608000002006107XXXXX",
	BIC:          "OIVUSCLQXXX",
}

var errInfra = errors.New("connection refused")

func TestSubmitBulkTransfer(t *testing.T) {
	tests := []struct {
		name       string
		req        model.BulkTransferRequest
		mockSetup  func(repo *mocks.MockRepository)
		wantStatus int
		wantBody   string // empty means the response has no body
		wantErr    error
	}{
		{
			name: "replays a known created outcome without touching the account",
			req:  model.BulkTransferRequest{IdempotencyKey: "key-1"},
			mockSetup: func(repo *mocks.MockRepository) {
				repo.EXPECT().AdvisoryLock(mock.Anything, "key-1").Return(nil)
				repo.EXPECT().FindIdempotencyRecordStatus(mock.Anything, "key-1").Return(StatusCreated, true, nil)
			},
			wantStatus: StatusCreated,
		},
		{
			name: "replays a known denied outcome with its fixed body",
			req:  model.BulkTransferRequest{IdempotencyKey: "key-2"},
			mockSetup: func(repo *mocks.MockRepository) {
				repo.EXPECT().AdvisoryLock(mock.Anything, "key-2").Return(nil)
				repo.EXPECT().FindIdempotencyRecordStatus(mock.Anything, "key-2").Return(StatusInsufficientFunds, true, nil)
			},
			wantStatus: StatusInsufficientFunds,
			wantBody:   `{"error":"insufficient funds"}`,
		},
		{
			name: "unknown account is denied and recorded",
			req: model.BulkTransferRequest{
				IdempotencyKey:   "key-3",
				OrganizationIBAN: "FR0000000000000000000000000",
				OrganizationBIC:  "UNKNOWNBIC",
				CreditTransfers:  []model.CreditTransfer{sampleTransfer(1450)},
			},
			mockSetup: func(repo *mocks.MockRepository) {
				repo.EXPECT().AdvisoryLock(mock.Anything, "key-3").Return(nil)
				repo.EXPECT().FindIdempotencyRecordStatus(mock.Anything, "key-3").Return(0, false, nil)
				repo.EXPECT().
					FindAccountForUpdate(mock.Anything, "FR0000000000000000000000000", "UNKNOWNBIC").
					Return(model.Account{}, false, nil)
				repo.EXPECT().RecordIdempotency(mock.Anything, "key-3", StatusAccountNotFound).Return(nil)
			},
			wantStatus: StatusAccountNotFound,
			wantBody:   `{"error":"account not found"}`,
		},
		{
			name: "insufficient funds is denied and recorded (sample2.json's total vs. the seeded balance)",
			req: model.BulkTransferRequest{
				IdempotencyKey:   "key-4",
				OrganizationIBAN: testAccount.IBAN,
				OrganizationBIC:  testAccount.BIC,
				CreditTransfers:  []model.CreditTransfer{sampleTransfer(10_648_216)},
			},
			mockSetup: func(repo *mocks.MockRepository) {
				repo.EXPECT().AdvisoryLock(mock.Anything, "key-4").Return(nil)
				repo.EXPECT().FindIdempotencyRecordStatus(mock.Anything, "key-4").Return(0, false, nil)
				repo.EXPECT().FindAccountForUpdate(mock.Anything, testAccount.IBAN, testAccount.BIC).Return(testAccount, true, nil)
				repo.EXPECT().RecordIdempotency(mock.Anything, "key-4", StatusInsufficientFunds).Return(nil)
			},
			wantStatus: StatusInsufficientFunds,
			wantBody:   `{"error":"insufficient funds"}`,
		},
		{
			name: "sufficient funds creates the batch and debits the account (sample1.json's total)",
			req: model.BulkTransferRequest{
				IdempotencyKey:   "key-5",
				OrganizationIBAN: testAccount.IBAN,
				OrganizationBIC:  testAccount.BIC,
				CreditTransfers: []model.CreditTransfer{
					sampleTransfer(1450), sampleTransfer(6_123_800), sampleTransfer(99_900),
				},
			},
			mockSetup: func(repo *mocks.MockRepository) {
				const total int64 = 1450 + 6_123_800 + 99_900
				transfers := []model.CreditTransfer{
					sampleTransfer(1450), sampleTransfer(6_123_800), sampleTransfer(99_900),
				}
				repo.EXPECT().AdvisoryLock(mock.Anything, "key-5").Return(nil)
				repo.EXPECT().FindIdempotencyRecordStatus(mock.Anything, "key-5").Return(0, false, nil)
				repo.EXPECT().FindAccountForUpdate(mock.Anything, testAccount.IBAN, testAccount.BIC).Return(testAccount, true, nil)
				repo.EXPECT().InsertBatch(mock.Anything, testAccount.ID, "key-5", total).Return(int64(99), nil)
				repo.EXPECT().InsertTransactions(mock.Anything, testAccount.ID, int64(99), transfers).Return(nil)
				repo.EXPECT().DebitAccount(mock.Anything, testAccount.ID, total).Return(testAccount.BalanceCents-total, nil)
				repo.EXPECT().RecordIdempotency(mock.Anything, "key-5", StatusCreated).Return(nil)
			},
			wantStatus: StatusCreated,
		},
		{
			name: "an infrastructure failure propagates as an error, not a Result",
			req:  model.BulkTransferRequest{IdempotencyKey: "key-6"},
			mockSetup: func(repo *mocks.MockRepository) {
				repo.EXPECT().AdvisoryLock(mock.Anything, "key-6").Return(errInfra)
			},
			wantErr: errInfra,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := mocks.NewMockRepository(t)
			txnMock := mocks.NewMockTransactor(t)
			runAtomicDirectly(txnMock)
			tt.mockSetup(repo)

			svc := New(repo, txnMock)
			result, err := svc.SubmitBulkTransfer(context.Background(), tt.req)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, result.StatusCode)
			if tt.wantBody == "" {
				assert.Nil(t, result.Body)
			} else {
				assert.JSONEq(t, tt.wantBody, string(result.Body))
			}
		})
	}
}
