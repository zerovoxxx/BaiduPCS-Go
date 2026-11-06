package ports

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/login"
)

type LoginGateway interface {
	OpenLogin(context.Context, configuration.Snapshot, login.Input) (LoginExchange, error)
}
type LoginExchange interface {
	Begin() (login.Step, error)
	Poll() (login.Step, error)
	Respond(string) (login.Step, error)
	Close() error
}
