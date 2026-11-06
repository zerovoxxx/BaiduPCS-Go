package ports

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"io"
)

type ToolGateway interface {
	OpenTools(context.Context, configuration.Snapshot, account.Credentials) (ToolSession, error)
}
type ToolSession interface {
	Inspect(string) (tool.Checksum, error)
	InspectLink(string, ...string) (tool.Checksum, error)
	VerifyLink(string, string, string, ...string) (tool.Checksum, error)
	Fix(string, tool.Checksum) error
	Changes(string) (map[string]any, error)
	Servers() (map[string]any, error)
	Close() error
}
type ToolFiles interface {
	PrepareTool(context.Context, tool.Plan) error
	Checksum(context.Context, tool.LocalFile, []string) (tool.Checksum, error)
	Transform(context.Context, tool.LocalFile, configuration.Directory, string, []byte, bool, bool, bool, func(json.RawMessage) error) (int64, error)
	CreateToolOutput(context.Context, configuration.Directory, string, func(json.RawMessage) error) (ToolOutput, error)
	CleanupTool(context.Context, json.RawMessage) error
}
type ToolOutput interface {
	io.Writer
	Commit(context.Context) error
	Abort()
}

// SecretFingerprint binds idempotency without retaining the temporary secret.
// Its key belongs to service storage; fingerprints never enter public DTOs.
type SecretFingerprint interface{ FingerprintSecret(string) string }
