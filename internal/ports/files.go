package ports

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
)

type FileReader interface {
	OpenFiles(context.Context, configuration.Snapshot, account.Credentials) (FileSession, error)
}
type FileSession interface {
	Meta(string) (file.Entry, error)
	List(string, file.ListOptions) ([]file.Entry, error)
	Search(string, string, bool) ([]file.Entry, error)
	Close() error
}
type QueryPage struct {
	Items                 []json.RawMessage
	NextCursor, ExpiresAt string
	HasMore               bool
}
type QuerySnapshots interface {
	CreateQuery(context.Context, string, []json.RawMessage, int) (QueryPage, error)
	ReadQuery(context.Context, string, string, int) (QueryPage, error)
}
