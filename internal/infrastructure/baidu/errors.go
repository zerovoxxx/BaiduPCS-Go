package baidu

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
)

func upstreamFault(err error) error {
	if err == nil {
		return nil
	}
	var f *fault.Error
	if errors.As(err, &f) {
		return f
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return &fault.Error{Kind: fault.Timeout, Code: "UPSTREAM_TIMEOUT", Message: "百度请求超时", Retryable: true}
	}
	var pcs pcserror.Error
	if errors.As(err, &pcs) {
		if pcs.GetErrType() == pcserror.ErrTypeJSONParseError {
			return fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度响应格式异常")
		}
		switch pcs.GetRemoteErrCode() {
		case 110, 111, 31023, 31024, 31041, 31045, -6:
			return fault.New(fault.Unsupported, "BAIDU_CREDENTIAL_EXPIRED", "百度凭据无效或已过期")
		case 31066, -9:
			return fault.New(fault.NotFound, "REMOTE_FILE_NOT_FOUND", "远端文件不存在")
		case 31061, -8:
			return fault.New(fault.Conflict, "TARGET_EXISTS", "远端目标已存在")
		}
		if pcs.GetError() != nil {
			return upstreamFault(pcs.GetError())
		}
	}
	if errors.Is(err, os.ErrPermission) {
		return fault.New(fault.Forbidden, "LOCAL_PATH_FORBIDDEN", "本地路径不可访问")
	}
	if errors.Is(err, os.ErrNotExist) {
		return fault.New(fault.NotFound, "LOCAL_FILE_NOT_FOUND", "本地文件不存在")
	}
	if errors.Is(err, syscall.ENOSPC) {
		return &fault.Error{Kind: fault.Unavailable, Code: "LOCAL_STORAGE_FULL", Message: "本地存储空间不足", Retryable: true}
	}
	return &fault.Error{Kind: fault.BadGateway, Code: "UPSTREAM_ERROR", Message: "百度传输或协议请求失败", Retryable: true}
}
