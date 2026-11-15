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
	available, err := availableBytes(fs)
	if err != nil {
		return Snapshot{}, err
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

func availableBytes(fs syscall.Statfs_t) (int64, error) {
	// f_bsize is preferred IO size. Block counts use f_frsize allocation units;
	// they differ on virtiofs. Older Linux ABIs can report a zero fragment size.
	unit := fs.Frsize
	if unit == 0 {
		unit = fs.Bsize
	}
	if unit <= 0 {
		return 0, ErrUnavailable
	}
	// Saturate before multiplication; the API cannot represent larger integers.
	if fs.Bavail > uint64(domain.MaxSafeInteger)/uint64(unit) {
		return domain.MaxSafeInteger, nil
	}
	return int64(fs.Bavail) * unit, nil
}
