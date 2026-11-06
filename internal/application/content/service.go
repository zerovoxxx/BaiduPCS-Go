package content

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/content"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"io"
	"sync"
	"time"
)

type Service struct {
	accounts   ports.AccountRepository
	cfg        ports.ConfigurationRepository
	gateway    ports.ContentGateway
	tickets    ports.DownloadTickets
	mu         sync.Mutex
	references map[string]linkClaim
}

func New(a ports.AccountRepository, c ports.ConfigurationRepository, g ports.ContentGateway, tickets ...ports.DownloadTickets) *Service {
	s := &Service{accounts: a, cfg: c, gateway: g, references: map[string]linkClaim{}}
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
	links, err := g.Links(remote, source, index)
	if err != nil {
		return nil, err
	}
	a, err := s.accounts.Account(ctx, id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.references {
		if time.Now().After(v.expires) {
			delete(s.references, k)
		}
	}
	if len(s.references)+len(links) > 256 {
		return nil, fault.New(fault.RateLimited, "LINK_REFERENCE_LIMIT", "摘要链接引用过多")
	}
	for _, v := range links {
		s.references[v.Reference] = linkClaim{owner: p.ID, account: id, address: v.URL, agent: v.UserAgent, revision: a.Credentials.Revision, expires: time.Now().Add(10 * time.Minute)}
	}
	return links, nil
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

type linkClaim struct {
	owner, account, address, agent string
	revision                       uint64
	expires                        time.Time
}

func (s *Service) Reference(ctx context.Context, p auth.Principal, id, ref string) (content.Link, error) {
	s.mu.Lock()
	c, ok := s.references[ref]
	s.mu.Unlock()
	if !ok {
		return content.Link{}, fault.New(fault.NotFound, "RESOURCE_NOT_FOUND", "链接引用不存在")
	}
	if c.owner != p.ID || c.account != id || !p.Has("read") || !p.AllowsAccount(id) {
		return content.Link{}, fault.New(fault.Forbidden, "FORBIDDEN", "链接引用不属于调用方和账号")
	}
	if time.Now().After(c.expires) {
		return content.Link{}, fault.New(fault.Gone, "RESOURCE_EXPIRED", "链接引用已过期")
	}
	a, e := s.accounts.Account(ctx, id)
	if e != nil {
		return content.Link{}, e
	}
	if a.Credentials.Revision != c.revision {
		return content.Link{}, fault.New(fault.Gone, "RESOURCE_EXPIRED", "链接凭据版本已变化")
	}
	return content.Link{URL: c.address, UserAgent: c.agent}, nil
}
