package seed

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// These guard the fixture data itself, not the Seed method's DB interaction — if
// someone edits the literals above and breaks internal consistency, this catches it
// without needing a database.

func TestSeedData_TransactionsNetToAccountBalance(t *testing.T) {
	var net int64
	for _, tr := range seedTransactions {
		net += tr.AmountCents
	}
	assert.Equal(t, seedAccount.BalanceCents, net,
		"seed transactions must net to the seed account's balance, or the fixture is internally inconsistent")
}

func TestSeedData_CurrencyIsAlwaysEUR(t *testing.T) {
	for _, tr := range seedTransactions {
		assert.Equal(t, "EUR", tr.AmountCurrency)
	}
}

func TestSeedData_AccountIdentifiersMatchSourceSqlite(t *testing.T) {
	assert.Equal(t, "FR10474608000002006107XXXXX", seedAccount.IBAN)
	assert.Equal(t, "OIVUSCLQXXX", seedAccount.BIC)
}
