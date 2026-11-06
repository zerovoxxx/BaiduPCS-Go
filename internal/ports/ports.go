package ports

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"

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

type ConfigurationSource interface {
	LoadConfiguration(context.Context, string) (configuration.Patch, error)
}
type CLIDefaults struct {
	Revision uint64
	Default  *string
	Accounts []CLIAccount
}
type CLIAccount struct{ ID, Cwd string }
type CLIDefaultsStore interface {
	ReadDefaults(context.Context) (CLIDefaults, error)
	WriteDefaults(context.Context, uint64, *string, bool, []CLIAccount, bool) (CLIDefaults, error)
}

type CLIDefaultsReplay interface {
	ReplayCLIDefaults(context.Context, operation.Mutation) (*CLIDefaults, error)
	SaveCLIDefaults(context.Context, CLIDefaults, operation.Mutation) error
}

type NetworkProbe interface {
	ProbeOutgoing(context.Context, configuration.Snapshot) (string, error)
}
