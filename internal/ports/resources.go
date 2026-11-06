package ports

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/session"
)

type ResourceRepository interface {
	Resource(context.Context, string, string) (session.Resource, error)
	PutResource(context.Context, session.Resource, uint64, operation.Mutation) (session.Resource, error)
	ReplayResource(context.Context, operation.Mutation) (*session.Resource, error)
	Resources(context.Context, string, int) ([]session.Resource, error)
}
