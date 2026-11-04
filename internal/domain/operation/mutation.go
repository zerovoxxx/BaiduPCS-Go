// Package operation identifies a logical mutation independently of HTTP requests.
package operation

import (
	"crypto/sha256"
	"encoding/json"
)

type Mutation struct {
	Principal, Operation string
	HasKey               bool
	KeyHash, RequestHash [32]byte
}

func New(principal, op, key string, input any) Mutation {
	b, _ := json.Marshal(input)
	return Mutation{Principal: principal, Operation: op, HasKey: key != "", KeyHash: sha256.Sum256([]byte(key)), RequestHash: sha256.Sum256(b)}
}
