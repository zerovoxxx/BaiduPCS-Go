package ports

import (
	"context"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/auth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
)

type ConfigurationRepository interface {
	Read(context.Context) (configuration.Snapshot, error)
	Replay(context.Context, configuration.Mutation) (*configuration.Change, error)
	Commit(context.Context, uint64, configuration.Client, configuration.Mutation) (configuration.Change, error)
}

type Authenticator interface {
	Authenticate(context.Context, string) (auth.Principal, error)
}

type DestinationValidator interface {
	ValidateDestination(context.Context, configuration.Directory) error
}

type NetworkEnvironment interface {
	LocalAddresses(context.Context) ([]string, error)
}
