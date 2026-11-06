package query

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

type Service struct{ repo ports.QuerySnapshots }
type Page = ports.QueryPage

func New(r ports.QuerySnapshots) *Service { return &Service{r} }
func (s *Service) Page(ctx context.Context, binding any, cursor string, size int, fetch func() ([]json.RawMessage, error)) (Page, error) {
	b, err := json.Marshal(binding)
	if err != nil {
		return Page{}, err
	}
	h := sha256.Sum256(b)
	key := hex.EncodeToString(h[:])
	if cursor != "" {
		return s.repo.ReadQuery(ctx, key, cursor, size)
	}
	items, err := fetch()
	if err != nil {
		return Page{}, err
	}
	return s.repo.CreateQuery(ctx, key, items, size)
}
func Encode[T any](items []T) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(items))
	for _, v := range items {
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}
