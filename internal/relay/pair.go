package relay

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// pairAlphabet omits 0/O and 1/I which are easy to confuse when typed.
const pairAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// newPairCode returns an 8-character code from pairAlphabet.
func newPairCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Errorf("relay: crypto/rand: %w", err))
	}
	out := make([]byte, 8)
	for i, v := range b {
		out[i] = pairAlphabet[int(v)%len(pairAlphabet)]
	}
	return string(out)
}

// normalizePairCode uppercases and strips separators so "a7k3-9qzp" matches.
func normalizePairCode(c string) string {
	c = strings.ToUpper(strings.TrimSpace(c))
	c = strings.NewReplacer("-", "", " ", "").Replace(c)
	return c
}

// newDeviceToken returns the plaintext token and its sha256 hex hash.
func newDeviceToken() (token, hash string) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic(fmt.Errorf("relay: crypto/rand: %w", err))
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// fingerprint returns the first 8 base32 characters of sha256(material),
// formatted as XXXX-XXXX (e.g. A7K3-9QZP).
func fingerprint(material string) string {
	h := sha256.Sum256([]byte(material))
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(h[:])
	return enc[:4] + "-" + enc[4:8]
}
