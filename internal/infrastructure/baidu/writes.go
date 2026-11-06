package baidu

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
	"strconv"
)

// A transport/parse failure after a write may have changed Baidu state. A known
// remote rejection is a definite item failure. These must never share retry code.
func writeFault(err error) error {
	if err == nil {
		return nil
	}
	if pcs, ok := err.(pcserror.Error); ok && pcs.GetErrType() == pcserror.ErrTypeRemoteError {
		return upstreamFault(err)
	}
	return fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "百度写入结果未确认，必须先核对，不能自动重试")
}
func (f *ClientFactory) writeSession(ctx context.Context, cfg configuration.Snapshot, c account.Credentials) (*Session, error) {
	if c.Cookies["BDUSS"] == "" {
		return nil, unsupported("BAIDU_COOKIE_REQUIRED", "网盘写业务需要BDUSS")
	}
	s, err := f.New(ctx, cfg, c)
	if err != nil {
		return nil, err
	}
	s.HTTP.Client.Transport = boundedFileTransport{base: s.HTTP.Client.Transport}
	return s, nil
}

type fileWriter struct{ *Session }

func (f *ClientFactory) OpenWriter(ctx context.Context, cfg configuration.Snapshot, c account.Credentials) (ports.FileWriteSession, error) {
	s, err := f.writeSession(ctx, cfg, c)
	if err != nil {
		return nil, err
	}
	return &fileWriter{s}, nil
}
func (s *fileWriter) Meta(p string) (file.Entry, error) { return (&fileSession{s.Session}).Meta(p) }
func (s *fileWriter) Mkdir(p string) error              { return writeFault(s.PCS.Mkdir(p)) }
func (s *fileWriter) Delete(p string) error             { return writeFault(s.PCS.Remove(p)) }
func (s *fileWriter) CopyMove(kind, from, to, policy string) error {
	op := baidupcs.OperationCopy
	if kind == "file_move" {
		op = baidupcs.OperationMove
	} else if kind == "file_rename" {
		op = baidupcs.OperationRename
	}
	return writeFault(s.PCS.CopyMovePolicy(op, policy, from, to))
}

type offlineSession struct{ *Session }

func (f *ClientFactory) OpenOffline(ctx context.Context, cfg configuration.Snapshot, c account.Credentials) (ports.OfflineSession, error) {
	s, err := f.writeSession(ctx, cfg, c)
	if err != nil {
		return nil, err
	}
	return &offlineSession{s}, nil
}
func (s *offlineSession) Add(u, p string) (string, error) {
	id, err := s.PCS.CloudDlAddTask(u, p)
	if err != nil {
		return "", writeFault(err)
	}
	if id <= 0 {
		return "", fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "离线创建响应缺少真实任务ID")
	}
	return strconv.FormatInt(id, 10), nil
}
func offlineEntries(tasks baidupcs.CloudDlTaskList) ([]write.Offline, error) {
	out := []write.Offline{}
	for _, t := range tasks {
		if t.TaskID <= 0 || t.Status < 0 || t.Status > 7 || t.FileSize < 0 || t.FinishedSize < 0 {
			return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "离线任务字段无效")
		}
		v := write.Offline{ID: strconv.FormatInt(t.TaskID, 10), Status: t.Status, StatusText: t.StatusText, Size: t.FileSize, Finished: t.FinishedSize, Created: t.CreateTime, Started: t.StartTime, Ended: t.FinishTime, Path: t.SavePath, URL: t.SourceURL, Name: t.TaskName, Type: t.OdType, Files: []write.OfflineFile{}, Missing: t.Result != 0}
		for _, f := range t.FileList {
			v.Files = append(v.Files, write.OfflineFile{Name: f.FileName, Size: f.FileSize})
		}
		out = append(out, v)
	}
	return out, nil
}
func (s *offlineSession) List() ([]write.Offline, error) {
	v, err := s.PCS.CloudDlListTask()
	if err != nil {
		return nil, upstreamFault(err)
	}
	if len(v) > 1000 {
		return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "离线任务超过上游1000项窗口")
	}
	return offlineEntries(v)
}
func parseIDs(values []string) ([]int64, error) {
	out := []int64{}
	seen := map[int64]bool{}
	for _, v := range values {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != v || seen[id] {
			return nil, fault.New(fault.Invalid, "INVALID_REQUEST", "百度ID必须为唯一规范正十进制整数")
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}
func (s *offlineSession) Query(ids []string) ([]write.Offline, error) {
	if len(ids) > 100 {
		return nil, fault.New(fault.TooLarge, "LIMIT_EXCEEDED", "离线任务查询最多100个ID")
	}
	numbers, err := parseIDs(ids)
	if err != nil {
		return nil, err
	}
	v, e := s.PCS.CloudDlQueryTask(numbers)
	if e != nil {
		return nil, upstreamFault(e)
	}
	return offlineEntries(v)
}
func (s *offlineSession) Cancel(id string) error {
	n, err := parseIDs([]string{id})
	if err != nil {
		return err
	}
	return writeFault(s.PCS.CloudDlCancelTask(n[0]))
}
func (s *offlineSession) Delete(id string) error {
	n, err := parseIDs([]string{id})
	if err != nil {
		return err
	}
	return writeFault(s.PCS.CloudDlDeleteTask(n[0]))
}
func (s *offlineSession) Clear() (uint64, error) {
	n, err := s.PCS.CloudDlClearTask()
	return uint64(max(n, 0)), writeFault(err)
}

type recycleSession struct{ *Session }

func (f *ClientFactory) OpenRecycle(ctx context.Context, cfg configuration.Snapshot, c account.Credentials) (ports.RecycleSession, error) {
	s, err := f.writeSession(ctx, cfg, c)
	if err != nil {
		return nil, err
	}
	return &recycleSession{s}, nil
}
func (s *recycleSession) List(page int) ([]write.Recycled, error) {
	v, err := s.PCS.RecycleList(page)
	if err != nil {
		return nil, upstreamFault(err)
	}
	out := []write.Recycled{}
	for _, x := range v {
		entry, err := remoteEntry(&baidupcs.FileDirectory{FsID: x.FsID, Path: x.Path, Filename: x.Filename, Isdir: x.Isdir != 0, Size: x.Size, MD5: x.MD5, Ctime: x.Ctime, Mtime: x.Mtime})
		if err != nil {
			return nil, err
		}
		out = append(out, write.Recycled{Entry: entry, LeftTime: strconv.Itoa(x.LeftTime)})
	}
	return out, nil
}
func (s *recycleSession) Restore(id string) ([]string, error) {
	n, err := parseIDs([]string{id})
	if err != nil {
		return nil, err
	}
	v, e := s.PCS.RecycleRestore(n[0])
	if e != nil {
		return nil, writeFault(e)
	}
	out := []string{}
	for _, x := range v {
		if x.FsID > 0 {
			out = append(out, strconv.FormatInt(x.FsID, 10))
		}
	}
	if len(out) == 0 {
		return nil, fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "还原响应未返回实际成功ID")
	}
	return out, nil
}
func (s *recycleSession) Delete(id string) error {
	n, err := parseIDs([]string{id})
	if err != nil {
		return err
	}
	return writeFault(s.PCS.RecycleDelete(n[0]))
}
func (s *recycleSession) Clear() (uint64, error) {
	n, err := s.PCS.RecycleClear()
	return uint64(max(n, 0)), writeFault(err)
}

func (s *fileWriter) Identity() (string, error) {
	id, err := s.PCS.CacheUK()
	if err != nil {
		return "", upstreamFault(err)
	}
	if id <= 0 {
		return "", fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "百度身份响应缺少有效UK")
	}
	return strconv.FormatInt(id, 10), nil
}
