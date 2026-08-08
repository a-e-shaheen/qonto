package server

import (
	"github.com/go-playground/validator/v10"

	"qonto-bulk-transfer/internal/transfer/model"
)

// newValidator builds a validator with the "amount" tag registered — the same
// format model.ParseAmountCents itself enforces, so a request that passes
// structural validation is guaranteed to parse cleanly in the service layer too.
func newValidator() *validator.Validate {
	v := validator.New()
	_ = v.RegisterValidation("amount", validateAmount)
	return v
}

func validateAmount(fl validator.FieldLevel) bool {
	_, err := model.ParseAmountCents(fl.Field().String())
	return err == nil
}
