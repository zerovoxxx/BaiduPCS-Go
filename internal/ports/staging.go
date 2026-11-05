package ports

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/staging"
	"io"
)

type StagingRepository interface {
	ReserveStage(context.Context, staging.File) error
	FinishStage(context.Context, staging.File, operation.Mutation) (staging.File, error)
	Stage(context.Context, string) (staging.File, error)
	DeleteStage(context.Context, string, operation.Mutation) error
	ReplayStage(context.Context, operation.Mutation) (*staging.File, error)
	StageCandidates(context.Context) ([]staging.File, error)
}

type StagingFiles interface {
	Metadata(context.Context, string) (staging.File, error)
	Receive(context.Context, string, string, string, io.Reader, ...func() (string, error)) (staging.File, error)
	Delete(context.Context, string, operation.Mutation) error
	Cleanup(context.Context) error
}
