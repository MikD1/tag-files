//go:build !linux

package diskspace

func Measure(string) (Snapshot, error) { return Snapshot{}, ErrUnavailable }
