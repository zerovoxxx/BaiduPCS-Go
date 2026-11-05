package login

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/accounts"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/application/contexts"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	model "github.com/qjfoidnh/BaiduPCS-Go/internal/domain/login"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"sort"
	"sync"
	"time"
)

type Challenge struct {
	ID, Type, Prompt string
	Image            bool
}
type View struct {
	ID, Method, State, AccountID string
	Expires                      int64
	Challenge                    *Challenge
	Image                        bool
	Problem                      *fault.Error
}
type entry struct {
	mu               sync.Mutex
	owner            auth.Principal
	view, initial    View
	exchange         ports.LoginExchange
	step             model.Step
	cancel           context.CancelFunc
	responses        map[string]response
	result           *account.Entry
	createHash       string
	completeInput    string
	completeRevision uint64
	failure          error
}
type response struct {
	hash string
	view View
}
type Service struct {
	ctx      context.Context
	gateway  ports.LoginGateway
	cfg      ports.ConfigurationRepository
	accounts *accounts.Service
	mu       sync.Mutex
	entries  map[string]*entry
	keys     map[string]string
	salt     []byte
}

func New(ctx context.Context, g ports.LoginGateway, c ports.ConfigurationRepository, a *accounts.Service) *Service {
	s := &Service{ctx: ctx, gateway: g, cfg: c, accounts: a, entries: map[string]*entry{}, keys: map[string]string{}, salt: make([]byte, 32)}
	_, _ = rand.Read(s.salt)
	return s
}
func (s *Service) hash(v any) string {
	b, _ := json.Marshal(v)
	h := hmac.New(sha256.New, s.salt)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Service) Create(ctx context.Context, p auth.Principal, in model.Input, key string) (View, error) {
	if !p.Has("admin") {
		return View{}, fault.New(fault.Forbidden, "FORBIDDEN", "登录需要管理员权限")
	}
	binding := s.hash([]string{p.ID, "LOG01", key})
	inputHash := s.hash(in)
	s.mu.Lock()
	if key != "" {
		if id := s.keys[binding]; id != "" {
			old := s.entries[id]
			s.mu.Unlock()
			old.mu.Lock()
			defer old.mu.Unlock()
			if old.createHash != inputHash {
				return View{}, fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", "登录幂等Key已用于不同输入")
			}
			return old.initial, old.failure
		}
	}
	if len(s.entries) >= 128 {
		s.mu.Unlock()
		return View{}, fault.New(fault.RateLimited, "LOGIN_SESSION_LIMIT", "登录会话数量已满")
	}
	id, e := contexts.ID("login_")
	if e != nil {
		s.mu.Unlock()
		return View{}, e
	}
	lifetime, cancel := context.WithTimeout(s.ctx, 300*time.Second)
	v := View{ID: id, Method: in.Method, State: "waiting", Expires: time.Now().Add(300 * time.Second).Unix()}
	r := &entry{owner: p.Clone(), view: v, cancel: cancel, responses: map[string]response{}, createHash: inputHash}
	r.mu.Lock()
	s.entries[id] = r
	if key != "" {
		s.keys[binding] = id
	}
	s.mu.Unlock()
	defer r.mu.Unlock()
	cfg, e := s.cfg.Read(ctx)
	if e == nil {
		r.exchange, e = s.gateway.OpenLogin(lifetime, cfg, in)
	}
	if e == nil {
		r.step, e = r.exchange.Begin()
	}
	if e == nil {
		e = s.apply(r, r.step)
	}
	if e != nil {
		r.failure = e
		r.view.State = "failed"
		r.view.Problem = fault.As(e)
		r.close()
	}
	r.initial = r.view
	return r.view, e
}
func (r *entry) close() {
	r.cancel()
	if r.exchange != nil {
		_ = r.exchange.Close()
		r.exchange = nil
	}
	r.step = model.Step{}
	r.view.Image = false
	r.view.Challenge = nil
}
func (s *Service) apply(r *entry, step model.Step) error {
	if step.State != "waiting" && step.State != "confirmed" && step.State != "challenge_required" {
		return fault.New(fault.BadGateway, "LOGIN_PROTOCOL_ERROR", "登录状态无效")
	}
	if step.State == "challenge_required" && step.ChallengeType != "captcha" && step.ChallengeType != "sms" && step.ChallengeType != "email" {
		return fault.New(fault.Unsupported, "LOGIN_CHALLENGE_UNSUPPORTED", "此挑战尚未实现")
	}
	r.step = step
	r.view.State = step.State
	r.view.Challenge = nil
	r.view.Image = len(step.Image) > 0
	if step.ChallengeType != "" {
		id, e := contexts.ID("challenge_")
		if e != nil {
			return e
		}
		r.view.Challenge = &Challenge{ID: id, Type: step.ChallengeType, Prompt: step.Prompt, Image: len(step.Image) > 0}
	}
	return nil
}
func (s *Service) locked(p auth.Principal, id string) (*entry, error) {
	s.mu.Lock()
	r := s.entries[id]
	s.mu.Unlock()
	if r == nil {
		return nil, fault.New(fault.NotFound, "RESOURCE_NOT_FOUND", "登录会话不存在")
	}
	r.mu.Lock()
	if !p.Has("admin") || r.owner.ID != p.ID {
		r.mu.Unlock()
		return nil, fault.New(fault.Forbidden, "FORBIDDEN", "登录会话不属于调用方")
	}
	if r.view.Expires <= time.Now().Unix() || r.view.State == "expired" {
		r.view.State = "expired"
		r.close()
		r.mu.Unlock()
		return nil, fault.New(fault.Gone, "RESOURCE_EXPIRED", "登录会话已过期或撤销")
	}
	return r, nil
}
func (s *Service) Get(ctx context.Context, p auth.Principal, id string) (View, error) {
	r, e := s.locked(p, id)
	if e != nil {
		return View{}, e
	}
	defer r.mu.Unlock()
	if r.view.State == "waiting" && r.view.Method == "qrcode" && r.exchange != nil {
		step, e := r.exchange.Poll()
		if e != nil {
			return r.view, e
		}
		if e = s.apply(r, step); e != nil {
			return r.view, e
		}
	}
	return r.view, nil
}
func (s *Service) Image(ctx context.Context, p auth.Principal, id string) ([]byte, error) {
	r, e := s.locked(p, id)
	if e != nil {
		return nil, e
	}
	defer r.mu.Unlock()
	if len(r.step.Image) == 0 {
		return nil, fault.New(fault.Conflict, "LOGIN_IMAGE_NOT_AVAILABLE", "此会话当前没有图片")
	}
	return append([]byte{}, r.step.Image...), nil
}
func (s *Service) Respond(ctx context.Context, p auth.Principal, id, challenge, answer, key string) (View, error) {
	r, e := s.locked(p, id)
	if e != nil {
		return View{}, e
	}
	defer r.mu.Unlock()
	hash := s.hash([]string{challenge, answer})
	if key != "" {
		if old, ok := r.responses[key]; ok {
			if old.hash != hash {
				return View{}, fault.New(fault.Conflict, "IDEMPOTENCY_KEY_REUSED", "挑战幂等Key已用于不同响应")
			}
			return old.view, nil
		}
	}
	if r.view.Challenge == nil || r.view.Challenge.ID != challenge || r.view.State != "challenge_required" || r.exchange == nil {
		return View{}, fault.New(fault.Conflict, "LOGIN_CHALLENGE_MISMATCH", "挑战已变化或不属于此会话")
	}
	if len(r.responses) >= 16 {
		return View{}, fault.New(fault.RateLimited, "LOGIN_RESPONSE_LIMIT", "挑战响应次数达到上限")
	}
	step, e := r.exchange.Respond(answer)
	if e != nil {
		return r.view, e
	}
	if e = s.apply(r, step); e != nil {
		return r.view, e
	}
	if key != "" {
		r.responses[key] = response{hash, r.view}
	}
	return r.view, nil
}
func (s *Service) Complete(ctx context.Context, p auth.Principal, id, target string, label *string) (account.Entry, error) {
	r, e := s.locked(p, id)
	if e != nil {
		return account.Entry{}, e
	}
	defer r.mu.Unlock()
	input := s.hash([]any{target, label})
	if r.completeInput != "" && input != r.completeInput {
		return account.Entry{}, fault.New(fault.Conflict, "LOGIN_COMPLETE_CONFLICT", "会话已经绑定另一个完成目标")
	}
	if r.result != nil {
		if input != r.completeInput {
			return account.Entry{}, fault.New(fault.Conflict, "LOGIN_COMPLETE_CONFLICT", "会话已经完成到另一目标")
		}
		if !p.AllowsAccount(r.result.ID) {
			return account.Entry{}, fault.New(fault.Forbidden, "FORBIDDEN", "账号未授权")
		}
		return *r.result, nil
	}
	if r.view.State != "confirmed" {
		return account.Entry{}, fault.New(fault.Conflict, "LOGIN_NOT_CONFIRMED", "登录尚未经过上游确认")
	}
	if target == "" {
		if !r.owner.AllowsAccount("*") || !p.AllowsAccount("*") {
			return account.Entry{}, fault.New(fault.Forbidden, "FORBIDDEN", "无权创建账号")
		}
	} else if !r.owner.AllowsAccount(target) || !p.AllowsAccount(target) {
		return account.Entry{}, fault.New(fault.Forbidden, "FORBIDDEN", "目标账号未授权")
	}
	c := r.step.Credentials
	identity, e := s.accounts.VerifyLogin(ctx, c)
	if e != nil {
		return account.Entry{}, e
	}
	var a account.Entry
	if target == "" {
		r.completeInput = input
		a, e = s.accounts.ImportCanonical(ctx, p, labelValue(label), "", "http-login/"+id, c)
	} else {
		existing, e2 := s.accounts.Get(ctx, p, target)
		if e2 != nil {
			return a, e2
		}
		if existing.Identity.Verified && existing.Identity.UK != nil && *existing.Identity.UK != *identity.UK {
			return a, fault.New(fault.Conflict, "LOGIN_ACCOUNT_IDENTITY_MISMATCH", "登录身份与目标账号不同")
		}
		if r.completeRevision == 0 {
			r.completeRevision = existing.Revision
		}
		cookie := ""
		keys := []string{}
		for k := range c.Cookies {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cookie += k + "=" + c.Cookies[k] + ";"
		}
		patch := account.Patch{"cookies": &cookie}
		r.completeInput = input
		a, e = s.accounts.Patch(ctx, p, target, r.completeRevision, label, patch, "http-login/"+id)
		if e != nil && fault.As(e).Kind == fault.Precondition {
			r.completeRevision = 0
		}
	}
	if e != nil {
		return a, e
	}
	validated, e := s.accounts.Validate(ctx, p, a.ID)
	if e != nil {
		return a, e
	}
	if !validated.Valid {
		return a, validated.Problem
	}
	a = validated.Account
	stored := a
	stored.Credentials = account.Credentials{}
	r.result = &stored
	r.completeInput = input
	r.view.State = "completed"
	r.view.AccountID = a.ID
	r.close()
	return a, nil
}
func (s *Service) Delete(ctx context.Context, p auth.Principal, id string) error {
	r, e := s.locked(p, id)
	if e != nil {
		if fault.As(e).Kind == fault.Gone {
			return nil
		}
		return e
	}
	defer r.mu.Unlock()
	r.view.State = "expired"
	r.close()
	return nil
}
func (s *Service) Cleanup(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	for id, r := range s.entries {
		r.mu.Lock()
		if r.view.Expires <= now {
			r.view.State = "expired"
			r.close()
		}
		if r.view.Expires+86400 <= now {
			delete(s.entries, id)
			for k, v := range s.keys {
				if v == id {
					delete(s.keys, k)
				}
			}
		}
		r.mu.Unlock()
	}
	return nil
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.entries {
		r.mu.Lock()
		r.view.State = "expired"
		r.close()
		r.mu.Unlock()
	}
}

func labelValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
