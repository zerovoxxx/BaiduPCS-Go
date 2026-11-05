package content

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/content"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"io"
	"time"
)

type Service struct {
	accounts ports.AccountRepository
	cfg      ports.ConfigurationRepository
	gateway  ports.ContentGateway
	tickets  ports.DownloadTickets
}

func New(a ports.AccountRepository, c ports.ConfigurationRepository, g ports.ContentGateway, tickets ...ports.DownloadTickets) *Service {
	s := &Service{accounts: a, cfg: c, gateway: g}
	if len(tickets) > 0 {
		s.tickets = tickets[0]
	}
	return s
}
func (s *Service) session(ctx context.Context, p auth.Principal, id string) (ports.ContentSession, error) {
	if !p.Has("read") || !p.AllowsAccount(id) {
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "无权读取此账号")
	}
	a, err := s.accounts.Account(ctx, id)
	if err != nil {
		return nil, err
	}
	cfg, err := s.cfg.Read(ctx)
	if err != nil {
		return nil, err
	}
	return s.gateway.OpenContent(ctx, cfg, a.Credentials)
}
func (s *Service) Metadata(ctx context.Context, p auth.Principal, id, remote string) (content.Metadata, error) {
	g, err := s.session(ctx, p, id)
	if err != nil {
		return content.Metadata{}, err
	}
	defer g.Close()
	return g.Metadata(remote)
}
func (s *Service) Links(ctx context.Context, p auth.Principal, id, remote, source string, index int) ([]content.Link, error) {
	g, err := s.session(ctx, p, id)
	if err != nil {
		return nil, err
	}
	defer g.Close()
	return g.Links(remote, source, index)
}

type lease struct {
	io.ReadCloser
	session ports.ContentSession
}

func (l *lease) Close() error { err := l.ReadCloser.Close(); _ = l.session.Close(); return err }
func (s *Service) Open(ctx context.Context, p auth.Principal, id, remote string, first, last, total int64) (io.ReadCloser, error) {
	g, err := s.session(ctx, p, id)
	if err != nil {
		return nil, err
	}
	body, err := g.Open(ctx, remote, first, last, total)
	if err != nil {
		g.Close()
		return nil, err
	}
	return &lease{body, g}, nil
}
func (s *Service) ResolveIDs(ctx context.Context, p auth.Principal, id string, ids []int64) ([]string, error) {
	g, err := s.session(ctx, p, id)
	if err != nil {
		return nil, err
	}
	defer g.Close()
	return g.ResolveIDs(ids)
}

func (s *Service) Issue(ctx context.Context, p auth.Principal, id, remote string, seconds int, key string) (string, content.Ticket, error) {
	var claim content.Ticket
	if seconds < 1 || seconds > 300 {
		return "", claim, fault.New(fault.Invalid, "INVALID_REQUEST", "票据有效期必须为1至300秒")
	}
	if _, err := s.Metadata(ctx, p, id, remote); err != nil {
		return "", claim, err
	}
	a, err := s.accounts.Account(ctx, id)
	if err != nil {
		return "", claim, err
	}
	claim = content.Ticket{TTLSeconds: seconds, Owner: p.ID, AccountID: id, Path: remote, CredentialRevision: a.Credentials.Revision, Expires: time.Now().Unix() + int64(seconds)}
	if s.tickets == nil {
		return "", claim, fault.New(fault.Unavailable, "TICKET_NOT_AVAILABLE", "票据存储不可用")
	}
	value, err := s.tickets.IssueDownloadTicket(ctx, claim, key)
	if err != nil {
		return "", claim, err
	}
	claim, err = s.tickets.ReadDownloadTicket(ctx, value)
	return value, claim, err
}
func (s *Service) Redeem(ctx context.Context, value, id, remote string) (string, error) {
	if s.tickets == nil {
		return "", fault.New(fault.Unauthorized, "INVALID_DOWNLOAD_TICKET", "票据不可用")
	}
	c, err := s.tickets.ReadDownloadTicket(ctx, value)
	if err != nil {
		return "", err
	}
	if c.AccountID != id || c.Path != remote {
		return "", fault.New(fault.Forbidden, "DOWNLOAD_TICKET_SCOPE_MISMATCH", "票据不属于此账号和路径")
	}
	a, err := s.accounts.Account(ctx, id)
	if err != nil {
		return "", err
	}
	if a.Credentials.Revision != c.CredentialRevision {
		return "", fault.New(fault.Gone, "DOWNLOAD_TICKET_REVOKED", "票据账号版本已变化")
	}
	return c.Owner, nil
}
