package scratch

import (
	"math"
	"syscall"
)

func available(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	if stat.Bsize < 1 || stat.Files > 0 && stat.Ffree < 4 {
		return 0, nil
	}
	if stat.Bavail > uint64(math.MaxInt64)/uint64(stat.Bsize) {
		return math.MaxInt64, nil
	}
	return int64(stat.Bavail) * stat.Bsize, nil
}
