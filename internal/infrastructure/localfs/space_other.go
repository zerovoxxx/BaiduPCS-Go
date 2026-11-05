//go:build !linux && !darwin

package localfs

func availableBytes(string) *uint64 { return nil }
