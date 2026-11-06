package baidu

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type toolSession struct{ *Session }

func (f *ClientFactory) OpenTools(ctx context.Context, cfg configuration.Snapshot, c account.Credentials) (ports.ToolSession, error) {
	s, e := f.writeSession(ctx, cfg, c)
	if e != nil {
		return nil, e
	}
	return &toolSession{s}, nil
}
func checksumUnavailable() error {
	return fault.New(fault.Unsupported, "CHECKSUM_UNAVAILABLE", "上游未提供所需的可信摘要")
}
func (s *toolSession) Inspect(p string) (tool.Checksum, error) {
	meta, e := s.PCS.FilesDirectoriesMeta(p)
	if e != nil {
		return tool.Checksum{}, upstreamFault(e)
	}
	if meta.Isdir {
		return tool.Checksum{}, unsupported("CHECKSUM_REQUIRES_FILE", "摘要只支持普通文件")
	}
	address, e2 := downloadURL(s.Session, p, "pcs", 0)
	if e2 != nil {
		return tool.Checksum{}, e2
	}
	v, err := s.InspectLink(address)
	if err != nil {
		return v, err
	}
	if v.Size != strconv.FormatInt(meta.Size, 10) {
		return v, fault.New(fault.Conflict, "REMOTE_FILE_CHANGED", "元数据和内容长度不一致")
	}
	v.Blocks = meta.BlockList
	return v, nil
}
func (s *toolSession) InspectLink(address string, agents ...string) (tool.Checksum, error) {
	v := tool.Checksum{Status: "missing"}
	u, err := url.Parse(address)
	if err != nil || !allowedURL(u) {
		return v, fault.New(fault.Forbidden, "FORBIDDEN", "摘要链接目标不允许")
	}
	req, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return v, err
	}
	req.Header.Set("Range", "bytes=0-262143")
	req.Header.Set("Accept-Encoding", "identity")
	agent := baidupcs.NetdiskUA
	if len(agents) > 0 && agents[0] != "" {
		agent = agents[0]
	}
	req.Header.Set("User-Agent", agent)
	resp, err := s.HTTP.Client.Do(req)
	if err != nil {
		return v, upstreamFault(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return v, fault.New(fault.BadGateway, "UPSTREAM_ERROR", "摘要读取被上游拒绝")
	}
	size := resp.ContentLength
	if resp.StatusCode == 206 {
		var first, last int64
		if _, err = fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &first, &last, &size); err != nil || first != 0 || last != min(size-1, 262143) || last-first+1 != resp.ContentLength {
			return v, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "摘要Range无效")
		}
	}
	if size < 0 || resp.Header.Get("Content-Encoding") != "" {
		return v, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "摘要缺少可信长度")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, min(size, 262144)+1))
	if err != nil {
		return v, upstreamFault(err)
	}
	// A server may ignore Range. Read only the bounded prefix, then close.
	if int64(len(data)) < min(size, 262144) {
		return v, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "摘要内容过短")
	}
	if len(data) > 262144 {
		data = data[:262144]
	}
	v.Size = strconv.FormatInt(size, 10)
	sum := md5.Sum(data)
	slice := hex.EncodeToString(sum[:])
	v.SliceMD5 = &slice
	raw := strings.ToLower(resp.Header.Get("Content-MD5"))
	if decoded, e := base64.StdEncoding.DecodeString(resp.Header.Get("Content-MD5")); e == nil && len(decoded) == 16 {
		raw = hex.EncodeToString(decoded)
	}
	if b, e := hex.DecodeString(raw); e == nil && len(b) == 16 {
		v.MD5 = &raw
		v.Status = "reported"
	}
	if size <= 262144 {
		v.MD5 = &slice
		v.Status = "verified"
	}
	if raw := resp.Header.Get("x-bs-meta-crc32"); raw != "" {
		n, e := strconv.ParseUint(raw, 10, 32)
		if e == nil && strconv.FormatUint(n, 10) == raw {
			v.CRC32 = &raw
		}
	}
	return v, nil
}
func (s *toolSession) Fix(p string, v tool.Checksum) error {
	if v.MD5 == nil || v.SliceMD5 == nil || v.CRC32 == nil {
		return checksumUnavailable()
	}
	n, e := strconv.ParseInt(v.Size, 10, 64)
	if e != nil {
		return e
	}
	return writeFault(s.PCS.RapidUploadNoCheckDir(p, *v.MD5, *v.SliceMD5, *v.CRC32, n))
}
func (s *toolSession) Changes(cursor string) (map[string]any, error) {
	body, e := s.PCS.PrepareFilesDirectoriesDiff(cursor)
	if e != nil {
		return nil, upstreamFault(e)
	}
	defer body.Close()
	var raw struct {
		Errno     *int              `json:"errno"`
		ErrorCode *int              `json:"error_code"`
		Cursor    *string           `json:"cursor"`
		HasMore   *binaryFlag       `json:"has_more"`
		Reset     *binaryFlag       `json:"reset"`
		Entries   []json.RawMessage `json:"entries"`
	}
	dec := json.NewDecoder(io.LimitReader(body, 8<<20))
	if e := dec.Decode(&raw); e != nil {
		return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "增量响应无效")
	}
	code := raw.Errno
	if code == nil {
		code = raw.ErrorCode
	}
	if code == nil {
		return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "增量响应缺少状态")
	}
	if *code == -6 || *code == 31045 {
		return nil, fault.New(fault.Unsupported, "BAIDU_CREDENTIAL_EXPIRED", "百度会话不可用，请更新凭据")
	}
	if *code != 0 {
		return nil, fault.New(fault.Unavailable, "UPSTREAM_CAPABILITY_UNAVAILABLE", "百度增量接口当前不可用")
	}
	if raw.HasMore == nil || raw.Reset == nil || len(raw.Entries) > 1000 || (!bool(*raw.Reset) && (raw.Cursor == nil || *raw.Cursor == "")) {
		return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "增量控制字段无效")
	}
	entries := []any{}
	for _, b := range raw.Entries {
		var x struct {
			ID      json.Number `json:"fs_id"`
			Path    string      `json:"path"`
			Deleted int         `json:"isdelete"`
			Dir     int         `json:"isdir"`
			Size    *int64      `json:"size"`
			MD5     *string     `json:"md5"`
		}
		d := json.NewDecoder(strings.NewReader(string(b)))
		d.UseNumber()
		if e := d.Decode(&x); e != nil {
			return nil, e
		}
		id := x.ID.String()
		n, idErr := strconv.ParseUint(id, 10, 64)
		if idErr != nil || n == 0 || strconv.FormatUint(n, 10) != id {
			return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "增量文件ID无效")
		}
		var p any
		if x.Path != "" {
			if !path.IsAbs(x.Path) || path.Clean(x.Path) != x.Path {
				return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "增量路径无效")
			}
			p = x.Path
		}
		if x.Deleted == 1 {
			entries = append(entries, map[string]any{"change": "delete", "fs_id": id, "path": p})
			continue
		}
		if x.Size == nil || *x.Size < 0 || p == nil {
			return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "增量文件字段缺失")
		}
		status := "missing"
		if x.MD5 != nil {
			status = "possibly_inaccurate"
		}
		entries = append(entries, map[string]any{"change": "upsert", "fs_id": id, "file": map[string]any{"fs_id": id, "path": x.Path, "name": path.Base(x.Path), "is_dir": x.Dir == 1, "size_bytes": strconv.FormatInt(*x.Size, 10), "md5_status": status, "md5": x.MD5}})
	}
	return map[string]any{"entries": entries, "cursor": raw.Cursor, "has_more": bool(*raw.HasMore), "reset_required": bool(*raw.Reset)}, nil
}
func (s *toolSession) Servers() (map[string]any, error) {
	body, e := s.PCS.PreparePCSServers()
	if e != nil {
		return nil, upstreamFault(e)
	}
	defer body.Close()
	v := &baidupcs.PCSInfo{PCSErrInfo: pcserror.NewPCSErrorInfo(baidupcs.OperationGetPCSServer)}
	if e := pcserror.HandleJSONParse(baidupcs.OperationGetPCSServer, io.LimitReader(body, 1<<20), v); e != nil {
		return nil, upstreamFault(e)
	}
	hosts := append([]string{}, v.Server...)
	for _, x := range v.Servers {
		u, e := url.Parse(x.ServerAddr)
		if e == nil {
			hosts = append(hosts, u.Hostname())
		}
	}
	out := []any{}
	seen := map[string]bool{}
	for _, h := range hosts {
		if seen[h] {
			continue
		}
		u, e := url.Parse("https://" + h)
		if e != nil || !allowedURL(u) || u.Hostname() != h || strings.ContainsAny(h, "/@?#:") {
			return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "PCS节点无效")
		}
		seen[h] = true
		out = append(out, map[string]any{"host": h, "https": true})
	}
	if len(out) == 0 {
		return nil, fault.New(fault.Unavailable, "UPSTREAM_CAPABILITY_UNAVAILABLE", "百度未返回可用PCS节点")
	}
	if len(out) > 100 {
		return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "PCS节点过多")
	}
	return map[string]any{"servers": out, "expires_at": nil}, nil
}

func (s *toolSession) VerifyLink(address, algorithm, expected string, agents ...string) (tool.Checksum, error) {
	v := tool.Checksum{Status: "verified"}
	u, e := url.Parse(address)
	if e != nil || !allowedURL(u) {
		return v, fault.New(fault.Forbidden, "FORBIDDEN", "链接目标无效")
	}
	req, e := http.NewRequest("GET", address, nil)
	if e != nil {
		return v, e
	}
	agent := baidupcs.NetdiskUA
	if len(agents) > 0 && agents[0] != "" {
		agent = agents[0]
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept-Encoding", "identity")
	resp, e := s.HTTP.Client.Do(req)
	if e != nil {
		return v, upstreamFault(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength < 0 || resp.ContentLength > 128<<30 || resp.Header.Get("Content-Encoding") != "" {
		return v, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "摘要内容长度或状态无效")
	}
	md, sha, sl, crc := md5.New(), sha256.New(), md5.New(), crc32.NewIEEE()
	buf := make([]byte, 64<<10)
	var n int64
	for {
		got, err := resp.Body.Read(buf)
		if got > 0 {
			b := buf[:got]
			md.Write(b)
			sha.Write(b)
			crc.Write(b)
			if n < 262144 {
				sl.Write(b[:min(int64(got), 262144-n)])
			}
			n += int64(got)
			if n > resp.ContentLength {
				return v, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "内容超过声明长度")
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return v, upstreamFault(err)
		}
	}
	if n != resp.ContentLength {
		return v, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "摘要内容未完整接收")
	}
	full, slice, crcstr := hex.EncodeToString(md.Sum(nil)), hex.EncodeToString(sl.Sum(nil)), strconv.FormatUint(uint64(crc.Sum32()), 10)
	sum := full
	if algorithm == "sha256" {
		sum = hex.EncodeToString(sha.Sum(nil))
	}
	if !strings.EqualFold(sum, expected) {
		return v, fault.New(fault.Conflict, "CHECKSUM_MISMATCH", "链接摘要与期望不一致")
	}
	v.Size = strconv.FormatInt(n, 10)
	v.MD5 = &full
	v.SliceMD5 = &slice
	v.CRC32 = &crcstr
	return v, nil
}

type binaryFlag bool

func (f *binaryFlag) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "0", "false":
		*f = false
	case "1", "true":
		*f = true
	default:
		return fmt.Errorf("invalid binary flag")
	}
	return nil
}
