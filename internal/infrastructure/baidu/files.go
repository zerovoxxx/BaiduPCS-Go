package baidu

import (
	"context"
	"encoding/hex"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"io"
	"net/http"
	"path"
	"strconv"
	"time"
	"unicode/utf8"
)

type fileSession struct{ *Session }

func (f *ClientFactory) OpenFiles(ctx context.Context, cfg configuration.Snapshot, c account.Credentials) (ports.FileSession, error) {
	if c.Cookies["BDUSS"] == "" {
		return nil, fault.New(fault.Unsupported, "BAIDU_COOKIE_REQUIRED", "此文件能力需要BDUSS Cookie凭据")
	}
	s, err := f.New(ctx, cfg, c)
	if err != nil {
		return nil, err
	}
	s.HTTP.Client.Transport = boundedFileTransport{s.HTTP.Client.Transport}
	return &fileSession{s}, nil
}
func remoteEntry(v *baidupcs.FileDirectory) (file.Entry, error) {
	if v == nil || v.FsID < 0 || v.Size < 0 || !path.IsAbs(v.Path) || path.Clean(v.Path) != v.Path || len(v.Path) > 4096 || !utf8.ValidString(v.Path) {
		return file.Entry{}, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度文件元数据无效")
	}
	name := v.Filename
	if name == "" {
		name = path.Base(v.Path)
	}
	if utf8.RuneCountInString(name) > 1024 || !utf8.ValidString(name) || v.Ctime > 253402300799 || v.Mtime > 253402300799 || len(v.BlockList) > 100000 {
		return file.Entry{}, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度文件字段超出有效范围")
	}
	for _, b := range v.BlockList {
		decoded, err := hex.DecodeString(b)
		if err != nil || len(decoded) != 16 {
			return file.Entry{}, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度分块摘要格式无效")
		}
	}
	e := file.Entry{ID: strconv.FormatInt(v.FsID, 10), Path: v.Path, Name: name, IsDir: v.Isdir, Size: v.Size, MD5Status: "missing", Blocks: append([]string{}, v.BlockList...)}
	if v.AppID > 0 {
		s := strconv.FormatInt(v.AppID, 10)
		e.AppID = &s
	}
	if v.Ctime > 0 {
		s := time.Unix(v.Ctime, 0).UTC().Format(time.RFC3339)
		e.CreatedAt = &s
	}
	if v.Mtime > 0 {
		s := time.Unix(v.Mtime, 0).UTC().Format(time.RFC3339)
		e.ModifiedAt = &s
	}
	if m, err := hex.DecodeString(v.MD5); err == nil && len(m) == 16 {
		md5 := v.MD5
		e.MD5 = &md5
		e.MD5Status = "reported"
		if len(v.BlockList) > 1 {
			e.MD5Status = "possibly_inaccurate"
		}
	}
	if v.Isdir && v.Ifhassubdir {
		yes := true
		e.HasSubdirectories = &yes
	}
	// Pan list does not report ifhassubdir; a false Go zero is not evidence.
	return e, nil
}
func entries(v baidupcs.FileDirectoryList, err error) ([]file.Entry, error) {
	if err != nil {
		return nil, upstreamFault(err)
	}
	out := make([]file.Entry, 0, len(v))
	for _, x := range v {
		e, err := remoteEntry(x)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}
func (s *fileSession) Meta(p string) (file.Entry, error) {
	v, err := s.PCS.FilesDirectoriesMeta(p)
	if err != nil {
		return file.Entry{}, upstreamFault(err)
	}
	e, conversionErr := remoteEntry(v)
	if conversionErr == nil && e.Path != p {
		return file.Entry{}, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度元数据未对应请求路径")
	}
	return e, conversionErr
}
func (s *fileSession) List(p string, o file.ListOptions) ([]file.Entry, error) {
	v, err := s.PCS.FilesDirectoriesListPage(p, &baidupcs.OrderOptions{By: baidupcs.OrderBy(o.OrderBy), Order: baidupcs.Order(o.Direction)}, o.PageSize, o.Page)
	out, e := entries(v, err)
	if e != nil {
		return nil, e
	}
	if len(out) > o.PageSize {
		return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度目录响应超过请求分页大小")
	}
	for _, v := range out {
		if path.Dir(v.Path) != p || v.Path == p {
			return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度目录条目不属于请求目录")
		}
	}
	return out, nil
}
func (s *fileSession) Search(p, k string, r bool) ([]file.Entry, error) {
	v, err := s.PCS.Search(p, k, r)
	out, e := entries(v, err)
	if e != nil {
		return nil, e
	}
	for _, v := range out {
		if !under(p, v.Path) {
			return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度搜索条目超出请求目录")
		}
	}
	return out, nil
}
func under(parent, child string) bool {
	return parent == "/" || child == parent || len(child) > len(parent) && child[:len(parent)+1] == parent+"/"
}

type boundedFileTransport struct{ base http.RoundTripper }
type boundedFileBody struct {
	io.Reader
	io.Closer
}

func (t boundedFileTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(r)
	if err == nil {
		response.Body = boundedFileBody{Reader: io.LimitReader(response.Body, 32<<20), Closer: response.Body}
	}
	return response, err
}
