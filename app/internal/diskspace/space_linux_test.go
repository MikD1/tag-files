package diskspace

import (
	"syscall"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
)

func TestAvailableBytesUsesAllocationFragmentsRatherThanIOTransferSize(t *testing.T) {
	// Actual virtiofs here reports 1MiB preferred IO, 4KiB allocation units.
	// Ten available allocation units are 40KiB, never 10MiB.
	for _, test := range []struct {
		name    string
		stat    syscall.Statfs_t
		want    int64
		invalid bool
	}{
		{"virtiofs", syscall.Statfs_t{Bsize: 1 << 20, Frsize: 4096, Bavail: 10}, 40960, false},
		{"ext4", syscall.Statfs_t{Bsize: 4096, Frsize: 4096, Bavail: 10}, 40960, false},
		{"old ABI", syscall.Statfs_t{Bsize: 4096, Frsize: 0, Bavail: 10}, 40960, false},
		{"empty", syscall.Statfs_t{Bsize: 4096, Frsize: 4096}, 0, false},
		{"saturation", syscall.Statfs_t{Bsize: 4096, Frsize: 4096, Bavail: ^uint64(0)}, domain.MaxSafeInteger, false},
		{"invalid unit", syscall.Statfs_t{}, 0, true},
		{"negative unit", syscall.Statfs_t{Bsize: 4096, Frsize: -1, Bavail: 10}, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := availableBytes(test.stat)
			if (err != nil) != test.invalid || got != test.want {
				t.Fatalf("got %d,%v want %d invalid=%v", got, err, test.want, test.invalid)
			}
		})
	}
}
