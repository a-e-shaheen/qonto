package model

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"time"
)

// TransactionHistoryItem is one row of an account's transaction ledger, as returned
// by the pagination endpoint.
type TransactionHistoryItem struct {
	ID               int64
	CounterpartyName string
	CounterpartyIBAN string
	CounterpartyBIC  string
	AmountCents      int64
	Currency         string
	Description      string
	CreatedAt        time.Time
}

// Cursor identifies a position in the transaction history, ordered by
// (created_at, id) descending.
type Cursor struct {
	CreatedAt time.Time
	ID        int64
}

const cursorByteLen = 16 // 8 bytes created_at (unix nanos) + 8 bytes id

// EncodeCursor packs (created_at, id) into a fixed 16-byte little-endian buffer and
// base64url-encodes it. No delimiters, no text round-trip through RFC3339 — just two
// int64s in, two int64s out, with a length check on decode instead of format parsing.
func EncodeCursor(c Cursor) string {
	buf := make([]byte, cursorByteLen)
	binary.LittleEndian.PutUint64(buf[0:8], uint64(c.CreatedAt.UnixNano()))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(c.ID))
	return base64.RawURLEncoding.EncodeToString(buf)
}

// DecodeCursor parses a cursor string produced by EncodeCursor. An empty string
// decodes to the zero Cursor, representing "start from the most recent transaction".
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	buf, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("invalid cursor: %w", err)
	}
	if len(buf) != cursorByteLen {
		return Cursor{}, fmt.Errorf("invalid cursor: wrong length")
	}
	nanos := int64(binary.LittleEndian.Uint64(buf[0:8]))
	id := int64(binary.LittleEndian.Uint64(buf[8:16]))
	return Cursor{CreatedAt: time.Unix(0, nanos).UTC(), ID: id}, nil
}
