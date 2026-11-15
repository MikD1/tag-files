package capacity

import (
	"errors"
	"strings"
	"testing"

	"github.com/mikd1/tag-files/app/internal/diskspace"
	"github.com/mikd1/tag-files/app/internal/scratch"
)

func space(id string, free int64, independent bool) diskspace.Snapshot {
	inodes := int64(10000)
	return diskspace.Snapshot{FileSystemID: strings.Repeat(id, 64), AvailableBytes: free, AvailableInodes: &inodes, Independent: independent}
}

func TestSharedFilesystemCannotSpendItsFreeBytesTwice(t *testing.T) {
	p := plan{Incoming: 50, Working: 20, Scratch: 40}
	s := scratch.Usage{QuotaBytes: 1000, Space: space("a", 100, true)}
	if err := evaluate(p, s, space("a", 100, true), space("a", 100, true), 1, 4); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatal("each separate check passed but shared peak111 exceeded free100", err)
	}
	s.Space.AvailableBytes = 111
	if err := evaluate(p, s, space("a", 111, true), space("a", 111, true), 1, 4); err != nil {
		t.Fatal("exact shared peak should fit", err)
	}
}

func TestSeparateFilesystemBudgetsAndUnknownAliasing(t *testing.T) {
	p := plan{Incoming: 50, Working: 20, Scratch: 40}
	s := scratch.Usage{QuotaBytes: 1000, Space: space("b", 41, true)}
	if err := evaluate(p, s, space("c", 1, true), space("a", 71, true), 1, 4); err != nil {
		t.Fatal("separate filesystem capacities were added unnecessarily", err)
	}
	s.Space.Independent = false
	if err := evaluate(p, s, space("c", 1000, true), space("a", 1000, true), 1, 4); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatal("unknown/overlay backing was assumed independent", err)
	}
}

func TestScratchWrittenBytesAreAlreadyInPhysicalFreeButRetainedFilesUseQuota(t *testing.T) {
	s := scratch.Usage{UsedBytes: 35, ActiveBytes: 30, AllocatedActiveBytes: 30, ReservedBytes: 10, QuotaBytes: 45, Space: space("a", 11, true)}
	if err := evaluate(plan{Scratch: 40}, s, space("a", 11, true), space("a", 11, true), 1, 4); err != nil {
		t.Fatal("written active bytes were charged twice", err)
	}
	s.QuotaBytes = 44
	if err := evaluate(plan{Scratch: 40}, s, space("a", 1000, true), space("a", 1000, true), 1, 4); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatal("retained unknown cache was ignored", err)
	}
}

func TestUnknownInodesAndArithmeticOverflowNeverAdmitWork(t *testing.T) {
	s := scratch.Usage{QuotaBytes: 1000, Space: space("a", 1000, true)}
	s.Space.AvailableInodes = nil
	if err := evaluate(plan{Incoming: 1}, s, space("a", 1000, true), space("a", 1000, true), 1, 4); err == nil {
		t.Fatal("unknown inodes treated as enough")
	}
	s.Space = space("a", 1000, true)
	if err := evaluate(plan{Incoming: 9223372036854775807, Working: 1}, s, space("a", 1000, true), space("a", 1000, true), 1, 4); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatal("overflow admitted work", err)
	}
}

func TestSparseLogicalLengthCannotCreatePhysicalFreeSpaceCredit(t *testing.T) {
	s := scratch.Usage{UsedBytes: 128, ActiveBytes: 128, ReservedBytes: 256, QuotaBytes: 1000, Space: space("a", 300, true)}
	if err := evaluate(plan{Scratch: 384}, s, space("a", 300, true), space("a", 300, true), 1, 4); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatal("unproven logical length was credited as physically allocated bytes", err)
	}
}
