//go:build !linux

package scratch

import "errors"

func available(string) (int64, error) {
	return 0, errors.New("scratch capacity requires the Linux application runtime")
}
