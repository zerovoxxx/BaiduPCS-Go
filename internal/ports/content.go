package ports

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/content"
	"io"
)

type ContentGateway interface {
	OpenContent(context.Context, configuration.Snapshot, account.Credentials) (ContentSession, error)
}
type ContentSession interface {
	Metadata(string) (content.Metadata, error)
	Links(string, string, int) ([]content.Link, error)
	Open(context.Context, string, int64, int64, int64) (io.ReadCloser, error)
	ResolveIDs([]int64) ([]string, error)
	Close() error
}

type DownloadTickets interface {
	IssueDownloadTicket(context.Context, content.Ticket, string) (string, error)
	ReadDownloadTicket(context.Context, string) (content.Ticket, error)
}
