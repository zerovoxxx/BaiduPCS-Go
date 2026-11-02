package transfers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

// Service is the bounded P2 execution slice, not a generic workflow scheduler.
// Its context belongs to the service lifecycle, never the submission request.
type Service struct {
	repo          ports.TransferRepository
	accounts      ports.AccountRepository
	configuration ports.ConfigurationRepository
	engine        ports.TransferEngine
	ctx           context.Context
	stop          context.CancelFunc
	mu            sync.Mutex
	closing       bool
	storageError  error
	running       map[string]context.CancelFunc
	slots         chan struct{}
	workers       chan struct{}
	wg            sync.WaitGroup
}

func New(ctx context.Context, repo ports.TransferRepository, accounts ports.AccountRepository, cfg ports.ConfigurationRepository, engine ports.TransferEngine) (*Service, error) {
	if err := repo.InterruptTransfers(ctx); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &Service{repo: repo, accounts: accounts, configuration: cfg, engine: engine, ctx: lifetime, stop: cancel, running: map[string]context.CancelFunc{}, slots: make(chan struct{}, 18), workers: make(chan struct{}, 2)}, nil
}
func permission(p auth.Principal, r transfer.Record) error {
	local := "local.read"
	if r.Request.Kind == "download" {
		local = "local.write"
	}
	if !p.Has("transfer") || !p.Has(local) || !p.AllowsAccount(r.Request.AccountID) || !p.AllowsRoot(r.Request.RootID) || (r.Owner != p.ID && !p.Has("admin")) {
		return fault.New(fault.Forbidden, "FORBIDDEN", "无权访问此账号、任务或本地目录")
	}
	return nil
}
func (s *Service) Submit(ctx context.Context, p auth.Principal, req transfer.Request, key string) (transfer.Record, error) {
	r := transfer.Record{Owner: p.ID, Request: req, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := permission(p, r); err != nil {
		return r, err
	}
	if req.Kind != "upload" && req.Kind != "download" || !configuration.ValidID(req.AccountID) || !configuration.ValidID(req.RootID) || !configuration.ValidRelative(req.RelativePath) || req.RelativePath == "" || req.RelativePath == "." || !strings.HasPrefix(req.RemotePath, "/") || path.Clean(req.RemotePath) != req.RemotePath || req.RemotePath == "/" || strings.ContainsAny(req.RemotePath, "\x00\r\n") || len(key) > 128 {
		return r, fault.New(fault.Invalid, "INVALID_REQUEST", "单文件传输输入无效")
	}
	if key != "" {
		b, _ := json.Marshal([]string{p.ID, req.AccountID, req.Kind, key})
		sum := sha256.Sum256(b)
		r.ID = "job_" + hex.EncodeToString(sum[:])
	} else {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return r, err
		}
		r.ID = "job_" + hex.EncodeToString(random[:])
	}
	// Resolve versions once. Replays retain the first record and its snapshot.
	a, err := s.accounts.Account(ctx, req.AccountID)
	if err != nil {
		return r, err
	}
	cfg, err := s.configuration.Read(ctx)
	if err != nil {
		return r, err
	}
	r.Config, r.ConfigRevision, r.CredentialRevision = cfg.Client(), cfg.Revision(), a.Credentials.Revision
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.ctx.Err() != nil {
		return r, fault.New(fault.Unavailable, "SERVICE_DRAINING", "服务正在关闭")
	}
	// Look up an existing logical submission before applying the queue bound.
	old, err := s.repo.Transfer(ctx, r.ID)
	if err == nil {
		if old.Request != req || old.Owner != p.ID {
			return r, fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", "幂等Key已用于不同请求")
		}
		return old, nil
	}
	if fault.As(err).Kind != fault.NotFound {
		return r, err
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return r, fault.New(fault.Unavailable, "QUEUE_FULL", "传输队列已满")
	}
	created, err := s.repo.CreateTransfer(ctx, r)
	if err != nil || !created {
		<-s.slots
		if err != nil {
			return r, err
		}
		return s.repo.Transfer(ctx, r.ID)
	}
	s.schedule(r)
	r.State = "queued"
	return r, nil
}

// schedule is called with mu held and an already reserved slot.
func (s *Service) schedule(r transfer.Record) {
	// The returned submission record and the executor own separate snapshots.
	r = r.Clone()
	ctx, cancel := context.WithCancel(s.ctx)
	s.running[r.ID] = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() { cancel(); <-s.slots; s.mu.Lock(); delete(s.running, r.ID); s.mu.Unlock() }()
		finishCtx := context.WithoutCancel(ctx)
		select {
		case s.workers <- struct{}{}:
			defer func() { <-s.workers }()
		case <-ctx.Done():
			_, err := s.repo.TransitionTransfer(finishCtx, r.ID, []string{"queued"}, "interrupted")
			s.recordStorageError(err)
			return
		}
		ok, err := s.repo.TransitionTransfer(finishCtx, r.ID, []string{"queued"}, "running")
		if err != nil || !ok {
			s.recordStorageError(err)
			return
		}
		a, err := s.accounts.Account(ctx, r.Request.AccountID)
		if err == nil && a.Credentials.Revision != r.CredentialRevision {
			err = fault.New(fault.Conflict, "CREDENTIAL_REVISION_MISMATCH", "凭据版本已变更，拒绝恢复旧任务")
		}
		if err == nil {
			err = s.engine.Execute(ctx, r, a.Credentials, func(cp json.RawMessage) error {
				flush, done := context.WithTimeout(finishCtx, 5*time.Second)
				defer done()
				return s.repo.SaveTransferCheckpoint(flush, r.ID, cp)
			})
		}
		state := "succeeded"
		code := ""
		if err != nil {
			code = fault.As(err).Code
			current, readErr := s.repo.Transfer(finishCtx, r.ID)
			s.recordStorageError(readErr)
			switch {
			case fault.As(err).Code == "REMOTE_RESULT_UNKNOWN" || fault.As(err).Code == "LOCAL_RESULT_UNKNOWN":
				state = "interrupted"
			case readErr == nil && current.State == "cancelling":
				state = "cancelled"
			case s.ctx.Err() != nil:
				state = "interrupted"
			default:
				state = "failed"
			}
		}
		s.recordStorageError(s.repo.FinishTransfer(finishCtx, r.ID, state, code))
	}()
}
func (s *Service) Get(ctx context.Context, p auth.Principal, id string) (transfer.Record, error) {
	r, err := s.repo.Transfer(ctx, id)
	if err != nil {
		return r, err
	}
	if err = permission(p, r); err != nil {
		return transfer.Record{}, err
	}
	return r, nil
}
func (s *Service) Cancel(ctx context.Context, p auth.Principal, id string) (transfer.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.Get(ctx, p, id)
	if err != nil {
		return r, err
	}
	if r.Terminal() {
		return r, nil
	}
	state := "cancelling"
	if r.State == "queued" || r.State == "interrupted" {
		state = "cancelled"
	}
	if _, err = s.repo.TransitionTransfer(ctx, id, []string{r.State}, state); err != nil {
		return r, err
	}
	if cancel := s.running[id]; cancel != nil {
		cancel()
	}
	return s.repo.Transfer(ctx, id)
}
func (s *Service) Resume(ctx context.Context, p auth.Principal, id string) (transfer.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.Get(ctx, p, id)
	if err != nil {
		return r, err
	}
	if s.closing || s.ctx.Err() != nil {
		return r, fault.New(fault.Unavailable, "SERVICE_DRAINING", "服务正在关闭")
	}
	if _, active := s.running[id]; active {
		return r, fault.New(fault.Conflict, "STATE_CONFLICT", "任务仍在排空")
	}
	if r.State != "interrupted" {
		return r, fault.New(fault.Conflict, "STATE_CONFLICT", "仅可恢复中断任务")
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return r, fault.New(fault.Unavailable, "QUEUE_FULL", "传输队列已满")
	}
	ok, err := s.repo.TransitionTransfer(ctx, id, []string{"interrupted"}, "queued")
	if err != nil || !ok {
		<-s.slots
		if err == nil {
			err = errors.New("task state changed")
		}
		return r, err
	}
	s.schedule(r)
	r.State = "queued"
	return r, nil
}
func (s *Service) recordStorageError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.storageError = errors.Join(s.storageError, err)
	s.mu.Unlock()
}
func (s *Service) Close() error {
	s.mu.Lock()
	s.closing = true
	s.stop()
	s.mu.Unlock()
	s.wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.storageError
}
