// Package model holds transport-agnostic domain types for the bulk-transfer
// feature. Nothing here knows about JSON, HTTP, or SQL.
package model

// CreditTransfer is one entry of a bulk transfer request, already validated and
// converted to integer cents.
type CreditTransfer struct {
	AmountCents      int64
	Currency         string
	CounterpartyName string
	CounterpartyBIC  string
	CounterpartyIBAN string
	Description      string
}

// BulkTransferRequest is a fully parsed and validated bulk transfer request, ready
// for the service layer. IdempotencyKey is always populated by the time the service
// sees it — either from the client's Idempotency-Key header or, if absent, a hash of
// the canonicalized request body.
type BulkTransferRequest struct {
	OrganizationBIC  string
	OrganizationIBAN string
	CreditTransfers  []CreditTransfer
	IdempotencyKey   string
}

// TotalCents sums every transfer's amount. Bulk requests are always non-empty and
// every amount positive by the time a BulkTransferRequest exists, so overflow aside,
// the result is always > 0.
func (r BulkTransferRequest) TotalCents() int64 {
	var total int64
	for _, t := range r.CreditTransfers {
		total += t.AmountCents
	}
	return total
}

// Account is a bank_accounts row.
type Account struct {
	ID               int64
	OrganizationName string
	BalanceCents     int64
	IBAN             string
	BIC              string
}
