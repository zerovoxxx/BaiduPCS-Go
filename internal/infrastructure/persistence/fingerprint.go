package persistence

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func (s *Store) FingerprintSecret(value string) string {
	m := hmac.New(sha256.New, s.fingerprintKey[:])
	m.Write([]byte(value))
	return hex.EncodeToString(m.Sum(nil))
}
