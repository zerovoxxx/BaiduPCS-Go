package baidu

import (
	"context"
	"os"
	"runtime"
	"strings"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

func (e *SingleFileEngine) Prepare(ctx context.Context, r transfer.Record, c account.Credentials) (string, error) {
	if !r.Config.EnableHTTPS {
		return "", unsupported("HTTPS_REQUIRED", "百度凭据请求必须启用HTTPS")
	}
	if c.Cookies["BDUSS"] == "" {
		return "", unsupported("UNSUPPORTED_CREDENTIAL", "P2单文件传输要求含BDUSS的Cookie凭据；token-only仅开放独立在线验证")
	}
	if r.Config.ForceLoginUsername != "" {
		return "", unsupported("UNSUPPORTED_OPTION", "P2不支持force_login_username身份回退")
	}
	if len(r.Plan) > 0 {
		return "batch:" + r.Request.AccountID + ":" + r.Request.Kind + ":" + r.Request.RootID + ":" + r.Request.RemotePath, nil
	}
	name, err := e.roots.File(ctx, r.Request.RootID, r.Request.RelativePath, r.Request.Kind == "download")
	if err != nil {
		if f := fault.As(err); f.Kind == fault.Unsupported || f.Kind == fault.Conflict {
			return "", f
		}
		if os.IsNotExist(err) {
			return "", fault.New(fault.NotFound, "LOCAL_FILE_NOT_FOUND", "本地文件或目标父目录不存在")
		}
		return "", fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "本地文件或目标父目录不可访问")
	}
	if r.Request.Kind == "upload" {
		file, err := e.roots.OpenFile(ctx, r.Request.RootID, r.Request.RelativePath, os.O_RDONLY, 0)
		if err != nil {
			return "", upstreamFault(err)
		}
		defer file.Close()
		if _, _, _, _, err = sourceFingerprint(file, ctx); err != nil {
			return "", err
		}
		return "remote:" + r.Request.AccountID + ":" + r.Request.RemotePath, nil
	}
	if _, err = os.Lstat(name); !downloadSettings(r).Overwrite && !downloadSettings(r).Test && (err == nil || !os.IsNotExist(err)) {
		return "", fault.New(fault.Conflict, "TARGET_EXISTS", "下载目标已存在")
	}
	s, err := e.clients.New(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
	if err != nil {
		return "", err
	}
	defer s.Close()
	meta, err := s.PCS.FilesDirectoriesMeta(r.Request.RemotePath)
	if err != nil {
		return "", upstreamFault(err)
	}
	if meta.Isdir || meta.Size < 0 || meta.Size > 128<<30 {
		return "", unsupported("UNSUPPORTED_OPTION", "P2仅支持1字节至32MiB的普通文件")
	}
	if downloadSettings(r).Verify && (len(meta.MD5) != 32 || strings.Trim(meta.MD5, "0123456789abcdefABCDEF") != "") {
		return "", unsupported("CHECKSUM_UNAVAILABLE", "缺少可信摘要，不能执行已承诺的校验下载")
	}
	if runtime.GOOS == "windows" {
		name = strings.ToLower(name)
	}
	return "local:" + name, nil
}
