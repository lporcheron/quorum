// Package ids generates the base58 identifiers used in URLs: short
// public IDs for resources and longer capability tokens (admin links,
// participant edit links). Tokens are stored hashed; possession of the
// URL is the authorization.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// Base58: no 0/O/I/l, so IDs survive being read out loud or copied by hand.
const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// generate returns n unbiased random alphabet characters, using
// rejection sampling (232 = 4*58 is the largest multiple of 58 ≤ 256).
func generate(n int) string {
	out := make([]byte, 0, n)
	buf := make([]byte, 2*n)
	for len(out) < n {
		rand.Read(buf) // never fails per crypto/rand contract
		for _, b := range buf {
			if b >= 232 {
				continue
			}
			out = append(out, alphabet[int(b)%len(alphabet)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out)
}

// PublicID returns a 12-character identifier for URLs (~70 bits).
// Non-guessable, but not a secret.
func PublicID() string { return generate(12) }

// Token returns a 26-character capability token (~152 bits). Secret:
// only its hash may be stored.
func Token() string { return generate(26) }

// HashToken returns the hex SHA-256 of a token, the only form that
// touches the database. The input is high-entropy random, so a plain
// hash (no salt, no KDF) is enough.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// MatchesHash reports whether token hashes to storedHash, in constant
// time.
func MatchesHash(token, storedHash string) bool {
	h := HashToken(token)
	return subtle.ConstantTimeCompare([]byte(h), []byte(storedHash)) == 1
}
