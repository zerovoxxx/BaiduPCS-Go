package ports

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/artifact"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/operation"
	"io"
)

type ArtifactContent interface {
	io.Reader
	io.Seeker
	io.Closer
}
type ArtifactWriter interface {
	io.Writer
	Complete(context.Context, bool) (artifact.Metadata, error)
	Abort(context.Context) error
}
type ArtifactFiles interface {
	Create(context.Context, string, string, string) (ArtifactWriter, error)
	Metadata(context.Context, string) (artifact.Metadata, error)
	List(context.Context, string) ([]artifact.Metadata, error)
	Open(context.Context, string) (artifact.Metadata, ArtifactContent, error)
	Delete(context.Context, string, operation.Mutation) error
	Cleanup(context.Context) error
}
type ArtifactRepository interface {
	ReserveArtifact(context.Context, artifact.Metadata) error
	FinishArtifact(context.Context, artifact.Metadata) error
	AbortArtifact(context.Context, string) error
	Artifact(context.Context, string) (artifact.Metadata, error)
	JobArtifacts(context.Context, string) ([]artifact.Metadata, error)
	RemoveArtifact(context.Context, string, operation.Mutation) error
	ArtifactReplayed(context.Context, operation.Mutation) (bool, error)
	ArtifactCandidates(context.Context) ([]artifact.Metadata, error)
}
type LocalRoots interface {
	LocalRoots(context.Context) ([]artifact.Root, error)
}
