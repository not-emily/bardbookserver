package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

const tokenBytes = 32

// NewToken returns a device token and the hash to store. The plaintext is
// shown to the client once and never persisted.
//
// A 256-bit random value needs no slow hash: there is nothing to brute force,
// so SHA-256 is sufficient and keeps per-request lookup cheap. Passwords and
// PINs are low entropy and use Argon2id instead.
func NewToken() (token, hash string, err error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	return token, HashToken(token), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
