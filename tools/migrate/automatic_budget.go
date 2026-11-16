package migrate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

type PlanStatistics struct {
	Objects            int   `json:"objects"`
	SourceBytes        int64 `json:"sourceBytes"`
	TargetBytes        int64 `json:"targetBytes"`
	LargestObjectBytes int64 `json:"largestObjectBytes"`
	SharedCopyBytes    int64 `json:"sharedCopyBytes"`
	MultipartParts     int64 `json:"multipartParts"`
}

type MetadataMeasurement struct {
	Files          int64 `json:"files"`
	LogicalBytes   int64 `json:"logicalBytes"`
	AllocatedBytes int64 `json:"allocatedBytes"`
}

type BudgetEvidence struct {
	Version       int                 `json:"version"`
	Method        string              `json:"method"`
	Plan          PlanStatistics      `json:"plan"`
	Metadata      MetadataMeasurement `json:"measuredTargetMetadata"`
	SnapshotBytes int64               `json:"snapshotBytes"`
	ManifestBytes int64               `json:"manifestBytes"`
	Budget        Budget              `json:"budget"`
}

func budgetAdd(values ...int64) (int64, error) {
	var sum int64
	for _, n := range values {
		if n < 0 || n > maxInt-sum {
			return 0, ErrBudget
		}
		sum += n
	}
	return sum, nil
}

// MeasureMetadata records allocated blocks as well as logical SQLite bytes.
// Symlinks and special files cannot be mistaken for owned migration metadata.
func MeasureMetadata(root string) (MetadataMeasurement, error) {
	var result MetadataMeasurement
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("target metadata contains a nonregular entry")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Blocks < 0 || stat.Blocks > maxInt/512 {
			return ErrBudget
		}
		result.AllocatedBytes, err = budgetAdd(result.AllocatedBytes, stat.Blocks*512)
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			result.Files++
			result.LogicalBytes, err = budgetAdd(result.LogicalBytes, info.Size())
		}
		return err
	})
	return result, err
}

// AutomaticBudget records measurements and conservative derived allowances; it
// does not present these estimates as a measured storage pilot. The write
// allowance covers two additional largest-object copies, a part, and 256 MiB
// of storage working data. Metadata allows four copies of the observed target,
// source snapshot and manifest, plus per-row and per-part journal overhead.
// Existing per-write capacity gates remain authoritative throughout transfer.
func AutomaticBudget(m *Manifest, measured MetadataMeasurement, snapshotBytes, manifestBytes, partSize int64) (BudgetEvidence, error) {
	e := BudgetEvidence{Version: 1, Method: "conservative-derived-v1; measured target allocation; capacity checked before every write", Metadata: measured, SnapshotBytes: snapshotBytes, ManifestBytes: manifestBytes}
	if partSize == 0 {
		partSize = 16 << 20
	}
	if partSize < 5<<20 || partSize > 5<<30 || measured.Files < 0 {
		return e, ErrBudget
	}
	for _, o := range m.Objects {
		if !o.DeleteMarker {
			var err error
			e.Plan.SourceBytes, err = budgetAdd(e.Plan.SourceBytes, o.Size)
			if err != nil {
				return e, err
			}
		}
	}
	seen := map[string]bool{}
	plans := makePlans(m, "budget-estimate")
	e.Plan.Objects = len(plans)
	for _, p := range plans {
		var err error
		e.Plan.TargetBytes, err = budgetAdd(e.Plan.TargetBytes, p.Size)
		if err != nil {
			return e, err
		}
		e.Plan.LargestObjectBytes = max(e.Plan.LargestObjectBytes, p.Size)
		if seen[p.SourceID] {
			e.Plan.SharedCopyBytes, err = budgetAdd(e.Plan.SharedCopyBytes, p.Size)
			if err != nil {
				return e, err
			}
		}
		seen[p.SourceID] = true
		if p.Size > 0 {
			e.Plan.MultipartParts, err = budgetAdd(e.Plan.MultipartParts, (p.Size-1)/partSize+1)
			if err != nil {
				return e, err
			}
		}
	}
	peak, err := budgetAdd(e.Plan.LargestObjectBytes, e.Plan.LargestObjectBytes, partSize, 256<<20)
	if err != nil {
		return e, err
	}
	base, err := budgetAdd(measured.LogicalBytes, measured.AllocatedBytes, snapshotBytes, manifestBytes)
	if err != nil || base > maxInt/4 {
		return e, ErrBudget
	}
	rows := int64(len(m.Model.Items)) + int64(len(m.Model.Tags)) + int64(len(m.Model.Collections)) + int64(len(m.Model.Categories)) + int64(len(m.Model.Joins)) + int64(len(m.Model.Processing)) + int64(len(plans))
	if rows > maxInt/(16<<10) || e.Plan.MultipartParts > maxInt/4096 {
		return e, ErrBudget
	}
	metadata, err := budgetAdd(64<<20, 4*base, rows*(16<<10), e.Plan.MultipartParts*4096)
	if err != nil {
		return e, err
	}
	e.Budget = Budget{WritePeakBytes: peak, MetadataBytes: metadata, ReserveBytes: max(10000000000, e.Plan.TargetBytes/10)}
	if _, err = budgetAdd(e.Plan.TargetBytes, peak, metadata, e.Budget.ReserveBytes); err != nil {
		return e, err
	}
	return e, nil
}
