package storageprofile

import "github.com/mikd1/tag-files/app/internal/diskspace"

// VolumeIdentity distinguishes copied/restored roots even when their logical
// storage/library UUIDs match. It is not copied as a portable volume identity.
// Ext4 statfs fsid derives from the filesystem UUID; the root inode survives
// ordinary container restarts. Other filesystem/VM lifecycle gates remain.
func VolumeIdentity(root string) (string, error) {
	snapshot, err := diskspace.Measure(root)
	return snapshot.RootIdentity, err
}
