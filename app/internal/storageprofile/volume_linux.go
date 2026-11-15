package storageprofile

import (
	"fmt"
	"syscall"
)

// VolumeIdentity distinguishes copied/restored roots even when their logical
// storage/library UUIDs match. It is not copied as a portable volume identity.
// Ext4 statfs fsid derives from the filesystem UUID; the root inode survives
// ordinary container restarts. Other filesystem/VM lifecycle gates remain.
func VolumeIdentity(root string) (string, error) {
	var stat syscall.Stat_t
	var filesystem syscall.Statfs_t
	if err := syscall.Stat(root, &stat); err != nil {
		return "", err
	}
	if err := syscall.Statfs(root, &filesystem); err != nil {
		return "", err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		return "", ErrStorageVolumeChanged
	}
	return digest([]byte(fmt.Sprintf("tag-files/volume-root/v1/%x/%08x/%08x/%x", filesystem.Type, uint32(filesystem.Fsid.X__val[0]), uint32(filesystem.Fsid.X__val[1]), stat.Ino))), nil
}
