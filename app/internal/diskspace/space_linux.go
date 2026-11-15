package diskspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"syscall"

	"github.com/mikd1/tag-files/app/internal/domain"
)

func Measure(root string) (Snapshot, error) {
	file, err := os.OpenFile(root, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return Snapshot{}, err
	}
	defer file.Close()
	var fs syscall.Statfs_t
	var stat syscall.Stat_t
	if err := syscall.Fstat(int(file.Fd()), &stat); err != nil {
		return Snapshot{}, err
	}
	if err := syscall.Fstatfs(int(file.Fd()), &fs); err != nil {
		return Snapshot{}, err
	}
	if fs.Bsize <= 0 {
		return Snapshot{}, ErrUnavailable
	}
	// Round down by saturation: a huge filesystem must never wrap its available
	// bytes or publish an integer that the JSON client cannot represent exactly.
	available := int64(domain.MaxSafeInteger)
	if fs.Bavail <= uint64(domain.MaxSafeInteger)/uint64(fs.Bsize) {
		available = int64(fs.Bavail) * fs.Bsize
	}
	id := sha256.Sum256(fmt.Appendf(nil, "tag-files/filesystem/v1/%x/%08x/%08x", fs.Type, uint32(fs.Fsid.X__val[0]), uint32(fs.Fsid.X__val[1])))
	rootID := sha256.Sum256(fmt.Appendf(nil, "tag-files/volume-root/v1/%x/%08x/%08x/%x", fs.Type, uint32(fs.Fsid.X__val[0]), uint32(fs.Fsid.X__val[1]), stat.Ino))
	s := Snapshot{FileSystemID: hex.EncodeToString(id[:]), AvailableBytes: available, Independent: fs.Type == 0xef53, RootIdentity: hex.EncodeToString(rootID[:])}
	if fs.Files != 0 {
		count := int64(min(fs.Ffree, uint64(domain.MaxSafeInteger)))
		s.AvailableInodes = &count
	}
	return s, nil
}
