package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-playground/validator/v10"
)

type errorResponse struct {
	Error   string   `json:"error"`
	Details []string `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string, details ...string) {
	writeJSON(w, status, errorResponse{Error: msg, Details: details})
}

// formatValidationError turns go-playground/validator's ValidationErrors into a
// short summary plus one "field: rule" line per failing field, for the response
// body's details array.
func formatValidationError(err error) (string, []string) {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return "validation failed", nil
	}
	details := make([]string, 0, len(verrs))
	for _, fe := range verrs {
		details = append(details, fmt.Sprintf("%s: failed %q validation", fe.Namespace(), fe.Tag()))
	}
	return "validation failed", details
}
