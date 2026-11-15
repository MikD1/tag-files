//go:build !linux

package scratch

import "io/fs"

func allocatedBytes(fs.FileInfo) int64 { return 0 }
