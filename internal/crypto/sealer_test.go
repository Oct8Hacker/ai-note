package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

func testKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 32)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(b)
}

func TestSealRoundTrip(t *testing.T) {
	s, err := NewSealer(testKey(t))
	require.NoError(t, err)

	a, err := s.Seal("secret_token")
	require.NoError(t, err)
	b, err := s.Seal("secret_token")
	require.NoError(t, err)
	require.NotEqual(t, a, b)
	require.NotContains(t, a, "secret_token")

	got, err := s.Open(a)
	require.NoError(t, err)
	require.Equal(t, "secret_token", got)

	raw, _ := base64.StdEncoding.DecodeString(a)
	raw[len(raw)-1] ^= 0xff
	_, err = s.Open(base64.StdEncoding.EncodeToString(raw))
	require.Error(t, err)
	_, err = s.Open("!!notbase64")
	require.Error(t, err)
	_, err = s.Open("")
	require.Error(t, err)
}

func TestNewSealerBadKey(t *testing.T) {
	_, err := NewSealer("not base64!")
	require.Error(t, err)
	_, err = NewSealer(base64.StdEncoding.EncodeToString([]byte("short")))
	require.Error(t, err)
}
