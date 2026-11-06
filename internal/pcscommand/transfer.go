package pcscommand

import (
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"path"
	"strconv"
	"strings"
	"time"
)

// RunShareTransfer 执行分享链接转存到网盘
func RunShareTransfer(params []string, opt *baidupcs.TransferOption) {
	if len(params) == 0 {
		fmt.Println("分享输入不能为空")
		return
	}
	if opt == nil {
		opt = &baidupcs.TransferOption{}
	}
	if opt.Rname {
		fmt.Println("Rname/randomize_name尚无有效实现")
		return
	}
	password := ""
	if len(params) > 1 {
		password = params[1]
	}
	featureStr, extraCode, err := baidupcs.ParseShareInput(params[0], password)
	if err != nil {
		fmt.Printf("%s失败: %s\n", baidupcs.OperationShareFileSavetoLocal, err)
		return
	}
	link := "https://pan.baidu.com/s/" + featureStr
	pcs := GetBaiduPCS()
	tokens := pcs.AccessSharePage(featureStr, true)
	if tokens["ErrMsg"] != "0" {
		fmt.Printf("%s失败: %s\n", baidupcs.OperationShareFileSavetoLocal, tokens["ErrMsg"])
		return
	}

	verifyUrl := pcs.GenerateShareQueryURL("verify", map[string]string{
		"shareid":    tokens["shareid"],
		"time":       strconv.Itoa(int(time.Now().UnixMilli())),
		"clienttype": "1",
		"uk":         tokens["share_uk"],
	}).String()
	res := pcs.PostShareQuery(verifyUrl, link, map[string]string{
		"pwd":       extraCode,
		"vcode":     "null",
		"vcode_str": "null",
		"bdstoken":  tokens["bdstoken"],
	})
	if res["ErrMsg"] != "0" {
		fmt.Printf("%s失败: %s\n", baidupcs.OperationShareFileSavetoLocal, res["ErrMsg"])
		return
	}

	pcs.UpdatePCSCookies(true)

	tokens = pcs.AccessSharePage(featureStr, false)
	if tokens["ErrMsg"] != "0" {
		fmt.Printf("%s失败: %s\n", baidupcs.OperationShareFileSavetoLocal, tokens["ErrMsg"])
		return
	}
	featureMap := map[string]string{
		"bdstoken": tokens["bdstoken"],
		"root":     "1",
		"web":      "5",
		"app_id":   baidupcs.PanAppID,
		"shorturl": featureStr[1:],
		"channel":  "chunlei",
	}
	queryShareInfoUrl := pcs.GenerateShareQueryURL("list", featureMap).String()
	transMetas := pcs.ExtractShareInfo(queryShareInfoUrl, tokens["shareid"], tokens["share_uk"], tokens["bdstoken"])

	if transMetas["ErrMsg"] != "success" {
		fmt.Printf("%s失败: %s\n", baidupcs.OperationShareFileSavetoLocal, transMetas["ErrMsg"])
		return
	}

	// 如果指定了 fs_id, 覆盖解析结果
	if opt.FsID != "" {
		transMetas["fs_id"] = "[" + opt.FsID + "]"
		transMetas["filename"] = "user_specified_file" // 名字可能不准确，但不影响转存
		transMetas["item_num"] = "1"
	}

	transMetas["path"] = GetActiveUser().Workdir
	if transMetas["item_num"] != "1" && opt.Collect {
		transMetas["filename"] += "等文件"
		transMetas["path"] = path.Join(GetActiveUser().Workdir, transMetas["filename"])
		pcs.Mkdir(transMetas["path"])
	}
	transMetas["referer"] = "https://pan.baidu.com/s/" + featureStr
	pcs.UpdatePCSCookies(true)
	resp := pcs.GenerateRequestQuery("POST", transMetas)
	if resp["ErrNo"] != "0" {
		fmt.Printf("%s失败: %s\n", baidupcs.OperationShareFileSavetoLocal, resp["ErrMsg"])
		//if resp["ErrNo"] == "4" {
		//	transMetas["shorturl"] = featureStr
		//	pcs.SuperTransfer(transMetas, resp["limit"]) // 试验性功能, 当前未启用
		//}
		return
	}
	if opt.Collect {
		resp["filename"] = transMetas["filename"]
	}
	fmt.Printf("%s成功, 保存了%s到当前目录\n", baidupcs.OperationShareFileSavetoLocal, resp["filename"])
	if opt.Download {
		fmt.Println("10s后开始下载")
		time.Sleep(10 * time.Second)
		paths := strings.Split(resp["filenames"], ",")
		RunDownload(paths, nil)
	}
}
