package proxy

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// Secret kinds, named the way people name them when they hand each other a
// proxy: a bare secret, a dd one, an ee one. The kind is the disguise the
// connection wears, so it is worth saying out loud in the log - it is the part
// people get wrong - while the secret itself never is.
const (
	SecretPlain = "plain"
	SecretDD    = "dd"
	SecretEE    = "ee"
)

// secretLength is the length of the secret proper, which every kind carries.
// The kinds differ in what they put around it: nothing, a one-byte transport
// tag, or that tag plus the domain the connection pretends to be talking to.
const secretLength = 16

// Transport tags an MTProto secret can carry. They are the first byte of a dd
// or ee secret and say which transport codec the proxy expects; gotd reads the
// same byte and builds the codec from it.
const (
	tagAbridged           = 0xef
	tagIntermediate       = 0xee
	tagPaddedIntermediate = 0xdd
)

// Secret is what an MTProto proxy asks a client to prove it knows.
//
// Bytes is handed to gotd as it came off the wire, prefix and cloak domain
// included: gotd reads the shape out of it again and builds the obfuscator
// from what it finds. Kind is what we read out of it, and it exists for the
// log line and for the refusal message, neither of which may name the secret.
type Secret struct {
	Bytes []byte
	Kind  string
}

// ParseSecret reads a secret as it is written in the config.
//
// A secret travels in two spellings and people copy whichever they were given.
// A tg://proxy link and most proxy bots write hex; a t.me/proxy link writes
// base64url. Both are accepted: refusing the one a person happens to hold would
// be refusing a secret that is perfectly good, and telling them to transcode it
// by hand is asking them to retype a credential.
//
// Which spelling a secret is in is decided by what it is made of rather than by
// trying both and seeing which one produces something: a string of nothing but
// hex digits is hex, and everything else is base64url. Reading it the other way
// round is how a mistyped hex secret gets quietly accepted as a base64url one -
// the character sets overlap, and a wrong-length hex string decodes as base64
// perfectly happily.
func ParseSecret(s string) (Secret, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Secret{}, fmt.Errorf("proxy.secret is empty; an mtproto proxy is reached with the secret it was published with")
	}

	raw, err := decodeSecret(s)
	if err != nil {
		return Secret{}, err
	}

	switch {
	case len(raw) < secretLength:
		return Secret{}, fmt.Errorf("proxy.secret decodes to %d bytes; a secret is %d bytes, one more with a transport tag, or longer still with the domain it is disguised as",
			len(raw), secretLength)
	case len(raw) == secretLength:
		return Secret{Bytes: raw, Kind: SecretPlain}, nil
	case len(raw) == secretLength+1:
		if err := checkTag(raw[0]); err != nil {
			return Secret{}, err
		}
		return Secret{Bytes: raw, Kind: SecretDD}, nil
	default:
		// Longer than a tagged secret: the tail is the domain the connection is
		// disguised as, which is what makes it an ee secret. Only the
		// intermediate tag appears here in practice - it is the byte the ee
		// spelling is named after - and a different one would mean the secret
		// was assembled by hand out of parts that do not go together.
		if raw[0] != tagIntermediate {
			return Secret{}, fmt.Errorf("proxy.secret carries a domain, which makes it an ee secret, but it starts with %#02x rather than 0xee", raw[0])
		}
		return Secret{Bytes: raw, Kind: SecretEE}, nil
	}
}

// decodeSecret turns the written secret into bytes. The spelling is read off
// the characters, so a secret that is hex and wrong is refused as hex rather
// than accepted as something else.
func decodeSecret(s string) ([]byte, error) {
	if isHex(s) {
		if len(s)%2 != 0 {
			return nil, fmt.Errorf("proxy.secret is written in hex and has %d characters; hex comes in pairs", len(s))
		}
		return hex.DecodeString(s)
	}
	// Padded or not, and the standard alphabet as well: a secret gets pasted
	// out of places that disagree about which base64 they write, and all four
	// spellings mean the same bytes.
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if raw, err := enc.DecodeString(s); err == nil {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("proxy.secret is neither hex nor base64url; it is what a proxy publishes, copied whole")
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

func checkTag(tag byte) error {
	switch tag {
	case tagAbridged, tagIntermediate, tagPaddedIntermediate:
		return nil
	default:
		return fmt.Errorf("proxy.secret starts with %#02x, which names no transport; a tagged secret starts with 0xdd, 0xee or 0xef", tag)
	}
}
