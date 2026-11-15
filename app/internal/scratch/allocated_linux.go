package scratch

import (
	"io/fs"
	"syscall"
)

func allocatedBytes(info fs.FileInfo) int64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Blocks <= 0 {
		return 0
	}
	// Linux st_blocks uses 512-byte units. Bound the multiplication by the
	// logical size: preallocation is not additional progress through the job.
	if stat.Blocks > info.Size()/512 {
		return info.Size()
	}
	return stat.Blocks * 512
}
