package pcsupload

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
	"os"
	"testing"
)

type groupProvider struct{}

func (groupProvider) Precreate() (string, pcserror.Error) { return "", nil }
func (groupProvider) TmpFile(context.Context, string, string, int, int64, rio.ReaderLen64) (string, error) {
	return "checked", nil
}
func (groupProvider) CreateSuperFile(string, string, string, int64, map[int]string) error { return nil }
func TestP4CLIUploadGroupsDoNotStopOtherOrLaterInvocations(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	file.Write([]byte("x"))
	a, b := &UploadGroup{}, &UploadGroup{}
	a.Stop()
	execute := func(group *UploadGroup) bool {
		engine := uploader.NewMultiUploader(groupProvider{}, rio.NewFileReaderAtLen64(file), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "target")
		engine.SetInstanceState(&uploader.InstanceState{Uploadid: "test"})
		group.Register(engine)
		defer group.Unregister(engine)
		success := false
		engine.OnSuccess(func() { success = true })
		engine.Execute()
		return success
	}
	if execute(a) {
		t.Fatal("stopped invocation ran")
	}
	if !execute(b) {
		t.Fatal("A stopped B")
	}
	if !execute(&UploadGroup{}) {
		t.Fatal("A poisoned later C")
	}
}
