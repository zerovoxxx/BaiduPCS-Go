package configuration_test

import (
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/configuration"
	"testing"
)

func TestSnapshotOwnsNestedValues(t *testing.T) {
	source := configuration.Client{ProxyHostnames: []string{"pan.baidu.com"}, LocalAddrs: []string{"127.0.0.1"}, SaveDir: &configuration.Directory{RootID: "root", Directory: "one"}}
	snapshot := configuration.NewSnapshot(7, source)
	source.ProxyHostnames[0] = "evil.invalid"
	source.SaveDir.Directory = "changed"
	detached := snapshot.Client()
	detached.LocalAddrs[0] = "192.0.2.1"
	detached.SaveDir.RootID = "other"
	actual := snapshot.Client()
	if snapshot.Revision() != 7 || actual.ProxyHostnames[0] != "pan.baidu.com" || actual.LocalAddrs[0] != "127.0.0.1" || actual.SaveDir.RootID != "root" || actual.SaveDir.Directory != "one" {
		t.Fatalf("snapshot aliased its input/output: %#v", actual)
	}
}
