package proxy_test

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorokin-vladimir/tele/internal/proxy"
)

// The three shapes a secret comes in, as a proxy publishes them: sixteen bytes,
// those sixteen behind a transport tag, and a tagged secret with the domain the
// connection is disguised as stuck on the end.
const (
	plainHex = "0123456789abcdef0123456789abcdef"
	ddHex    = "dd" + plainHex
	eeHex    = "ee" + plainHex + "6578616d706c652e636f6d" // example.com
)

func TestParseSecret_Kinds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       string
		wantKind string
		wantLen  int
	}{
		{"plain hex", plainHex, proxy.SecretPlain, 16},
		{"dd hex", ddHex, proxy.SecretDD, 17},
		{"ee hex with a cloak domain", eeHex, proxy.SecretEE, 28},
		{"uppercase hex is the same secret", strings.ToUpper(plainHex), proxy.SecretPlain, 16},
		{"surrounding whitespace is not part of it", "  " + ddHex + "\n", proxy.SecretDD, 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := proxy.ParseSecret(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.wantKind, got.Kind)
			assert.Len(t, got.Bytes, tc.wantLen)
		})
	}
}

// A t.me/proxy link carries the same secret in base64url, and that is what gets
// copied out of a chat. Reading only hex would refuse a secret that is perfectly
// good and ask somebody to transcode a credential by hand.
func TestParseSecret_ReadsBase64url(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hex      string
		wantKind string
	}{
		{"plain", plainHex, proxy.SecretPlain},
		{"ee with a cloak domain", eeHex, proxy.SecretEE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := hex.DecodeString(tc.hex)
			require.NoError(t, err)

			for encName, encoded := range map[string]string{
				"unpadded": base64.RawURLEncoding.EncodeToString(raw),
				"padded":   base64.URLEncoding.EncodeToString(raw),
			} {
				got, err := proxy.ParseSecret(encoded)
				require.NoError(t, err, encName)
				assert.Equal(t, tc.wantKind, got.Kind, encName)
				assert.Equal(t, raw, got.Bytes, encName)
			}
		})
	}
}

// Every refusal ends up on a terminal in front of somebody who has no tele and
// possibly no other way to reach Telegram, so each one says which key it is
// about and what is wrong with it.
func TestParseSecret_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name, in, says string
	}{
		{"empty", "", "proxy.secret"},
		{"neither spelling", "not a secret at all!!", "neither hex nor base64url"},
		{"hex with an odd number of characters", plainHex[:31], "hex comes in pairs"},
		// Hex and base64url share an alphabet, so a hex secret with characters
		// missing decodes as base64url without complaint. It is refused for
		// what it is - a short hex secret - rather than read as another
		// spelling that happens to work.
		{"too short", plainHex[:30], "decodes to 15 bytes"},
		{"tagged with a tag that names no transport", "ab" + plainHex, "0xab"},
		{"a domain behind the wrong tag", "dd" + plainHex + "6578616d706c65", "rather than 0xee"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := proxy.ParseSecret(tc.in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.says)
		})
	}
}

// The secret is handed to gotd exactly as it was written, prefix and domain
// included: gotd reads the shape out of it again to choose the obfuscation, so
// anything trimmed here would change how the connection is disguised.
func TestParseSecret_KeepsEveryByteForGotd(t *testing.T) {
	want, err := hex.DecodeString(eeHex)
	require.NoError(t, err)

	got, err := proxy.ParseSecret(eeHex)
	require.NoError(t, err)
	assert.Equal(t, want, got.Bytes)
}
