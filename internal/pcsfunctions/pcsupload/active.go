package pcsupload

import (
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
	"sync"
)

// UploadGroup scopes graceful stopping to one invocation, including uploaders
// registered after stop. A later command always gets a fresh group.
type UploadGroup struct {
	mu      sync.Mutex
	stopped bool
	active  map[*uploader.MultiUploader]bool
}

func (g *UploadGroup) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	for u := range g.active {
		u.GracefulStop()
	}
}
func (g *UploadGroup) Register(u *uploader.MultiUploader) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active == nil {
		g.active = map[*uploader.MultiUploader]bool{}
	}
	g.active[u] = true
	if g.stopped {
		u.GracefulStop()
	}
}
func (g *UploadGroup) Unregister(u *uploader.MultiUploader) {
	g.mu.Lock()
	delete(g.active, u)
	g.mu.Unlock()
}

// Deprecated compatibility helpers. Executable TaskUnits use an injected group.
var legacyUploadGroup UploadGroup

func GracefulStopActiveUploaders()                       { legacyUploadGroup.Stop() }
func RegisterActiveUploader(u *uploader.MultiUploader)   { legacyUploadGroup.Register(u) }
func UnregisterActiveUploader(u *uploader.MultiUploader) { legacyUploadGroup.Unregister(u) }
