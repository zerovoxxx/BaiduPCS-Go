package httpapi

import "testing"

func TestP3ArtifactSingleRangeBoundaries(t *testing.T) {
	for _, c := range []struct {
		Value             string
		Size, First, Last int64
		Status            int
	}{
		{"bytes=0-3", 17, 0, 3, 0}, {"bytes=4-", 17, 4, 16, 0}, {"bytes=-4", 17, 13, 16, 0}, {"bytes=-99", 17, 0, 16, 0}, {"bytes=0-999", 17, 0, 16, 0},
		{"bytes=17-", 17, 0, 0, 416}, {"bytes=-0", 17, 0, 0, 416}, {"bytes=0-", 0, 0, 0, 416},
		{"bytes=0-1,3-4", 17, 0, 0, 400}, {"bytes=a-b", 17, 0, 0, 400}, {"bytes=2-1", 17, 0, 0, 400}, {"items=0-3", 17, 0, 0, 400}, {"bytes=+1-2", 17, 0, 0, 400},
	} {
		first, last, err := artifactRange(c.Value, c.Size)
		if c.Status == 0 {
			if err != nil || first != c.First || last != c.Last {
				t.Fatalf("%s: %d %d %v", c.Value, first, last, err)
			}
		} else if e, ok := err.(*httpError); !ok || e.status != c.Status {
			t.Fatalf("%s expected %d: %v", c.Value, c.Status, err)
		}
	}
}
