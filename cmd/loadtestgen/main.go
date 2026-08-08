// Command loadtestgen generates vegeta JSON-lines target files for
// `make load-test` (see the Makefile's load-test target for how it's invoked).
//
// Written in Go rather than a one-off script in another language: this is a
// Go project, and pulling in a second language toolchain for a small
// dev-tooling script would mean anyone touching it needs to know Go *and*
// whatever else, instead of `go run` being the only thing required.
//
// Spreads requests round-robin across N accounts seeded by
// scripts/seed-load-test-accounts.sql, each POST with its own distinct
// Idempotency-Key, so the load test exercises real concurrent write
// throughput instead of every request past the first colliding on one shared
// account's balance (or, if the same run ID were reused, on one shared
// idempotency key — see the --run-id flag).
//
// vegeta's plain HTTP target format takes the request body from a file
// (`@path`), which would mean one file per distinct body. Its JSON-lines
// format instead lets each line carry its own base64-encoded body inline, so
// this generates everything into a single target file per endpoint.
package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

type creditTransfer struct {
	Amount           string `json:"amount"`
	Currency         string `json:"currency"`
	CounterpartyName string `json:"counterparty_name"`
	CounterpartyBIC  string `json:"counterparty_bic"`
	CounterpartyIBAN string `json:"counterparty_iban"`
	Description      string `json:"description"`
}

type bulkTransferBody struct {
	OrganizationBIC  string           `json:"organization_bic"`
	OrganizationIBAN string           `json:"organization_iban"`
	CreditTransfers  []creditTransfer `json:"credit_transfers"`
}

// vegetaTarget mirrors vegeta's JSON target format. Body is base64-encoded
// text here for the same reason Go's own encoding/json would produce that if
// this were a []byte field: vegeta decodes it the same way on its end.
type vegetaTarget struct {
	Method string              `json:"method"`
	URL    string              `json:"url"`
	Header map[string][]string `json:"header,omitempty"`
	Body   string              `json:"body,omitempty"`
}

func main() {
	runID := flag.String("run-id", "", "unique per invocation (e.g. a timestamp) — reusing one across runs replays old idempotency results instead of evaluating fresh requests")
	requests := flag.Int("requests", 0, "number of POST/GET target pairs to generate")
	accounts := flag.Int("accounts", 0, "number of seeded load-test accounts to round-robin across")
	accountIDsFile := flag.String("account-ids-file", "", "file with one real bank_accounts.id per line, for GET /accounts/{id}/transactions")
	transferCents := flag.Int("transfer-cents", 0, "amount of each individual transfer, in cents")
	baseURL := flag.String("url", "http://localhost:8080", "base URL of the running app")
	postOut := flag.String("post-out", "", "output path for POST /transfers/bulk targets")
	getOut := flag.String("get-out", "", "output path for GET /accounts/{id}/transactions targets")
	flag.Parse()

	if *runID == "" || *requests <= 0 || *accounts <= 0 || *accountIDsFile == "" ||
		*transferCents <= 0 || *postOut == "" || *getOut == "" {
		flag.Usage()
		os.Exit(2)
	}

	accountIDs, err := readLines(*accountIDsFile)
	if err != nil {
		log.Fatalf("read account ids file: %v", err)
	}
	if len(accountIDs) == 0 {
		log.Fatalf("account ids file %q is empty", *accountIDsFile)
	}

	if err := writePostTargets(*postOut, *runID, *requests, *accounts, *transferCents, *baseURL); err != nil {
		log.Fatalf("write post targets: %v", err)
	}
	if err := writeGetTargets(*getOut, *requests, accountIDs, *baseURL); err != nil {
		log.Fatalf("write get targets: %v", err)
	}
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

func writePostTargets(path, runID string, requests, accounts, transferCents int, baseURL string) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	enc := json.NewEncoder(f)
	amount := fmt.Sprintf("%d.%02d", transferCents/100, transferCents%100)

	for i := range requests {
		account := (i % accounts) + 1
		body := bulkTransferBody{
			OrganizationBIC:  fmt.Sprintf("LOADTST%04d", account),
			OrganizationIBAN: fmt.Sprintf("FRLOADTEST%017d", account),
			CreditTransfers: []creditTransfer{{
				Amount:           amount,
				Currency:         "EUR",
				CounterpartyName: "Load Test Counterparty",
				CounterpartyBIC:  "CRLYFRPPTOU",
				CounterpartyIBAN: "EE383680981021245685",
				Description:      "load test",
			}},
		}
		bodyJSON, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body for request %d: %w", i, err)
		}

		target := vegetaTarget{
			Method: "POST",
			URL:    baseURL + "/transfers/bulk",
			Header: map[string][]string{
				"Content-Type":    {"application/json"},
				"Idempotency-Key": {fmt.Sprintf("load-test-%s-%d", runID, i)},
			},
			Body: base64.StdEncoding.EncodeToString(bodyJSON),
		}
		if err := enc.Encode(target); err != nil {
			return fmt.Errorf("encode target for request %d: %w", i, err)
		}
	}
	return nil
}

func writeGetTargets(path string, requests int, accountIDs []string, baseURL string) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	enc := json.NewEncoder(f)
	for i := range requests {
		accountID := accountIDs[i%len(accountIDs)]
		target := vegetaTarget{
			Method: "GET",
			URL:    fmt.Sprintf("%s/accounts/%s/transactions?limit=20", baseURL, accountID),
		}
		if err := enc.Encode(target); err != nil {
			return fmt.Errorf("encode target for request %d: %w", i, err)
		}
	}
	return nil
}
