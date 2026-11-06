package baidu

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

type uploadSettingsValue struct {
	Parallel    int    `json:"parallel_per_file"`
	Policy      string `json:"policy"`
	SkipRapid   bool   `json:"skip_rapid_check"`
	Check       bool   `json:"check_filename"`
	Retries     int    `json:"max_retries"`
	Concurrency int    `json:"file_concurrency"`
}

func uploadSettings(r transfer.Record) uploadSettingsValue {
	o := uploadSettingsValue{Parallel: 1, Policy: "skip", SkipRapid: true, Concurrency: 1}
	_ = json.Unmarshal(r.EffectiveOptions, &o)
	return o
}
func uploadBlockSize(n int64) int64 {
	if n >= baidupcs.MaxUploadThreshold {
		return baidupcs.MaxUploadBlockSize
	}
	if n >= baidupcs.MiddleUploadThreshold {
		return baidupcs.MiddleUploadBlockSize
	}
	return baidupcs.MinUploadBlockSize
}
func blockDigests(f *os.File, n, block int64, contexts ...context.Context) ([]string, error) {
	out := []string{}
	for off := int64(0); off < n; off += block {
		h := md5.New()
		if _, err := io.Copy(h, contextReader{Reader: io.NewSectionReader(f, off, min(block, n-off)), ctx: firstContext(contexts)}); err != nil {
			return nil, err
		}
		out = append(out, hex.EncodeToString(h.Sum(nil)))
	}
	if n == 0 {
		out = append(out, baidupcs.EmptyContentMD5)
	}
	return out, nil
}
func prepareUpload(ctx context.Context, r transfer.Record, s *Session, f *os.File, cp *fileCheckpoint, persist func() error, o uploadSettingsValue) error {
	if o.Check && strings.ContainsAny(pathBase(r.Request.RemotePath), "\\:*?\"<>|\x00\r\n") {
		return unsupported("INVALID_FILENAME", "目标文件名含不允许字符")
	}
	if cp.BlockSize == 0 {
		cp.BlockSize = uploadBlockSize(cp.Size)
	}
	if len(cp.BlockMD5) == 0 {
		var err error
		cp.BlockMD5, err = blockDigests(f, cp.Size, cp.BlockSize, ctx)
		if err != nil {
			return err
		}
	}
	if cp.Upload != nil {
		if cp.Upload.Uploadid == "" {
			return unsupported("CHECKPOINT_BINDING_MISMATCH", "检查点缺少uploadid")
		}
		blocks := uploader.SplitBlock(cp.Size, cp.BlockSize)
		if len(blocks) != len(cp.Upload.BlockList) {
			return unsupported("CHECKPOINT_BINDING_MISMATCH", "检查点分片布局不一致")
		}
		for i, b := range cp.Upload.BlockList {
			if b.ID != i || (b.Range.Begin != blocks[i].Range.Begin || b.Range.End != blocks[i].Range.End) || b.CheckSum != "" && !strings.EqualFold(b.CheckSum, cp.BlockMD5[i]) {
				return unsupported("CHECKPOINT_BINDING_MISMATCH", "检查点分片范围或摘要不一致")
			}
		}
		return nil
	}
	meta, metaErr := s.PCS.FilesDirectoriesMeta(r.Request.RemotePath)
	var err error = metaErr
	if err == nil {
		if meta.Isdir {
			return fault.New(fault.Conflict, "TARGET_EXISTS", "上传目标是目录")
		}
		if o.Policy == "skip" || o.Policy == "rsync" && meta.Size == cp.Size {
			cp.Skipped = true
			cp.Phase = "done"
			return persist()
		}
	} else if fault.As(upstreamFault(err)).Kind != fault.NotFound {
		return err
	}
	cp.Phase = "preparing"
	if !o.SkipRapid {
		cp.Phase = "committing"
	}
	if err = persist(); err != nil {
		return err
	}
	var uploadID string
	if !o.SkipRapid {
		uk, e := s.PCS.CacheUK()
		if e != nil {
			cp.Phase = "preparing"
			return e
		}
		now := time.Now().Unix()
		mixed := md5.Sum([]byte(fmt.Sprintf("%d%s%d", uk, cp.MD5, now)))
		raw := int64(0)
		for _, b := range mixed[:4] {
			raw = raw*256 + int64(b)
		}
		length := min(int64(256<<10), cp.Size)
		offset := int64(0)
		if cp.Size > length {
			offset = raw % (cp.Size - length + 1)
		}
		data := make([]byte, length)
		if length > 0 {
			if _, err = f.ReadAt(data, offset); err != nil {
				return err
			}
		}
		slice := md5.New()
		if _, err = io.Copy(slice, io.NewSectionReader(f, 0, min(cp.Size, int64(baidupcs.SliceMD5Size)))); err != nil {
			return err
		}
		rapidErr, pre := s.PCS.RapidUpload(r.Request.RemotePath, o.Policy, "", cp.MD5, hex.EncodeToString(slice.Sum(nil)), strings.TrimRight(base64.StdEncoding.EncodeToString(data), "="), "0", offset, length, cp.Size, now, cp.BlockMD5)
		if rapidErr == nil && pre.ReturnType == 2 {
			cp.Rapid = true
			cp.Phase = "done"
			return persist()
		}
		if rapidErr != nil {
			if rapidErr.GetErrType() != pcserror.ErrTypeRemoteError {
				return fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "秒传提交结果未确认，须核验")
			}
			if rapidErr.GetRemoteErrCode() != -9 {
				cp.Phase = "preparing"
				if err = persist(); err != nil {
					return err
				}
				return rapidErr
			}
			// Some current handshakes return -9 with an uploadid instead of return_type=1.
		}
		uploadID = pre.UploadID
	}
	if uploadID == "" {
		err, pre := s.PCS.FakeRapidUpload(r.Request.RemotePath, o.Policy, cp.Size)
		if err != nil {
			return err
		}
		uploadID = pre.UploadID
	}
	if uploadID == "" {
		return fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "上游握手缺少uploadid")
	}
	cp.Upload = &uploader.InstanceState{Uploadid: uploadID, BlockList: uploader.SplitBlock(cp.Size, cp.BlockSize)}
	cp.Phase = "uploading"
	return persist()
}
func pathBase(s string) string {
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}
