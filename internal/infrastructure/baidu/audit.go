package baidu

import (
	"context"
	"encoding/json"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"os"
	"strings"
)

func (e *SingleFileEngine) Audit(ctx context.Context, r transfer.Record, c account.Credentials) error {
	if c.Revision != r.CredentialRevision {
		return fault.New(fault.Conflict, "RECONCILIATION_IDENTITY_UNVERIFIED", "旧传输检查点缺少换凭据核验所需的原百度身份")
	}
	if len(r.Plan) > 0 {
		return fault.New(fault.Unsupported, "UNSUPPORTED_JOB_ACTION", "目录传输使用resume核验其逐项检查点")
	}
	var cp fileCheckpoint
	if json.Unmarshal(r.Checkpoint, &cp) != nil || cp.Binding != binding(r) || cp.Phase != "committing" && cp.Phase != "done" {
		return fault.New(fault.Conflict, "RECONCILIATION_NOT_REQUIRED", "此传输没有未知提交检查点")
	}
	if r.Request.Kind == "upload" {
		s, err := e.clients.New(ctx, configuration.NewSnapshot(r.ConfigRevision, r.Config), c)
		if err != nil {
			return err
		}
		defer s.Close()
		v, err := s.PCS.FilesDirectoriesMeta(r.Request.RemotePath)
		if err != nil || v.Isdir || v.Size != cp.Size || !strings.EqualFold(v.MD5, cp.MD5) {
			return fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "远端上传结果未确认")
		}
		return nil
	}
	f, err := e.roots.OpenFile(ctx, r.Request.RootID, r.Request.RelativePath, os.O_RDONLY, 0)
	if err != nil {
		return fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "本地提交结果未确认")
	}
	defer f.Close()
	size, _, sha, _, err := sourceFingerprint(f, ctx)
	if err != nil || size != cp.Size || sha != cp.SHA256 {
		return fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "本地提交摘要未确认")
	}
	return nil
}
