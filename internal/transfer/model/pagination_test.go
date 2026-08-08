package model

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorRoundTrip(t *testing.T) {
	c := Cursor{CreatedAt: time.Date(2026, 8, 7, 12, 0, 0, 123_456_000, time.UTC), ID: 42}

	decoded, err := DecodeCursor(EncodeCursor(c))
	require.NoError(t, err)

	assert.True(t, c.CreatedAt.Equal(decoded.CreatedAt), "created_at should survive the round trip exactly")
	assert.Equal(t, c.ID, decoded.ID)
}

func TestDecodeCursor_EmptyMeansStartFromMostRecent(t *testing.T) {
	c, err := DecodeCursor("")
	require.NoError(t, err)
	assert.True(t, c.CreatedAt.IsZero())
	assert.Zero(t, c.ID)
}

func TestDecodeCursor_InvalidBase64(t *testing.T) {
	_, err := DecodeCursor("not-valid-base64!!!")
	assert.Error(t, err)
}

func TestDecodeCursor_WrongDecodedLength(t *testing.T) {
	tooShort := base64.RawURLEncoding.EncodeToString([]byte("short"))
	_, err := DecodeCursor(tooShort)
	assert.Error(t, err)
}
