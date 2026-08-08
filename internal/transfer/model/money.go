package model

import (
	"fmt"
	"strconv"
	"strings"
)

var amountFormatError = "invalid amount format: %q (expected a positive number with at most 2 decimal places)"

// ParseAmountCents converts a euro-amount string (e.g. "13.22", "999") into integer
// cents. It works on the string's digits directly — never through
// strconv.ParseFloat — so there is no floating-point rounding step where a cent
// could be lost or gained.
func ParseAmountCents(s string) (int64, error) {
	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" || !isDigits(whole) {
		return 0, fmt.Errorf(amountFormatError, s)
	}
	if hasFrac && (len(frac) == 0 || len(frac) > 2 || !isDigits(frac)) {
		return 0, fmt.Errorf(amountFormatError, s)
	}

	wholeCents, err := strconv.ParseInt(whole, 10, 63)
	if err != nil {
		return 0, fmt.Errorf(amountFormatError, s)
	}

	cents := wholeCents * 100
	if hasFrac {
		for len(frac) < 2 {
			frac += "0"
		}
		fracCents, err := strconv.ParseInt(frac, 10, 63)
		if err != nil {
			return 0, fmt.Errorf(amountFormatError, s)
		}
		cents += fracCents
	}

	if cents <= 0 {
		return 0, fmt.Errorf("amount must be positive: %q", s)
	}
	return cents, nil
}

// FormatAmountCents renders integer cents back into a euro-amount string in the
// same shape ParseAmountCents accepts, preserving the sign for debit rows.
func FormatAmountCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
