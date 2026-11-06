package persistence

import (
	"context"
	"encoding/base64"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/content"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"time"
)

func (s *Store) IssueDownloadTicket(ctx context.Context, c content.Ticket, key string) (string, error) {
	m := operation.New(c.Owner, "DWN05/"+c.AccountID, key, []any{c.Path, c.TTLSeconds, c.CredentialRevision})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var replay string
	if found, e := s.replayRequest(ctx, tx, m, &replay); found || e != nil {
		return replay, e
	}
	data, err := s.sealValue("download-ticket", c)
	if err != nil {
		return "", err
	}
	ticket := base64.RawURLEncoding.EncodeToString(data)
	if err = s.saveReplay(ctx, tx, m, "ticket", ticket); err != nil {
		return "", err
	}
	return ticket, tx.Commit()
}
func (s *Store) ReadDownloadTicket(ctx context.Context, value string) (content.Ticket, error) {
	var c content.Ticket
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(value) > 4096 || s.openValue("download-ticket", raw, &c) != nil {
		return c, fault.New(fault.Unauthorized, "INVALID_DOWNLOAD_TICKET", "下载票据无效")
	}
	if c.Expires <= time.Now().Unix() {
		return c, fault.New(fault.Gone, "DOWNLOAD_TICKET_EXPIRED", "下载票据已过期")
	}
	return c, ctx.Err()
}
