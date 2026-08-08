package observability

import "context"

type phaseKey struct{}

// WithPhase attaches a short label (e.g. "check_idempotency") to ctx. pkg/database's
// QueryTracer picks it up to prefix each query's span name — so a trace waterfall
// shows which business step a query belongs to (e.g. "check_idempotency:
// postgres.query") without adding a separate span level for that step. Reassign
// ctx with a new phase before each section of a multi-step function; the label
// applies to whichever queries run until the next WithPhase call.
func WithPhase(ctx context.Context, phase string) context.Context {
	return context.WithValue(ctx, phaseKey{}, phase)
}

// PhaseFromContext returns the phase attached by WithPhase, if any.
func PhaseFromContext(ctx context.Context) (string, bool) {
	phase, ok := ctx.Value(phaseKey{}).(string)
	return phase, ok
}
