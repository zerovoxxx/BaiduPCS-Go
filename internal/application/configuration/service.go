package configuration

import (
	"context"
	"encoding/json"

	appauth "github.com/qjfoidnh/BaiduPCS-Go/internal/application/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

type Service struct {
	repo         ports.ConfigurationRepository
	defaults     configuration.Client
	destinations ports.DestinationValidator
	network      ports.NetworkEnvironment
}

func New(repo ports.ConfigurationRepository, defaults configuration.Client, destinations ports.DestinationValidator, network ports.NetworkEnvironment) *Service {
	return &Service{repo: repo, defaults: defaults.Clone(), destinations: destinations, network: network}
}
func admin(p auth.Principal) error { return appauth.Authorize(p, []string{"admin"}, "") }
func (s *Service) Read(ctx context.Context, p auth.Principal) (configuration.Snapshot, error) {
	if err := admin(p); err != nil {
		return configuration.Snapshot{}, err
	}
	return s.repo.Read(ctx)
}
func (s *Service) Defaults(p auth.Principal) (configuration.Client, error) {
	if err := admin(p); err != nil {
		return configuration.Client{}, err
	}
	return s.defaults.Clone(), nil
}
func (s *Service) issues(ctx context.Context, p auth.Principal, c configuration.Client) []fault.Issue {
	issues := configuration.Validate(c)
	if c.SaveDir != nil {
		if !p.AllowsRoot(c.SaveDir.RootID) {
			issues = append(issues, fault.Issue{Pointer: "/savedir", Code: "LOCAL_PATH_FORBIDDEN", Message: "无权使用此本地根目录"})
		} else if s.destinations == nil {
			issues = append(issues, fault.Issue{Pointer: "/savedir", Code: "LOCAL_PATH_FORBIDDEN", Message: "未配置本地根目录"})
		} else if err := s.destinations.ValidateDestination(ctx, *c.SaveDir); err != nil {
			issues = append(issues, fault.Issue{Pointer: "/savedir", Code: "LOCAL_PATH_FORBIDDEN", Message: "本地目标不在允许的可写根目录内"})
		}
	}
	if len(c.LocalAddrs) > 0 {
		local := map[string]bool{}
		var addrs []string
		var err error
		if s.network != nil {
			addrs, err = s.network.LocalAddresses(ctx)
		}
		if err != nil || s.network == nil {
			issues = append(issues, fault.Issue{Pointer: "/local_addrs", Code: "INVALID_CONFIGURATION", Message: "无法枚举本地网卡地址"})
		} else {
			for _, addr := range addrs {
				local[addr] = true
			}
			for _, ip := range c.LocalAddrs {
				if !local[ip] {
					issues = append(issues, fault.Issue{Pointer: "/local_addrs", Code: "INVALID_CONFIGURATION", Message: "地址不属于本机网卡"})
					break
				}
			}
		}
	}
	return issues
}
func (s *Service) Validate(ctx context.Context, p auth.Principal, patch configuration.Patch) (configuration.Client, []fault.Issue, error) {
	if err := admin(p); err != nil {
		return configuration.Client{}, nil, err
	}
	snapshot, err := s.repo.Read(ctx)
	if err != nil {
		return configuration.Client{}, nil, err
	}
	candidate, _ := patch.Apply(snapshot.Client())
	candidate = configuration.Normalize(candidate)
	return candidate, s.issues(ctx, p, candidate), nil
}
func (s *Service) Patch(ctx context.Context, p auth.Principal, expected uint64, key string, patch configuration.Patch) (configuration.Change, error) {
	canonical, err := json.Marshal(patch)
	if err != nil {
		return configuration.Change{}, err
	}
	mutation := configuration.NewMutation(p.ID, "CFG02", key, expected, canonical)
	return s.mutate(ctx, p, expected, mutation, func(current configuration.Client) (configuration.Client, []string, error) {
		c, fields := patch.Apply(current)
		return c, fields, nil
	})
}
func (s *Service) Reset(ctx context.Context, p auth.Principal, expected uint64, key string, fields []string) (configuration.Change, error) {
	canonical, _ := json.Marshal(struct {
		Fields []string `json:"fields"`
	}{fields})
	mutation := configuration.NewMutation(p.ID, "CFG05", key, expected, canonical)
	return s.mutate(ctx, p, expected, mutation, func(current configuration.Client) (configuration.Client, []string, error) {
		return configuration.Reset(current, s.defaults, fields)
	})
}
func (s *Service) mutate(ctx context.Context, p auth.Principal, expected uint64, m configuration.Mutation, apply func(configuration.Client) (configuration.Client, []string, error)) (configuration.Change, error) {
	if err := admin(p); err != nil {
		return configuration.Change{}, err
	}
	if replay, err := s.repo.Replay(ctx, m); err != nil {
		return configuration.Change{}, err
	} else if replay != nil {
		return *replay, nil
	}
	current, err := s.repo.Read(ctx)
	if err != nil {
		return configuration.Change{}, err
	}
	if current.Revision() != expected {
		// Another identical request may have committed after the first replay
		// lookup. Give its durable response precedence over the stale ETag.
		if replay, replayErr := s.repo.Replay(ctx, m); replayErr != nil {
			return configuration.Change{}, replayErr
		} else if replay != nil {
			return *replay, nil
		}
		return configuration.Change{}, fault.New(fault.Precondition, "REVISION_MISMATCH", "配置版本已改变")
	}
	candidate, fields, err := apply(current.Client())
	if err != nil {
		return configuration.Change{}, err
	}
	candidate = configuration.Normalize(candidate)
	if issues := s.issues(ctx, p, candidate); len(issues) > 0 {
		return configuration.Change{}, &fault.Error{Kind: fault.Unsupported, Code: "INVALID_CONFIGURATION", Message: "配置校验失败", Issues: issues}
	}
	m.Fields = fields
	return s.repo.Commit(ctx, expected, candidate, m)
}
