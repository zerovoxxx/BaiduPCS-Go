package transfers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

// Service is the single owner's bounded durable dispatcher. Its context belongs
// to the service lifecycle, never to a submission's HTTP connection.
type Service struct {
	repo                    ports.TransferRepository
	accounts                ports.AccountRepository
	configuration           ports.ConfigurationRepository
	engine                  ports.TransferEngine
	ctx                     context.Context
	stop                    context.CancelFunc
	mu                      sync.Mutex
	closing                 bool
	storageError            error
	running                 map[string]context.CancelFunc
	targets                 map[string]*targetLock
	slots, workers          chan struct{}
	wake                    chan struct{}
	dispatchDone, closeDone chan struct{}
	closeOnce               sync.Once
	wg                      sync.WaitGroup
}

type targetLock struct {
	token      chan struct{}
	references int
}

func New(ctx context.Context, repo ports.TransferRepository, accounts ports.AccountRepository, cfg ports.ConfigurationRepository, engine ports.TransferEngine) (*Service, error) {
	if err := repo.InterruptTransfers(ctx); err != nil {
		return nil, err
	}
	// Fail startup on corrupt pending records, before any executor is started.
	if _, err := repo.QueuedTransfers(ctx, 18); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	s := &Service{repo: repo, accounts: accounts, configuration: cfg, engine: engine, ctx: lifetime, stop: cancel, running: map[string]context.CancelFunc{}, targets: map[string]*targetLock{}, slots: make(chan struct{}, 18), workers: make(chan struct{}, 2), wake: make(chan struct{}, 1), dispatchDone: make(chan struct{}), closeDone: make(chan struct{})}
	go s.dispatchLoop()
	s.notify()
	return s, nil
}
func permission(p auth.Principal, r transfer.Record) error {
	if r.Request.Kind == "file_scan" {
		if !p.Has("read") || !p.AllowsAccount(r.Request.AccountID) || (r.Owner != p.ID && !p.Has("admin")) {
			return fault.New(fault.Forbidden, "FORBIDDEN", "无权访问扫描任务")
		}
		return nil
	}
	local := "local.read"
	if r.Request.Kind == "download" {
		local = "local.write"
	}
	if !p.Has("transfer") || !p.Has(local) || !p.AllowsAccount(r.Request.AccountID) || !p.AllowsRoot(r.Request.RootID) || (r.Owner != p.ID && !p.Has("admin")) {
		return fault.New(fault.Forbidden, "FORBIDDEN", "无权访问此账号、任务或本地目录")
	}
	return nil
}
func queueFull() error {
	return &fault.Error{Kind: fault.RateLimited, Code: "QUEUE_FULL", Message: "传输队列已满", Retryable: true}
}
func (s *Service) Ready(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.ctx.Err() != nil {
		return fault.New(fault.Unavailable, "SERVICE_DRAINING", "服务正在关闭")
	}
	return s.storageError
}
func (s *Service) Submit(ctx context.Context, p auth.Principal, req transfer.Request, key string) (transfer.Record, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r := transfer.Record{Owner: p.ID, Request: req, CreatedAt: now, UpdatedAt: now, State: "queued", Version: 1}
	if err := permission(p, r); err != nil {
		return r, err
	}
	if req.Kind != "file_scan" && (req.Kind != "upload" && req.Kind != "download" || !configuration.ValidID(req.AccountID) || !configuration.ValidID(req.RootID) || !configuration.ValidRelative(req.RelativePath) || req.RelativePath == "" || req.RelativePath == "." || !strings.HasPrefix(req.RemotePath, "/") || path.Clean(req.RemotePath) != req.RemotePath || req.RemotePath == "/" || strings.ContainsAny(req.RemotePath, "\x00\r\n") || len(req.RemotePath) > 4096 || len(key) > 128) {
		return r, fault.New(fault.Invalid, "INVALID_REQUEST", "单文件传输输入无效")
	}
	if key != "" {
		binding := []string{p.ID, req.AccountID, req.Kind, key}
		if req.RetryOf != "" {
			binding = append(binding, req.RetryOf)
		}
		b, _ := json.Marshal(binding)
		sum := sha256.Sum256(b)
		r.ID = "job_" + hex.EncodeToString(sum[:])
	} else {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return r, err
		}
		r.ID = "job_" + hex.EncodeToString(random[:])
	}
	op := "UPL01/" + req.AccountID
	if req.Kind == "download" {
		op = "DWN01/" + req.AccountID
	}
	if req.Kind == "file_scan" {
		op = "FIL05/" + req.AccountID
	}
	if req.RetryOf != "" {
		op = "JOB09/" + req.RetryOf
	}
	r.Admission = operation.New(p.ID, op, key, req)
	if replay, err := s.repo.ReplayControl(ctx, r.Admission); err != nil {
		return r, err
	} else if replay != nil {
		return *replay, nil
	}
	// Replay before resolving defaults: later configuration changes must not alter
	// an accepted task's snapshot or turn its replay into an unsupported request.
	if old, err := s.repo.Transfer(ctx, r.ID); err == nil {
		if old.Request != req || old.Owner != p.ID {
			return r, fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", "幂等Key已用于不同请求")
		}
		return initialReceipt(old), nil
	} else if fault.As(err).Kind != fault.NotFound {
		return r, err
	}
	a, err := s.accounts.Account(ctx, req.AccountID)
	if err != nil {
		return r, err
	}
	cfg, err := s.configuration.Read(ctx)
	if err != nil {
		return r, err
	}
	r.Config, r.ConfigRevision, r.CredentialRevision = cfg.Client(), cfg.Revision(), a.Credentials.Revision
	if req.Kind == "file_scan" {
		var plan struct{ Options json.RawMessage }
		if err = json.Unmarshal([]byte(req.ScanPlan), &plan); err != nil {
			return r, err
		}
		r.EffectiveOptions = plan.Options
	} else if err = resolveOptions(&r); err != nil {
		return r, err
	}
	if preparer, ok := s.engine.(ports.TransferPreparer); ok {
		r.TargetKey, err = preparer.Prepare(ctx, r, a.Credentials)
		if err != nil {
			return r, err
		}
	} else if req.Public {
		return r, fault.New(fault.Unavailable, "TRANSFER_NOT_READY", "传输准备服务不可用")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.ctx.Err() != nil {
		return r, fault.New(fault.Unavailable, "SERVICE_DRAINING", "服务正在关闭")
	}
	// Submissions and controls serialize admission, not network execution.
	if old, err := s.repo.Transfer(ctx, r.ID); err == nil {
		if old.Request != req || old.Owner != p.ID {
			return r, fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", "幂等Key已用于不同请求")
		}
		return initialReceipt(old), nil
	} else if fault.As(err).Kind != fault.NotFound {
		return r, err
	}
	n, err := s.repo.ActiveTransferCount(ctx)
	if err != nil {
		return r, err
	}
	if n >= 18 {
		return r, queueFull()
	}
	created, err := s.repo.CreateTransfer(ctx, r)
	if err != nil {
		return r, err
	}
	if !created {
		return s.repo.Transfer(ctx, r.ID)
	}
	// The durable queued row is the dispatch authority. A crash or disconnected
	// request here is repaired by the next scan or the next service start.
	s.notify()
	return r, nil
}
func (s *Service) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) dispatchLoop() {
	defer close(s.dispatchDone)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		records, err := s.repo.QueuedTransfers(s.ctx, 18)
		if err != nil {
			if s.ctx.Err() == nil {
				s.recordStorageError(err)
			}
			continue
		}
		s.mu.Lock()
		if !s.closing && s.ctx.Err() == nil {
			for _, r := range records {
				if _, active := s.running[r.ID]; active {
					continue
				}
				select {
				case s.slots <- struct{}{}:
					s.schedule(r)
				default:
				}
			}
		}
		s.mu.Unlock()
	}
}

// acquireTarget is cancellable. Keys name the actual local destination (root
// aliases included) or the account's absolute remote target. Locks are released
// only after the final checkpoint and terminal state have been written.
func (s *Service) acquireTarget(ctx context.Context, key string) (func(), error) {
	s.mu.Lock()
	lock := s.targets[key]
	if lock == nil {
		lock = &targetLock{token: make(chan struct{}, 1)}
		s.targets[key] = lock
	}
	lock.references++
	s.mu.Unlock()
	releaseReference := func() {
		s.mu.Lock()
		lock.references--
		if lock.references == 0 {
			delete(s.targets, key)
		}
		s.mu.Unlock()
	}
	select {
	case lock.token <- struct{}{}:
		return func() { <-lock.token; releaseReference() }, nil
	case <-ctx.Done():
		releaseReference()
		return nil, ctx.Err()
	}
}
func (s *Service) schedule(r transfer.Record) {
	r = r.Clone()
	ctx, cancel := context.WithCancel(s.ctx)
	s.running[r.ID] = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			cancel()
			<-s.slots
			s.mu.Lock()
			delete(s.running, r.ID)
			s.mu.Unlock()
			s.notify()
		}()
		select {
		case s.workers <- struct{}{}:
			defer func() { <-s.workers }()
		case <-ctx.Done():
			flush, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			if _, err := s.accounts.CredentialVersion(flush, r.Request.AccountID, r.CredentialRevision); err != nil && fault.As(err).Code == "CREDENTIAL_REVOKED" {
				state := "failed"
				var cp struct{ Phase string }
				_ = json.Unmarshal(r.Checkpoint, &cp)
				if cp.Phase == "committing" || cp.Phase == "done" {
					state = "interrupted"
				}
				err = s.repo.FinishTransfer(flush, r.ID, state, "CREDENTIAL_REVOKED")
				if fault.As(err).Kind != fault.Conflict {
					s.recordStorageError(err)
				}
				return
			}
			_, err := s.transition(r.ID, []string{"queued"}, "interrupted")
			s.recordStorageError(err)
			return
		}
		ok, err := s.transition(r.ID, []string{"queued"}, "running")
		if err != nil || !ok {
			s.recordStorageError(err)
			return
		}
		target := r.TargetKey
		if target == "" {
			target = r.Request.Kind + ":" + r.Request.AccountID + ":" + r.Request.RootID + ":" + r.Request.RelativePath + ":" + r.Request.RemotePath
		}
		release, err := s.acquireTarget(ctx, target)
		if err == nil {
			defer release()
		}
		var aErr error
		if err == nil {
			c, credentialErr := s.accounts.CredentialVersion(ctx, r.Request.AccountID, r.CredentialRevision)
			aErr = credentialErr
			if aErr == nil {
				aErr = s.engine.Execute(ctx, r, c, func(cp json.RawMessage) error {
					flush, done := context.WithTimeout(context.Background(), 5*time.Second)
					defer done()
					if _, err := s.accounts.CredentialVersion(flush, r.Request.AccountID, r.CredentialRevision); err != nil {
						return err
					}
					return s.repo.SaveTransferCheckpoint(flush, r.ID, cp)
				})
			}
			err = aErr
		}
		flush, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		state, code := "succeeded", ""
		if err != nil {
			code = fault.As(err).Code
			if _, credentialErr := s.accounts.CredentialVersion(flush, r.Request.AccountID, r.CredentialRevision); credentialErr != nil && fault.As(credentialErr).Code == "CREDENTIAL_REVOKED" {
				code = "CREDENTIAL_REVOKED"
			}
			current, readErr := s.repo.Transfer(flush, r.ID)
			s.recordStorageError(readErr)
			var cp struct{ Phase string }
			_ = json.Unmarshal(current.Checkpoint, &cp)
			switch {
			case code == "REMOTE_RESULT_UNKNOWN" || code == "LOCAL_RESULT_UNKNOWN" || cp.Phase == "committing" || cp.Phase == "done":
				state = "interrupted"
			case code == "CREDENTIAL_REVOKED":
				state = "failed"
			case readErr == nil && current.State == "cancelling":
				state = "cancelled"
				code = ""
			case s.ctx.Err() != nil:
				state = "interrupted"
				code = ""
			default:
				state = "failed"
			}
		}
		if finishErr := s.repo.FinishTransfer(flush, r.ID, state, code); finishErr != nil {
			s.recordStorageError(finishErr)
		} else if state == "cancelled" {
			s.cleanup(flush, r.ID)
		}
	}()
}
func (s *Service) transition(id string, from []string, to string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.repo.TransitionTransfer(ctx, id, from, to)
}
func (s *Service) Get(ctx context.Context, p auth.Principal, id string) (transfer.Record, error) {
	r, err := s.repo.Transfer(ctx, id)
	if err != nil {
		return r, err
	}
	if err = permission(p, r); err != nil {
		return transfer.Record{}, err
	}
	if len(r.EffectiveOptions) == 0 && r.Request.Kind != "file_scan" {
		legacy := r
		legacy.Request.Public = false
		if err = resolveOptions(&legacy); err != nil {
			return transfer.Record{}, err
		}
		r.EffectiveOptions = legacy.EffectiveOptions
	}
	return r, nil
}

type Control struct{ Key, Reason string }

func (s *Service) Cancel(ctx context.Context, p auth.Principal, id string, input ...Control) (transfer.Record, error) {
	return s.control(ctx, p, id, false, input...)
}
func (s *Service) Resume(ctx context.Context, p auth.Principal, id string, input ...Control) (transfer.Record, error) {
	return s.control(ctx, p, id, true, input...)
}
func (s *Service) control(ctx context.Context, p auth.Principal, id string, resume bool, input ...Control) (transfer.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.Get(ctx, p, id)
	if err != nil {
		return r, err
	}
	if s.closing || s.ctx.Err() != nil {
		return r, fault.New(fault.Unavailable, "SERVICE_DRAINING", "服务正在关闭")
	}
	control := Control{}
	if len(input) > 0 {
		control = input[0]
	}
	op := "JOB06/" + id
	if resume {
		op = "JOB08/" + id
	}
	m := operation.New(p.ID, op, control.Key, control.Reason)
	if replay, err := s.repo.ReplayControl(ctx, m); err != nil {
		return r, err
	} else if replay != nil {
		if len(replay.EffectiveOptions) == 0 || string(replay.EffectiveOptions) == "null" {
			replay.EffectiveOptions = r.EffectiveOptions
		}
		return *replay, nil
	}
	state := ""
	if resume {
		if r.Request.Kind == "file_scan" {
			return r, fault.New(fault.Unsupported, "UNSUPPORTED_JOB_ACTION", "扫描中断后可取消，再重试创建新任务")
		}
		if _, active := s.running[id]; active {
			return r, fault.New(fault.Conflict, "STATE_CONFLICT", "任务仍在排空")
		}
		if r.State != "interrupted" {
			return r, fault.New(fault.Conflict, "STATE_CONFLICT", "仅可恢复中断任务")
		}
		if _, err := s.accounts.CredentialVersion(ctx, r.Request.AccountID, r.CredentialRevision); err != nil {
			return r, err
		}
		n, err := s.repo.ActiveTransferCount(ctx)
		if err != nil {
			return r, err
		}
		if n >= 18 {
			return r, queueFull()
		}
		state = "queued"
	} else if !r.Terminal() {
		var cp struct{ Phase string }
		if len(r.Checkpoint) > 0 && json.Unmarshal(r.Checkpoint, &cp) != nil {
			return r, fault.New(fault.Internal, "CHECKPOINT_INVALID", "任务检查点损坏")
		}
		if (r.State == "interrupted" || r.State == "queued") && (cp.Phase == "committing" || cp.Phase == "done") {
			return r, fault.New(fault.Conflict, "RECONCILIATION_REQUIRED", "提交结果尚未核对，请恢复任务以核验结果")
		}
		state = "cancelling"
		if r.State == "queued" || r.State == "interrupted" {
			state = "cancelled"
		}
	}
	result, err := s.repo.ControlTransfer(ctx, id, []string{r.State}, state, m)
	if err != nil {
		return r, err
	}
	if len(result.EffectiveOptions) == 0 {
		result.EffectiveOptions = r.EffectiveOptions
	}
	if !resume {
		if cancel := s.running[id]; cancel != nil {
			cancel()
		}
		if result.State == "cancelled" {
			if cleaner, ok := s.engine.(ports.TransferCleaner); ok {
				if err := cleaner.CleanupTransfer(ctx, result); err != nil {
					return result, err
				}
			}
		}
	}
	s.notify()
	return result, nil
}
func (s *Service) cleanup(ctx context.Context, id string) {
	if cleaner, ok := s.engine.(ports.TransferCleaner); ok {
		r, err := s.repo.Transfer(ctx, id)
		if err == nil {
			err = cleaner.CleanupTransfer(ctx, r)
		}
		s.recordStorageError(err)
	}
}
func (s *Service) recordStorageError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	// Preserve the first failure without growing an unbounded error chain if a
	// failed storage device makes every subsequent dispatch scan fail as well.
	if s.storageError == nil {
		s.storageError = err
	}
	s.mu.Unlock()
}
func (s *Service) CloseContext(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.stop()
		s.mu.Unlock()
		go func() { <-s.dispatchDone; s.wg.Wait(); close(s.closeDone) }()
	})
	select {
	case <-s.closeDone:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.storageError
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return s.CloseContext(ctx)
}

// Public P2 admission was always queued. Its immutable fields and option
// snapshot let v3 records replay that receipt even before submission replays
// were persisted separately. This projection never rewrites current job state.
func initialReceipt(r transfer.Record) transfer.Record {
	r = r.Clone()
	r.State = "queued"
	r.Version = 1
	r.UpdatedAt = r.CreatedAt
	r.StartedAt = ""
	r.FinishedAt = ""
	r.Checkpoint = nil
	r.FailureCode = ""
	r.ScanResult = nil
	return r
}
