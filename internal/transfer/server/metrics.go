package server

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"qonto-bulk-transfer/internal/transfer/model"
	"qonto-bulk-transfer/internal/transfer/service"
	"qonto-bulk-transfer/pkg/observability"
)

// businessMetrics holds the financial/business instruments from the metrics spec:
// bulk_transfers_total, transfer_amount_cents_total, bulk_transfer_batch_size.
// balance_invariant_violations_total lives in internal/transfer/service instead —
// it's a database invariant check, not something the handler can observe.
type businessMetrics struct {
	bulkTransfersTotal       metric.Int64Counter
	transferAmountCentsTotal metric.Int64Counter
	batchSize                metric.Int64Histogram
}

var bizMetrics = newBusinessMetrics()

func newBusinessMetrics() businessMetrics {
	m := observability.Meter()

	bulkTransfersTotal, _ := m.Int64Counter("bulk_transfers_total",
		metric.WithDescription("Bulk transfer requests by business outcome"))
	transferAmountCentsTotal, _ := m.Int64Counter("transfer_amount_cents_total",
		metric.WithDescription("Total amount, in cents, of successfully applied bulk transfers"))
	batchSize, _ := m.Int64Histogram("bulk_transfer_batch_size",
		metric.WithDescription("Number of individual transfers per bulk transfer request"))

	return businessMetrics{
		bulkTransfersTotal:       bulkTransfersTotal,
		transferAmountCentsTotal: transferAmountCentsTotal,
		batchSize:                batchSize,
	}
}

// recordRejection increments bulk_transfers_total for a request that never
// reached the service — a transport-level validation failure. The requested
// status buckets are success|insufficient_funds|invalid_payload|server_error;
// this is exactly the invalid_payload case.
func recordRejection(ctx context.Context) {
	bizMetrics.bulkTransfersTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("status", "invalid_payload")))
}

// recordOutcome records the batch size for every request the service actually
// evaluated, bulk_transfers_total for its outcome, and — only when the transfer
// was actually applied — the total amount moved (this must only count real,
// settled money movement, not attempted-but-rejected amounts).
func recordOutcome(ctx context.Context, req model.BulkTransferRequest, result service.Result, err error) {
	bizMetrics.batchSize.Record(ctx, int64(len(req.CreditTransfers)))

	status := "server_error"
	switch {
	case err != nil:
		status = "server_error"
	case result.StatusCode == http.StatusNoContent:
		status = "success"
	case result.StatusCode == http.StatusUnprocessableEntity:
		status = "insufficient_funds"
	case result.StatusCode == http.StatusNotFound:
		// Extension beyond the spec's four buckets: account-not-found is a real,
		// distinct outcome in this API. Folding it into "invalid_payload" would
		// hide 404s from a dashboard filtered by status — see README.
		status = "account_not_found"
	}
	bizMetrics.bulkTransfersTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))

	if result.StatusCode == http.StatusNoContent {
		bizMetrics.transferAmountCentsTotal.Add(ctx, req.TotalCents())
	}
}
