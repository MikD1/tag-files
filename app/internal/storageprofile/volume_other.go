//go:build !linux

package storageprofile

import "errors"

func VolumeIdentity(string) (string, error) {
	return "", errors.New("storage volume identity requires the Linux runtime")
}
