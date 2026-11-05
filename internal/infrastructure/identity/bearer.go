package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
)

type Record struct {
	ID          string   `json:"id"`
	TokenSHA256 string   `json:"token_sha256"`
	Scopes      []string `json:"scopes"`
	Accounts    []string `json:"accounts"`
	Roots       []string `json:"roots"`
}

type token struct {
	digest    [32]byte
	principal auth.Principal
}
type Bearer struct{ tokens []token }

func New(records []Record) (*Bearer, error) {
	if len(records) == 0 || len(records) > 1000 {
		return nil, errors.New("service requires 1 to 1000 principals")
	}
	b := &Bearer{}
	ids := map[string]bool{}
	hashes := map[string]bool{}
	validScopes := map[string]bool{"read": true, "write": true, "transfer": true, "local.read": true, "local.write": true, "admin": true}
	for _, record := range records {
		if len(record.Scopes) > 7 || len(record.Accounts) > 1000 || len(record.Roots) > 1000 {
			return nil, errors.New("principal allowlist exceeds service limits")
		}
		digest, err := hex.DecodeString(record.TokenSHA256)
		if err != nil || len(digest) != 32 || !configuration.ValidID(record.ID) || ids[record.ID] || hashes[strings.ToLower(record.TokenSHA256)] {
			return nil, errors.New("invalid or duplicate principal ID/token digest")
		}
		for _, scope := range record.Scopes {
			if !validScopes[scope] {
				return nil, errors.New("invalid principal scope")
			}
		}
		for _, list := range [][]string{record.Accounts, record.Roots} {
			for _, id := range list {
				if id != "*" && !configuration.ValidID(id) {
					return nil, errors.New("invalid principal resource allowlist")
				}
			}
		}
		var d [32]byte
		copy(d[:], digest)
		b.tokens = append(b.tokens, token{digest: d, principal: auth.Principal{ID: record.ID, Scopes: append([]string{}, record.Scopes...), Accounts: append([]string{}, record.Accounts...), Roots: append([]string{}, record.Roots...)}})
		ids[record.ID] = true
		hashes[strings.ToLower(record.TokenSHA256)] = true
	}
	return b, nil
}
func (b *Bearer) Authenticate(ctx context.Context, value string) (auth.Principal, error) {
	if err := ctx.Err(); err != nil {
		return auth.Principal{}, err
	}
	digest := sha256.Sum256([]byte(value))
	matched := -1
	// Do not short-circuit on the first match or retain the supplied plaintext.
	for i, entry := range b.tokens {
		if subtle.ConstantTimeCompare(digest[:], entry.digest[:]) == 1 {
			matched = i
		}
	}
	if matched < 0 || !ValidToken(value) {
		return auth.Principal{}, fault.New(fault.Unauthorized, "API_UNAUTHORIZED", "服务API身份缺失或无效")
	}
	return b.tokens[matched].principal.Clone(), nil
}

// ValidToken follows the RFC6750 bearer-token alphabet without accepting spaces
// or control characters; hashes alone cannot validate a provisioned token.
func ValidToken(value string) bool {
	if len(value) == 0 || len(value) > 512 {
		return false
	}
	padding := false
	for _, c := range value {
		if c == '=' {
			padding = true
			continue
		}
		if padding || !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/", c)) {
			return false
		}
	}
	return value[0] != '='
}

// Principal rechecks a ticket owner's current static permissions. Ticket claims
// never grant their stored scopes and never restore a removed identity.
func (b *Bearer) Principal(ctx context.Context, id string) (auth.Principal, error) {
	if err := ctx.Err(); err != nil {
		return auth.Principal{}, err
	}
	for _, entry := range b.tokens {
		if entry.principal.ID == id {
			return entry.principal.Clone(), nil
		}
	}
	return auth.Principal{}, fault.New(fault.Unauthorized, "API_UNAUTHORIZED", "票据原调用方已不可用")
}
