package baidu

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/content"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
)

type contentSession struct{ *Session }

func (f *ClientFactory) OpenContent(ctx context.Context, cfg configuration.Snapshot, c account.Credentials) (ports.ContentSession, error) {
	if c.Cookies["BDUSS"] == "" {
		return nil, unsupported("BAIDU_COOKIE_REQUIRED", "下载需要BDUSS")
	}
	s, err := f.New(ctx, cfg, c)
	if err != nil {
		return nil, err
	}
	s.HTTP.SetTimeout(0)
	s.HTTP.Client.Transport = pacedTransport{base: s.HTTP.Client.Transport, limiter: rio.NewLimiter(cfg.Client().MaxDownloadRate), ctx: ctx}
	return &contentSession{s}, nil
}
func (s *contentSession) Metadata(p string) (content.Metadata, error) {
	v, err := s.PCS.FilesDirectoriesMeta(p)
	if err != nil {
		return content.Metadata{}, upstreamFault(err)
	}
	if v.Isdir || v.Size < 0 {
		return content.Metadata{}, unsupported("CONTENT_REQUIRES_FILE", "内容代理只读取普通文件")
	}
	return content.Metadata{Path: p, Name: pathBase(p), MD5: v.MD5, Size: v.Size, Mtime: v.Mtime}, nil
}
func (s *contentSession) Links(p, source string, index int) ([]content.Link, error) {
	urls := []string{}
	cookie := true
	ua := s.HTTP.UserAgent
	switch source {
	case "pcs":
		info, err := s.PCS.LocateDownload(p)
		if err != nil {
			return nil, upstreamFault(err)
		}
		for _, u := range info.URLStrings(true) {
			urls = append(urls, u.String())
		}
		cookie = false
	case "pan":
		meta, err := s.PCS.FilesDirectoriesMeta(p)
		if err != nil {
			return nil, upstreamFault(err)
		}
		links, err := s.PCS.LocatePanAPIDownload(meta.FsID)
		if err != nil {
			return nil, upstreamFault(err)
		}
		for _, u := range links {
			urls = append(urls, u.Dlink)
		}
		ua = baidupcs.NetdiskUA
	case "direct":
		address, err := downloadURL(s.Session, p, "pcs", 0)
		if err != nil {
			return nil, err
		}
		urls = append(urls, address)
	default:
		return nil, unsupported("UNSUPPORTED_OPTION", "未知直链来源")
	}
	if index >= len(urls) {
		return nil, unsupported("DOWNLOAD_LINK_INDEX_UNAVAILABLE", "指定直链序号不可用")
	}
	out := []content.Link{}
	for _, address := range urls[index:] {
		u, err := url.Parse(address)
		if err != nil || !allowedURL(u) {
			return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "上游直链不允许")
		}
		var nonce [16]byte
		if _, err = rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		out = append(out, content.Link{URL: address, Reference: "link_" + hex.EncodeToString(nonce[:]), UserAgent: ua, RequiresCookie: cookie})
	}
	return out, nil
}
func (s *contentSession) Open(ctx context.Context, p string, first, last, total int64) (io.ReadCloser, error) {
	address, err := downloadURL(s.Session, p, "pcs", 0)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.HTTP.UserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	partial := first != 0 || last != total-1
	if partial {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", first, last))
	}
	resp, err := s.HTTP.Client.Do(req)
	if err != nil {
		return nil, upstreamFault(err)
	}
	valid := resp.StatusCode == 200
	if partial {
		valid = resp.StatusCode == 206 && resp.Header.Get("Content-Range") == fmt.Sprintf("bytes %d-%d/%d", first, last, total)
	}
	if !valid || resp.ContentLength != last-first+1 || resp.Header.Get("Content-Encoding") != "" {
		resp.Body.Close()
		return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "上游内容状态、Range或长度不一致")
	}
	return resp.Body, nil
}
func (s *contentSession) ResolveIDs(ids []int64) ([]string, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, fault.New(fault.Invalid, "INVALID_REQUEST", "文件ID数量无效")
	}
	encoded, _ := json.Marshal(ids)
	u := "https://pan.baidu.com/api/filemetas?dlink=0&fsids=" + url.QueryEscape(string(encoded))
	resp, err := s.HTTP.Req(http.MethodGet, u, nil, nil)
	if err != nil {
		return nil, upstreamFault(err)
	}
	defer resp.Body.Close()
	var result struct {
		*pcserror.PanErrorInfo
		Info []struct {
			ID   int64  `json:"fs_id"`
			Path string `json:"path"`
		}
	}
	result.PanErrorInfo = pcserror.NewPanErrorInfo("filemetas")
	if err := pcserror.HandleJSONParse("filemetas", io.LimitReader(resp.Body, 32<<20), &result); err != nil {
		return nil, upstreamFault(err)
	}
	byID := map[int64]string{}
	for _, v := range result.Info {
		if !path.IsAbs(v.Path) || path.Clean(v.Path) != v.Path {
			return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "ID元数据缺少路径")
		}
		byID[v.ID] = v.Path
	}
	out := []string{}
	for _, id := range ids {
		p, ok := byID[id]
		if !ok {
			return nil, fault.New(fault.NotFound, "REMOTE_FILE_NOT_FOUND", "未取得文件ID "+strconv.FormatInt(id, 10))
		}
		out = append(out, p)
	}
	return out, nil
}
