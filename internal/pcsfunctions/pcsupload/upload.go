package pcsupload

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/multipartreader"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
	"io"
	"net/http"
)

type (
	PCSUpload struct {
		pcs        *baidupcs.BaiduPCS
		targetPath string
		client     *requester.HTTPClient
	}

	EmptyReaderLen64 struct {
	}
)

var pcsPeriod = 256 // 上传多少个分片更换一次pcsHost

func (e EmptyReaderLen64) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

func (e EmptyReaderLen64) Len() int64 {
	return 0
}

func NewPCSUpload(pcs *baidupcs.BaiduPCS, targetPath string, clients ...*requester.HTTPClient) uploader.MultiUpload {
	client := pcs.GetClient()
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	return &PCSUpload{client: client,
		pcs:        pcs,
		targetPath: targetPath,
	}
}

func (pu *PCSUpload) lazyInit() {
	if pu.pcs == nil {
		pu.pcs = &baidupcs.BaiduPCS{}
	}
}

// Precreate 上传前准备, 获取本次上传用的pcs服务器
func (pu *PCSUpload) Precreate() (originPCSHost string, pcsError pcserror.Error) {
	originPCSHost = pu.pcs.GetPCSAddr()
	_, newPCSHost := pu.pcs.GetRandomPCSHost()
	pu.pcs.SetPCSAddr(newPCSHost)
	return
}

func (pu *PCSUpload) TmpFile(ctx context.Context, uploadId, targetPath string, partSeq int, partOffset int64, r rio.ReaderLen64) (checksum string, uperr error) {
	pu.lazyInit()

	var respErr *uploader.MultiError

	checksum, pcsError := pu.pcs.UploadTmpFile(uploadId, targetPath, partSeq, partOffset, func(uploadURL string, jar http.CookieJar) (resp *http.Response, err error) {

		mr := multipartreader.NewMultipartReader()
		mr.AddFormFile("uploadedfile", "", r)
		mr.CloseMultipart()

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, mr)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", mr.ContentType())
		req.ContentLength = mr.Len()
		resp, err = pu.client.Client.Do(req)
		if resp != nil {
			switch resp.StatusCode {
			case 400, 401, 403, 413:
				respErr = &uploader.MultiError{Terminated: true}
			}
		}

		return
	})

	if respErr != nil {
		respErr.Err = pcsError
		return checksum, respErr
	}

	return checksum, pcsError
}

func (pu *PCSUpload) CreateSuperFile(pcsHost, policy, uploadId string, fileSize int64, checksumMap map[int]string) (err error) {
	pu.lazyInit()
	pu.pcs.SetPCSAddr(pcsHost) // 恢复默认pcs服务器
	return pu.pcs.UploadCreateSuperFile(uploadId, policy, fileSize, pu.targetPath, checksumMap)
}
