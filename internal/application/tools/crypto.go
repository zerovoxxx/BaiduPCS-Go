package tools

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/tool"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

func (s *Service) RememberSecret(binding, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.secretSalt == nil {
		s.secretSalt = make([]byte, 32)
		if _, e := rand.Read(s.secretSalt); e != nil {
			return "", e
		}
	}
	mac := hmac.New(sha256.New, s.secretSalt)
	mac.Write([]byte(binding))
	mac.Write([]byte{0})
	mac.Write([]byte(key))
	ref := "secret_" + hex.EncodeToString(mac.Sum(nil))
	if s.fingerprint != nil {
		ref = "secret_" + s.fingerprint.FingerprintSecret(binding+"\x00"+key)
	}
	if _, ok := s.secrets[ref]; !ok && len(s.secrets) >= 36 {
		return "", fault.New(fault.RateLimited, "SECRET_LIMIT", "临时密钥任务过多")
	}
	s.secrets[ref] = []byte(key)
	return ref, nil
}
func (s *Service) crypto(ctx context.Context, r transfer.Record, p tool.Plan, save func(json.RawMessage) error) error {
	s.mu.Lock()
	key := append([]byte{}, s.secrets[p.KeyRef]...)
	s.mu.Unlock()
	if len(key) == 0 {
		return fault.New(fault.Unsupported, "TEMPORARY_SECRET_LOST", "临时密钥在重启或清理后不可恢复，请重新提交")
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
		s.ForgetSecret(p.KeyRef)
	}()
	var opts struct {
		Method string `json:"method"`
		Gzip   bool   `json:"gzip"`
		Remove bool   `json:"remove_source"`
	}
	if e := json.Unmarshal(p.Options, &opts); e != nil {
		return e
	}
	result := transfer.ScanResult{Complete: true, Counts: transfer.Counts{Total: uint64(len(p.Units))}, ArtifactIDs: []string{}}
	defer s.saveIncomplete(r.ID, &result)
	for i, u := range p.Units {
		if e := ctx.Err(); e != nil {
			if err := s.cancelRemaining(r, p, i, &result); err != nil {
				return err
			}
			return e
		}
		n, runErr := s.local.Transform(ctx, u.Local, *p.Destination, opts.Method, key, opts.Gzip, opts.Remove, r.Request.Kind == "local_encryption", save)
		if fault.As(runErr).Code == "LOCAL_RESULT_UNKNOWN" {
			return runErr
		}
		item := transfer.Item{ID: fmt.Sprint("item_", i), Index: uint64(i), RootID: u.Local.RootID, RelativePath: u.Local.Path, State: "succeeded", Attempts: 1, Written: n}
		result.Counts.Processed++
		if runErr != nil && ctx.Err() != nil {
			item.State = "cancelled"
			result.Counts.Cancelled++
			result.Complete = false
		} else if runErr != nil {
			item.State = "failed"
			item.Failure = fault.As(runErr)
			result.Counts.Failed++
		} else {
			result.Counts.Succeeded++
			result.Written += n
		}
		if e := s.saveItem(r.ID, item, result); e != nil {
			return fault.New(fault.Conflict, "LOCAL_RESULT_UNKNOWN", "本地结果已提交但逐项回执未保存")
		}
		if fault.As(runErr).Code == "LOCAL_RESULT_UNKNOWN" {
			return runErr
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.repo.SaveScanResult(ctx, r.ID, result)
}
