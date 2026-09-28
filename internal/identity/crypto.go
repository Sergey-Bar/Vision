package identity

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"golang.org/x/crypto/argon2"
)

const argon2idVersion = 19

// argon2id is wrapped rather than called directly so the parameters used in
// hashing and in verification are the same call, and so a future change to the
// algorithm is one edit in one place.
func argon2id(password, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
	return argon2.IDKey(password, salt, time, memory, threads, keyLen)
}

func b64(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }

func unb64(s string) ([]byte, error) { return base64.RawStdEncoding.DecodeString(s) }

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
