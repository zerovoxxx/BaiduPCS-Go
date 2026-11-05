package persistence

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

func (s *Store) ReplayCLIDefaults(ctx context.Context, m operation.Mutation) (*ports.CLIDefaults, error) {
	var v ports.CLIDefaults
	ok, e := s.replayRequest(ctx, s.db, m, &v)
	if !ok || e != nil {
		return nil, e
	}
	return &v, nil
}
func (s *Store) SaveCLIDefaults(ctx context.Context, v ports.CLIDefaults, m operation.Mutation) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = s.saveReplay(ctx, tx, m, "cli-defaults", v); e != nil {
		return e
	}
	return tx.Commit()
}
