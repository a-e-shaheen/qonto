package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAmountCents(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{name: "whole number", input: "999", want: 99900},
		{name: "two decimals", input: "14.50", want: 1450},
		{name: "one decimal padded", input: "13.2", want: 1320},
		{name: "sample1.json amount", input: "61238", want: 6_123_800},
		{name: "sample2.json amount", input: "8024.99", want: 802_499},
		{name: "zero is not positive", input: "0", wantErr: true},
		{name: "negative sign rejected", input: "-5", wantErr: true},
		{name: "three decimals rejected", input: "1.234", wantErr: true},
		{name: "non numeric rejected", input: "abc", wantErr: true},
		{name: "empty string rejected", input: "", wantErr: true},
		{name: "trailing dot rejected", input: "5.", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAmountCents(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatAmountCents(t *testing.T) {
	assert.Equal(t, "14.50", FormatAmountCents(1450))
	assert.Equal(t, "999.00", FormatAmountCents(99900))
	assert.Equal(t, "-10.00", FormatAmountCents(-1000))
}

func TestParseAmountCents_FormatAmountCents_RoundTrip(t *testing.T) {
	tests := map[string]string{ // input -> FormatAmountCents(ParseAmountCents(input))
		"14.50":   "14.50",
		"8024.99": "8024.99",
		"0.01":    "0.01",
		"61238":   "61238.00", // FormatAmountCents always shows two decimals
		"999":     "999.00",
	}
	for input, want := range tests {
		cents, err := ParseAmountCents(input)
		require.NoError(t, err)
		assert.Equal(t, want, FormatAmountCents(cents))
	}
}
