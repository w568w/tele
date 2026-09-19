package appkey

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The key itself is never written down here. What these tests pin is its shape,
// which is what a fragment lost in an edit would break: a truncated or
// reordered set decodes into something that is not an id and not a hash.
func TestPublishedHasTheShapeOfAnAppKey(t *testing.T) {
	id, hash := Published()

	assert.Greater(t, id, 0, "api_id is not a positive number")

	require.Len(t, hash, 32, "api_hash is not 32 characters")
	_, err := hex.DecodeString(hash)
	assert.NoError(t, err, "api_hash is not hexadecimal")
	assert.Equal(t, strings.ToLower(hash), hash, "api_hash is not lowercase")
}

func TestPublishedIsStable(t *testing.T) {
	firstID, firstHash := Published()
	secondID, secondHash := Published()

	assert.Equal(t, firstID, secondID)
	assert.Equal(t, firstHash, secondHash)
}

// A fragment lost or reordered has to be visible here rather than at a login
// screen: the tests above check the assembled key, this one checks that the
// assembly is what produced it.
func TestJoinDependsOnEveryFragment(t *testing.T) {
	_, whole := Published()

	assert.Empty(t, join([]string{"not base64 at all"}), "invalid base64 is not rejected")

	short := join(hashFragments[:len(hashFragments)-1])
	assert.NotEqual(t, whole, short, "dropping a fragment left the hash unchanged")
	assert.NotEqual(t, 32, len(short), "a partial hash still has the length of a whole one")

	reversed := make([]string, len(hashFragments))
	for i, fragment := range hashFragments {
		reversed[len(hashFragments)-1-i] = fragment
	}
	assert.NotEqual(t, whole, join(reversed), "fragment order does not matter")
}
