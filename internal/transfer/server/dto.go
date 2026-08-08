// Package server is the HTTP transport layer for the bulk-transfer feature: JSON
// decoding, structural validation (go-playground/validator), routing, and error →
// status-code mapping. Business rules live in internal/transfer/service — this
// layer never touches SQL or decides whether funds are sufficient.
package server

// creditTransferDTO mirrors one entry of the OpenAPI contract's credit_transfers
// array. Validation tags cover shape only; ParseAmountCents (via the "amount" tag)
// also covers format, but sufficiency-of-funds and everything else business-rule
// related is the service's job, not this struct's.
type creditTransferDTO struct {
	Amount           string `json:"amount" validate:"required,amount"`
	Currency         string `json:"currency" validate:"required,oneof=EUR"`
	CounterpartyName string `json:"counterparty_name" validate:"required"`
	CounterpartyBIC  string `json:"counterparty_bic" validate:"required"`
	CounterpartyIBAN string `json:"counterparty_iban" validate:"required"`
	Description      string `json:"description"`
}

// bulkTransferRequestDTO mirrors the OpenAPI contract's request body.
type bulkTransferRequestDTO struct {
	OrganizationBIC  string              `json:"organization_bic" validate:"required"`
	OrganizationIBAN string              `json:"organization_iban" validate:"required"`
	CreditTransfers  []creditTransferDTO `json:"credit_transfers" validate:"required,min=1,dive"`
}
