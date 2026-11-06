package localfs

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/domain/artifact"
	"os"
	"sort"
)

func (r *Registry) LocalRoots(ctx context.Context) ([]artifact.Root, error) {
	out := []artifact.Root{}
	for _, root := range r.roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v := artifact.Root{ID: root.ID, Label: root.ID, Readable: root.Read, Writable: root.Write, State: "ready"}
		if !root.Write {
			v.State = "read_only"
		}
		if info, err := os.Stat(root.Path); err != nil || !info.IsDir() {
			v.State = "unavailable"
			v.Readable = false
			v.Writable = false
		} else {
			v.Available = availableBytes(root.Path)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
