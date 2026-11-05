package baidu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/account"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/file"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/write"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/ports"
)

type shareSession struct{ *Session }

func (f *ClientFactory) OpenShares(ctx context.Context, cfg configuration.Snapshot, c account.Credentials) (ports.ShareSession, error) {
	s, err := f.writeSession(ctx, cfg, c)
	if err != nil {
		return nil, err
	}
	return &shareSession{s}, nil
}
func (s *shareSession) Create(paths []string, password string, period int, combined bool) (write.Share, error) {
	v, err := s.PCS.ShareSet(paths, &baidupcs.ShareOption{Password: password, Period: period, IsCombined: combined})
	if err != nil {
		f := writeFault(err)
		if fault.As(f).Kind == fault.BadGateway {
			f = fault.New(fault.Unsupported, "SHARE_CREATE_REJECTED", "百度拒绝分享参数或操作")
		}
		return write.Share{}, f
	}
	if v.ShareID <= 0 {
		return write.Share{}, fault.New(fault.Conflict, "REMOTE_RESULT_UNKNOWN", "分享响应缺少真实share_id")
	}
	return write.Share{ID: strconv.FormatInt(v.ShareID, 10), Link: v.Link, Password: v.Pwd, Combined: combined}, nil
}
func (s *shareSession) List(page int, passwords bool) ([]write.Share, error) {
	values, err := s.PCS.ShareList(page)
	if err != nil {
		return nil, upstreamFault(err)
	}
	out := []write.Share{}
	for _, v := range values {
		if v.ShareID <= 0 || v.Status < 0 {
			return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "分享列表字段无效")
		}
		ids := []string{}
		for _, id := range v.FsIds {
			ids = append(ids, strconv.FormatInt(id, 10))
		}
		entry := write.Share{ID: strconv.FormatInt(v.ShareID, 10), Link: v.Shortlink, Fields: map[string]any{"fs_ids": ids, "status": v.Status, "public": v.Public != 0, "typical_category": v.TypicalCategory, "typical_path": optionalPath(v.TypicalPath), "expire_type": v.ExpireType, "expires_at": shareExpiry(v.ExpireTime), "view_count": strconv.Itoa(v.ViewCount), "valid": v.ExpireType != -1}}
		if passwords {
			detail, e := s.Detail(entry.ID)
			if e != nil {
				return nil, e
			}
			entry.Password = detail.Password
			entry.Link = detail.Link
		}
		out = append(out, entry)
	}
	return out, nil
}
func optionalPath(p string) any {
	if path.IsAbs(p) {
		return p
	}
	return nil
}
func upstreamTime(t int64) any {
	if t <= 0 || t > 253402300799 {
		return nil
	}
	return time.Unix(t, 0).UTC().Format(time.RFC3339)
}
func (s *shareSession) Detail(id string) (write.Share, error) {
	n, err := parseIDs([]string{id})
	if err != nil {
		return write.Share{}, err
	}
	v, e := s.PCS.ShareSURLInfo(n[0])
	if e != nil {
		return write.Share{}, upstreamFault(e)
	}
	if v == nil {
		return write.Share{}, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "分享详情缺失")
	}
	link := v.ShortURL
	if !strings.HasPrefix(link, "https://") {
		link = "https://pan.baidu.com/s/" + strings.TrimPrefix(link, "/")
	}
	return write.Share{ID: id, Link: link, Password: v.Pwd}, nil
}
func (s *shareSession) Cancel(id string) error {
	n, err := parseIDs([]string{id})
	if err != nil {
		return err
	}
	return writeFault(s.PCS.ShareCancel(n))
}

func (s *shareSession) Inspect(input, password string) (write.Access, error) {
	randsk := ""
	feature, password, err := baidupcs.ParseShareInput(input, password)
	if err != nil {
		return write.Access{}, fault.New(fault.Invalid, "INVALID_SHARE_INPUT", "分享链接或提取码无效")
	}
	tokens := s.PCS.AccessSharePage(feature, true)
	if tokens["ErrMsg"] != "0" {
		return write.Access{}, fault.New(fault.Unsupported, "SHARE_ACCESS_UNAVAILABLE", "分享页不可访问或缺少有效STOKEN")
	}
	if password != "" {
		u := s.PCS.GenerateShareQueryURL("verify", map[string]string{"shareid": tokens["shareid"], "uk": tokens["share_uk"], "clienttype": "1", "time": strconv.FormatInt(time.Now().UnixMilli(), 10)}).String()
		result := s.PCS.PostShareQuery(u, "https://pan.baidu.com/s/"+feature, map[string]string{"pwd": password, "vcode": "null", "vcode_str": "null", "bdstoken": tokens["bdstoken"]})
		if result["ErrMsg"] != "0" {
			return write.Access{}, fault.New(fault.Unsupported, "SHARE_PASSWORD_REJECTED", "分享提取码被上游拒绝")
		}
		randsk = result["randsk"]
		if result["randsk"] != "" {
			origin, _ := url.Parse("https://pan.baidu.com/")
			s.HTTP.Jar.SetCookies(origin, []*http.Cookie{{Name: "BDCLND", Value: result["randsk"], Path: "/", Secure: true}})
		}
		tokens = s.PCS.AccessSharePage(feature, false)
		if tokens["ErrMsg"] != "0" {
			return write.Access{}, fault.New(fault.Unsupported, "SHARE_ACCESS_UNAVAILABLE", "分享验证后无法读取页面")
		}
	}
	if _, err = parseIDs([]string{tokens["shareid"]}); err != nil {
		return write.Access{}, fault.New(fault.BadGateway, "SHARE_PAGE_ID_MISSING", "分享页面缺少有效身份信息")
	}
	if _, err = parseIDs([]string{tokens["share_uk"]}); err != nil {
		return write.Access{}, fault.New(fault.BadGateway, "SHARE_PAGE_OWNER_MISSING", "分享页面缺少有效owner UK")
	}
	access := write.Access{Feature: feature, ShareID: tokens["shareid"], OwnerUK: tokens["share_uk"], Token: tokens["bdstoken"], Password: password, Randsk: randsk}
	list, err := s.SharedDirectory(access, "")
	if err != nil {
		return access, err
	}
	access.Entries = list
	for _, entry := range list {
		access.RootIDs = append(access.RootIDs, entry.ID)
	}
	return access, nil
}
func (s *shareSession) SharedDirectory(a write.Access, dir string) ([]file.Entry, error) {
	s.installShareCookie(a)
	out := []file.Entry{}
	seen := map[string]bool{}
	for page := 1; ; page++ {
		params := map[string]string{"app_id": baidupcs.PanAppID, "web": "5", "channel": "chunlei", "shorturl": strings.TrimPrefix(a.Feature, "1"), "bdstoken": a.Token, "page": strconv.Itoa(page), "num": "100", "root": "1"}
		if dir != "" {
			params["root"] = "0"
			params["dir"] = dir
		}
		u := s.PCS.GenerateShareQueryURL("list", params).String()
		response, err := s.HTTP.Req("GET", u, nil, map[string]string{"User-Agent": baidupcs.NetdiskUA, "Referer": "https://pan.baidu.com/s/" + a.Feature})
		if err != nil {
			return nil, upstreamFault(err)
		}
		var result struct {
			*pcserror.PanErrorInfo
			List []struct {
				ID    upstreamInteger `json:"fs_id"`
				Path  string          `json:"path"`
				Name  string          `json:"server_filename"`
				Size  upstreamInteger `json:"size"`
				Dir   upstreamInteger `json:"isdir"`
				MD5   string          `json:"md5"`
				Ctime upstreamInteger `json:"server_ctime"`
				Mtime upstreamInteger `json:"server_mtime"`
			}
		}
		result.PanErrorInfo = pcserror.NewPanErrorInfo("share/list")
		var e pcserror.Error
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 32<<20)).Decode(&result)
		response.Body.Close()
		if decodeErr != nil {
			result.SetJSONError(decodeErr)
			e = result.PanErrorInfo
		} else if result.GetRemoteErrCode() != 0 {
			result.SetRemoteError()
			e = result.PanErrorInfo
		}
		if e != nil {
			var typed *json.UnmarshalTypeError
			if errors.As(e.GetError(), &typed) {
				field := strings.ToUpper(strings.ReplaceAll(typed.Field, ".", "_"))
				switch field {
				case "LIST_SIZE", "LIST_ISDIR", "LIST_FS_ID", "LIST_SERVER_CTIME", "LIST_SERVER_MTIME", "ERRNO":
					return nil, fault.New(fault.BadGateway, "SHARE_RESPONSE_TYPE_"+field, "分享列表字段类型与协议不符")
				}
			}
			var syntax *json.SyntaxError
			if errors.As(e.GetError(), &syntax) {
				return nil, fault.New(fault.BadGateway, "SHARE_RESPONSE_NOT_JSON", "分享列表没有返回有效JSON")
			}
			return nil, upstreamFault(e)
		}
		for _, v := range result.List {
			entry, err := remoteEntry(&baidupcs.FileDirectory{FsID: int64(v.ID), Path: v.Path, Filename: v.Name, Size: int64(v.Size), Isdir: v.Dir != 0, MD5: v.MD5, Ctime: int64(v.Ctime), Mtime: int64(v.Mtime)})
			if err != nil {
				return nil, err
			}
			if entry.ID == "0" || entry.Name != path.Base(entry.Path) || strings.ContainsAny(entry.Path+entry.Name, "\x00\r\n") || entry.Name == "." || entry.Name == ".." || entry.Name == "/" || dir != "" && path.Dir(entry.Path) != dir {
				return nil, fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "分享项目路径不属于已检查目录")
			}
			if seen[entry.ID] {
				return nil, fault.New(fault.BadGateway, "UPSTREAM_PAGINATION_STALLED", "分享检查分页重复")
			}
			seen[entry.ID] = true
			out = append(out, entry)
			if len(out) > 10000 {
				return nil, fault.New(fault.TooLarge, "INSPECTION_LIMIT", "分享检查最多10000项")
			}
		}
		if len(result.List) < 100 {
			break
		}
	}
	return out, nil
}
func (s *shareSession) Transfer(a write.Access, ids []string, target string) (write.Outcome, error) {
	s.installShareCookie(a)
	numbers, err := parseIDs(ids)
	if err != nil {
		return write.Outcome{}, err
	}
	encoded, _ := json.Marshal(numbers)
	u := s.PCS.GenerateShareQueryURL("transfer", map[string]string{"shareid": a.ShareID, "from": a.OwnerUK, "bdstoken": a.Token, "app_id": baidupcs.PanAppID, "channel": "chunlei", "clienttype": "0", "web": "1"}).String()
	resp, err := s.HTTP.Req("POST", u, strings.NewReader(url.Values{"fsidlist": {string(encoded)}, "path": {target}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Referer": "https://pan.baidu.com/s/" + a.Feature, "User-Agent": baidupcs.NetdiskUA})
	if err != nil {
		return write.Outcome{}, writeFault(err)
	}
	defer resp.Body.Close()
	var result struct {
		*pcserror.PanErrorInfo
		Info []struct {
			ID    upstreamInteger `json:"fsid"`
			Path  string          `json:"path"`
			Errno int             `json:"errno"`
		}
	}
	result.PanErrorInfo = pcserror.NewPanErrorInfo("share/transfer")
	if err := pcserror.HandleJSONParse("share/transfer", io.LimitReader(resp.Body, 32<<20), &result); err != nil {
		failure := writeFault(err)
		if err.GetErrType() == pcserror.ErrTypeRemoteError && fault.As(failure).Kind == fault.BadGateway {
			failure = fault.New(fault.Conflict, "SHARE_TRANSFER_REJECTED", "百度明确拒绝转存请求")
		}
		return write.Outcome{}, failure
	}
	out := write.Outcome{IDs: []string{}, Paths: []string{}}
	for _, v := range result.Info {
		if v.Errno != 0 {
			return out, fault.New(fault.Conflict, "SHARE_TRANSFER_REJECTED", "分享转存项目被上游拒绝")
		}
		if v.ID > 0 {
			out.IDs = append(out.IDs, strconv.FormatInt(int64(v.ID), 10))
		}
		if path.IsAbs(v.Path) && under(target, v.Path) {
			out.Paths = append(out.Paths, v.Path)
		}
	}
	out.Affected = uint64(len(ids))
	return out, nil
}

func (s *shareSession) installShareCookie(a write.Access) {
	if a.Randsk != "" {
		origin, _ := url.Parse("https://pan.baidu.com/")
		s.HTTP.Jar.SetCookies(origin, []*http.Cookie{{Name: "BDCLND", Value: a.Randsk, Path: "/", Secure: true}})
	}
}

func shareExpiry(seconds int64) any {
	if seconds <= 0 {
		return nil
	}
	if seconds > 253402300799-time.Now().Unix() {
		return nil
	}
	return upstreamTime(time.Now().Unix() + seconds)
}
