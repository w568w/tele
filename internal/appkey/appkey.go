// Package appkey holds the Telegram app key that a build from published source
// runs on: homebrew-core, the Nix flake, a BSD port, anyone's own `go build`.
//
// It is the last of three steps. A key in the person's config wins over
// everything; an official build then uses the one the release pipeline injects
// through ldflags; only a build with neither reaches this package. The key here
// is therefore the same for everyone who compiles tele themselves, which is
// what lets `brew install tele` reach the login screen instead of exiting with
// instructions to go register an application first.
//
// An app key is the id and the hash together. Published hands over both or
// neither: half of one key and half of another authenticates nothing.
package appkey

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// The two halves of the key, each kept in fragments and put back together only
// in Published.
var (
	idFragments   = []string{"MzQy", "NTA2", "NDc="}
	hashFragments = []string{"NDUzNGJhZ", "DI2OGMxZD", "I5OGUwMDZ", "jY2IzYzA4", "YWJlYjU="}
)

// Published returns the app key for builds from source: the api_id and the
// api_hash, in that order.
//
// It returns 0 and "" if the fragments cannot be put back together, which
// leaves the caller on the path it would have taken before this package
// existed - telling the person to supply their own key - rather than sending a
// nonsense one to Telegram.
func Published() (int, string) {
	hash := join(hashFragments)
	if hash == "" {
		return 0, ""
	}
	id, err := strconv.Atoi(join(idFragments))
	if err != nil || id == 0 {
		return 0, ""
	}
	return id, hash
}

// join concatenates the fragments and decodes the result.
func join(fragments []string) string {
	decoded, err := base64.StdEncoding.DecodeString(strings.Join(fragments, ""))
	if err != nil {
		return ""
	}
	return string(decoded)
}
