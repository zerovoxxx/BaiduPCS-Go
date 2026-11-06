package baidu

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/fault"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/transfer"
)

type downloadSettingsValue struct {
	Mode        string `json:"mode"`
	PathMode    string `json:"path_mode"`
	Parallel    int    `json:"parallel"`
	Concurrency int    `json:"file_concurrency"`
	Overwrite   bool   `json:"overwrite"`
	Verify      bool   `json:"verify_checksum"`
	Mtime       bool   `json:"preserve_mtime"`
	Executable  bool   `json:"executable"`
	Index       int    `json:"link_index"`
	Test        bool   `json:"test"`
	Report      bool   `json:"report_workers"`
}

func downloadSettings(r transfer.Record) downloadSettingsValue {
	o := downloadSettingsValue{Mode: "pcs", PathMode: "relative", Parallel: 1, Concurrency: 1, Verify: true}
	_ = json.Unmarshal(r.EffectiveOptions, &o)
	return o
}
func downloadURL(s *Session, p, mode string, index int) (string, error) {
	var address string
	cb := func(u string, _ http.CookieJar) error { address = u; return nil }
	switch mode {
	case "pcs":
		if err := s.PCS.DownloadFile(p, cb); err != nil {
			return "", err
		}
	case "stream":
		if err := s.PCS.DownloadStreamFile(p, cb); err != nil {
			return "", err
		}
	case "locate":
		links, err := s.PCS.LocateDownload(p)
		if err != nil {
			return "", err
		}
		urls := links.URLStrings(true)
		if index >= len(urls) {
			return "", fault.New(fault.Unsupported, "DOWNLOAD_LINK_INDEX_UNAVAILABLE", "指定下载链接序号不可用")
		}
		address = urls[index].String()
	default:
		return "", unsupported("UNSUPPORTED_OPTION", "未知下载模式")
	}
	u, err := url.Parse(address)
	if err != nil || !allowedURL(u) {
		return "", fault.New(fault.BadGateway, "UPSTREAM_PROTOCOL_ERROR", "上游下载地址不可用")
	}
	return address, nil
}
