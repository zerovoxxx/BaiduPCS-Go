package baidupcs

import (
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/tidwall/gjson"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type (
	// ShareOption 分享可选项
	TransferOption struct {
		Download bool   // 是否直接开始下载
		Collect  bool   // 多文件整合
		Rname    bool   // 随机改文件名
		FsID     string // 指定fs_id转存
	}
)

func (pcs *BaiduPCS) GenerateShareQueryURL(subPath string, params map[string]string) *url.URL {
	shareURL := &url.URL{
		Scheme: GetHTTPScheme(true),
		Host:   PanBaiduCom,
		Path:   "/share/" + subPath,
	}
	uv := shareURL.Query()
	for key, value := range params {
		uv.Set(key, value)
	}

	shareURL.RawQuery = uv.Encode()
	return shareURL
}

func (pcs *BaiduPCS) ExtractShareInfo(shareURL, shardID, shareUK, bdstoken string) (res map[string]string) {
	res = make(map[string]string)

	parsed, err := url.Parse(shareURL)
	if err != nil {
		res["ErrMsg"] = "分享查询URL无效"
		return
	}
	ids := []string{}
	seen := map[string]bool{}
	firstName := ""
	for page := 1; ; page++ {
		q := parsed.Query()
		q.Set("page", strconv.Itoa(page))
		q.Set("num", "100")
		parsed.RawQuery = q.Encode()
		body, panError := pcs.sendReqReturnReadCloser(reqTypePan, OperationShareFileSavetoLocal, http.MethodGet, parsed.String(), nil, map[string]string{"User-Agent": NetdiskUA, "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8"})
		if panError != nil {
			res["ErrMsg"] = "提交分享项查询请求时发生错误"
			return
		}
		raw, readErr := ioutil.ReadAll(io.LimitReader(body, 32<<20))
		body.Close()
		if readErr != nil || !gjson.ValidBytes(raw) {
			res["ErrMsg"] = "分享查询响应无效"
			return
		}
		if gjson.GetBytes(raw, "errno").Int() != 0 {
			res["ErrMsg"] = "分享项查询被上游拒绝"
			return
		}
		list := gjson.GetBytes(raw, "list").Array()
		for _, entry := range list {
			id := entry.Get("fs_id").String()
			if id == "" || seen[id] || len(ids) >= 10000 {
				res["ErrMsg"] = "分享分页重复或超过10000项"
				return
			}
			seen[id] = true
			ids = append(ids, id)
			if firstName == "" {
				firstName = entry.Get("server_filename").String()
			}
		}
		if len(list) < 100 {
			break
		}
	}
	if len(ids) == 0 {
		res["ErrMsg"] = "分享文件列表为空"
		return
	}
	res["filename"] = firstName
	res["shareid"] = shardID
	res["from"] = shareUK
	res["bdstoken"] = bdstoken
	shareUrl := &url.URL{
		Scheme: GetHTTPScheme(true),
		Host:   PanBaiduCom,
		Path:   "/share/transfer",
	}
	uv := shareUrl.Query()
	uv.Set("app_id", PanAppID)
	uv.Set("channel", "chunlei")
	uv.Set("clienttype", "0")
	uv.Set("web", "1")
	for key, value := range res {
		uv.Set(key, value)
	}
	res["item_num"] = strconv.Itoa(len(ids))
	res["ErrMsg"] = "success"
	res["fs_id"] = "[" + strings.Join(ids, ",") + "]"
	shareUrl.RawQuery = uv.Encode()
	res["shareUrl"] = shareUrl.String()
	return
}

func (pcs *BaiduPCS) PostShareQuery(url string, referer string, data map[string]string) (res map[string]string) {
	dataReadCloser, panError := pcs.sendReqReturnReadCloser(reqTypePan, OperationShareFileSavetoLocal, http.MethodPost, url, data, map[string]string{
		"User-Agent":   requester.UserAgent,
		"Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
		"Referer":      referer,
	})
	res = make(map[string]string)
	if panError != nil {
		res["ErrMsg"] = "提交分享项查询请求时发生错误"
		return
	}
	defer dataReadCloser.Close()
	body, _ := ioutil.ReadAll(dataReadCloser)
	errno := gjson.Get(string(body), `errno`).Int()
	if errno != 0 {
		res["ErrMsg"] = fmt.Sprintf("未知错误, 错误码%d", errno)
		if errno == -9 {
			res["ErrMsg"] = "提取码错误"
		}
		return
	}
	res["randsk"] = gjson.Get(string(body), `randsk`).String()
	res["ErrMsg"] = "0"
	return
}

func (pcs *BaiduPCS) AccessSharePage(featurestr string, first bool) (tokens map[string]string) {
	tokens = make(map[string]string)
	tokens["ErrMsg"] = "0"
	headers := make(map[string]string)
	headers["User-Agent"] = NetdiskUA // 修 #534：Chrome 76 UA 被百度反爬弹到无 loginstate 的登录页；netdisk 客户端 UA 放行拿真实分享页
	headers["Referer"] = "https://pan.baidu.com/disk/home"
	if !first {
		headers["Referer"] = fmt.Sprintf("https://pan.baidu.com/share/init?surl=%s", featurestr[1:])
	}
	shareLink := fmt.Sprintf("https://pan.baidu.com/s/%s", featurestr)

	dataReadCloser, panError := pcs.sendReqReturnReadCloser(reqTypePan, OperationShareFileSavetoLocal, http.MethodGet, shareLink, nil, headers)

	if panError != nil {
		tokens["ErrMsg"] = "访问分享页失败"
		return
	}
	defer dataReadCloser.Close()
	body, _ := ioutil.ReadAll(dataReadCloser)
	notFoundFlag := strings.Contains(string(body), "platform-non-found")
	errorPageTitle := strings.Contains(string(body), "error-404")
	if errorPageTitle {
		tokens["ErrMsg"] = "页面不存在"
		return
	}
	if notFoundFlag {
		tokens["ErrMsg"] = "分享链接已失效"
		return
	} else {
		re, _ := regexp.Compile(`(\{.+?loginstate.+?\})`) // 修 #534：旧页 locals.mset({...}); 要求 )，新页 try{{...}}; 无 )，去 \)\; 终结符兼容两者
		sub := re.FindSubmatch(body)
		if len(sub) < 2 {
			tokens["ErrMsg"] = "请确认登录参数中已经包含了网盘STOKEN"
			return
		}
		tokens["bdstoken"] = gjson.Get(string(sub[1]), `bdstoken`).String()
		tokens["uk"] = gjson.Get(string(sub[1]), `uk`).String()
		tokens["share_uk"] = gjson.Get(string(sub[1]), `share_uk`).String()
		tokens["shareid"] = gjson.Get(string(sub[1]), `shareid`).String()
		return
	}

}

func (pcs *BaiduPCS) GenerateRequestQuery(mode string, params map[string]string) (res map[string]string) {
	res = make(map[string]string)
	res["ErrNo"] = "0"
	headers := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/76.0.3809.100 Safari/537.36",
		"Referer":    params["referer"],
	}
	if mode == "POST" {
		headers["Content-Type"] = "application/x-www-form-urlencoded"
	}
	postdata := make(map[string]string)
	postdata["fsidlist"] = params["fs_id"]
	postdata["path"] = params["path"]
	dataReadCloser, panError := pcs.sendReqReturnReadCloser(reqTypePan, OperationShareFileSavetoLocal, mode, params["shareUrl"], postdata, headers)
	if panError != nil {
		res["ErrNo"] = "1"
		res["ErrMsg"] = "网络错误"
		return
	}
	defer dataReadCloser.Close()
	body, err := ioutil.ReadAll(dataReadCloser)
	if err != nil {
		res["ErrNo"] = "-1"
		res["ErrMsg"] = "未知错误"
		return
	}
	if !gjson.Valid(string(body)) {
		res["ErrNo"] = "2"
		res["ErrMsg"] = "返回json解析错误"
		return
	}
	errno := gjson.Get(string(body), `errno`).Int()
	if errno != 0 {
		res["ErrNo"] = "3"
		res["ErrMsg"] = "获取分享项元数据错误"
		if mode == "POST" && errno == 12 {
			path := gjson.Get(string(body), `info.0.path`).String()
			_, file := filepath.Split(path) // Should be path.Split here, but never mind~
			_errno := gjson.Get(string(body), `info.0.errno`).Int()
			targetFileNums := gjson.Get(string(body), `target_file_nums`).Int()
			targetFileNumsLimit := gjson.Get(string(body), `target_file_nums_limit`).Int()
			if targetFileNums > targetFileNumsLimit {
				res["ErrNo"] = "4"
				res["ErrMsg"] = fmt.Sprintf("转存文件数%d超过当前用户上限, 当前用户单次最大转存数%d", targetFileNums, targetFileNumsLimit)
				res["limit"] = fmt.Sprintf("%d", targetFileNumsLimit)
			} else if _errno == -30 {
				res["ErrNo"] = "9"
				res["ErrMsg"] = fmt.Sprintf("当前目录下已有%s同名文件/文件夹", file)
			} else {
				res["ErrMsg"] = fmt.Sprintf("未知错误, 错误代码%d", _errno)
			}
		} else if mode == "POST" && errno == 4 {
			res["ErrMsg"] = fmt.Sprintf("文件重复")
		}
		return
	}

	_, res["filename"] = filepath.Split(gjson.Get(string(body), `info.0.path`).String())
	filenames := gjson.Get(string(body), `info.#.path`).Array()
	filenamesStr := ""
	for _, _path := range filenames {
		filenamesStr += "," + path.Base(_path.String())
	}
	if len(filenamesStr) < 1 {
		res["filenames"] = "default" + pcsutil.GenerateRandomString(5)
	} else {
		res["filenames"] = filenamesStr[1:]
	}
	if len(gjson.Get(string(body), `info.#.fsid`).Array()) > 1 {
		res["filename"] += "等多个文件/文件夹"
	}
	return
}

func (pcs *BaiduPCS) SuperTransfer(params map[string]string, limit string) {
	//headers := map[string]string{
	//	"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/76.0.3809.100 Safari/537.36",
	//	"Referer":    params["referer"],
	//}
	//limit_num, _ := strconv.Atoi(limit)
	//fsidlist_str := params["fs_id"]
	//fsidlist := strings.Split(fsidlist_str[1:len(fsidlist_str)-1], ",")
	//listUrl := &url.URL{
	//	Scheme: GetHTTPScheme(true),
	//	Host:   PanBaiduCom,
	//	Path:   "/share/list",
	//}
	//uv := listUrl.Query()
	//uv.Set("app_id", PanAppID)
	//uv.Set("channel", "chunlei")
	//uv.Set("clienttype", "0")
	//uv.Set("web", "1")
	//uv.Set("page", "1")
	//uv.Set("num", "100")
	//uv.Set("shorturl", params["shorturl"])
	//uv.Set("root", "1")
	//dataReadCloser, panError := pcs.sendReqReturnReadCloser(reqTypePan, OperationShareFileSavetoLocal, http.MethodGet, listUrl.String(), nil, headers)
	//if panError != nil {
	//	res["ErrNo"] = "1"
	//	res["ErrMsg"] = "网络错误"
	//	return
	//}
	//defer dataReadCloser.Close()
	//body, err := ioutil.ReadAll(dataReadCloser)
	//res["ErrNo"] = "-1"
	//if err != nil {
	//	res["ErrMsg"] = "未知错误"
	//	return
	//}
	return

}
