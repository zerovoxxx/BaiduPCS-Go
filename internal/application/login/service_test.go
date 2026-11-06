package login

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	model "github.com/qjfoidnh/BaiduPCS-Go/internal/domain/login"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"sync/atomic"
	"testing"
	"time"
)

type testConfig struct{}

func (testConfig) Read(context.Context) (configuration.Snapshot, error) {
	return configuration.NewSnapshot(1, configuration.Client{}), nil
}
func (testConfig) Replay(context.Context, configuration.Mutation) (*configuration.Change, error) {
	return nil, nil
}
func (testConfig) Commit(context.Context, uint64, configuration.Client, configuration.Mutation) (configuration.Change, error) {
	return configuration.Change{}, nil
}

type exchange struct {
	closed atomic.Bool
	step   model.Step
}

func (e *exchange) Begin() (model.Step, error)         { return e.step, nil }
func (e *exchange) Poll() (model.Step, error)          { return e.step, nil }
func (e *exchange) Respond(string) (model.Step, error) { return e.step, nil }
func (e *exchange) Close() error                       { e.closed.Store(true); return nil }

type gateway struct{ current *exchange }

func (g gateway) OpenLogin(context.Context, configuration.Snapshot, model.Input) (ports.LoginExchange, error) {
	return g.current, nil
}
func TestP6LoginTTLAndUnknownChallengeFailClosed(t *testing.T) {
	ctx := context.Background()
	p := auth.Principal{ID: "owner", Scopes: []string{"admin"}, Accounts: []string{"*"}}
	for _, kind := range []string{"captcha", "unsupported"} {
		x := &exchange{step: model.Step{State: "challenge_required", ChallengeType: kind}}
		s := New(ctx, gateway{x}, testConfig{}, nil)
		v, e := s.Create(ctx, p, model.Input{Method: "password", Username: "user", Password: "never-public"}, "create")
		if kind == "unsupported" {
			if fault.As(e).Code != "LOGIN_CHALLENGE_UNSUPPORTED" || !x.closed.Load() {
				t.Fatal("unknown challenge accepted", e)
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		s.entries[v.ID].view.Expires = time.Now().Add(-time.Second).Unix()
		if _, e = s.Get(ctx, p, v.ID); fault.As(e).Kind != fault.Gone {
			t.Fatal("expired session remained usable", e)
		}
		if !x.closed.Load() {
			t.Fatal("expiry did not destroy exchange")
		}
		if _, e = s.Complete(ctx, p, v.ID, "", nil); fault.As(e).Kind != fault.Gone {
			t.Fatal("expired session completed", e)
		}
	}
}
