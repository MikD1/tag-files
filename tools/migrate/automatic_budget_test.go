package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAutomaticBudgetCountsIndependentCopiesOfSharedPrimary(t *testing.T) {
	m := &Manifest{RunID: "fixture", Objects: []SourceObject{
		{Bucket: "library", Key: "shared", Latest: true, Size: 100},
		{Bucket: "extra", Key: "orphan", Latest: true, Size: 7},
	}, Model: Model{Items: []Item{
		{ID: 1, FileName: "shared", Valid: true},
		{ID: 2, FileName: "shared", Valid: true},
	}}}
	e, err := AutomaticBudget(m, MetadataMeasurement{LogicalBytes: 4096, AllocatedBytes: 8192}, 4096, 2048, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	if e.Plan.TargetBytes != 207 || e.Plan.SourceBytes != 107 || e.Plan.Objects != 3 || e.Plan.LargestObjectBytes != 100 || e.Plan.SharedCopyBytes != 100 {
		t.Fatalf("lost independently owned bytes: %+v", e.Plan)
	}
	if e.Budget.WritePeakBytes < 200 || e.Budget.MetadataBytes <= 8192 || e.Budget.ReserveBytes < 10000000000 || e.Metadata.AllocatedBytes != 8192 {
		t.Fatalf("missing working/metadata/reserve budget evidence: %+v", e)
	}
	for _, measurement := range []MetadataMeasurement{{LogicalBytes: -1}, {AllocatedBytes: maxInt}} {
		if _, err := AutomaticBudget(m, measurement, 4096, 2048, 16<<20); !errors.Is(err, ErrBudget) {
			t.Fatalf("unsafe metadata estimate accepted: %v", err)
		}
	}
	m.Objects[0].Size = maxInt
	if _, err := AutomaticBudget(m, MetadataMeasurement{}, 4096, 2048, 16<<20); !errors.Is(err, ErrBudget) {
		t.Fatalf("overflow accepted: %v", err)
	}
}

func TestExplicitMissingMediaDecisionAllowsOnlyAbsentProbePrimary(t *testing.T) {
	m := &Manifest{Model: Model{Items: []Item{{ID: 13, Kind: "Image", Valid: true}}}, Issues: []Issue{{ID: "missing-13", Code: "missing_primary", Entity: "item:13", Blocking: true}}}
	r := ProbeReport{Results: []ProbeResult{{ItemID: 13, Error: "missing primary"}}}
	if err := ValidateProbe(m, r, nil); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("implicit loss accepted: %v", err)
	}
	x := []Exception{{IssueID: "missing-13", Decision: "missing_media", Reason: "original no longer exists", Owner: "owner"}}
	if err := ValidateProbe(m, r, x); err != nil {
		t.Fatal(err)
	}
	r.Results[0].Error = "ffprobe: invalid media"
	if err := ValidateProbe(m, r, x); err == nil {
		t.Fatal("a missing-media exception accepted corrupt existing bytes")
	}
}

func TestProbeGateRefusesPartialInspectionEvenAfterEarlierSuccess(t *testing.T) {
	m := &Manifest{RunID: "run", Model: Model{Items: []Item{{ID: 1, Kind: "Image"}, {ID: 2, Kind: "Video"}}}}
	r := ProbeReport{RunID: "run", Results: []ProbeResult{{ItemID: 1, Openable: true}}}
	if err := ValidateProbe(m, r, nil); err == nil {
		t.Fatal("partial probe report permitted uninspected media")
	}
	r.Results = append(r.Results, ProbeResult{ItemID: 2, Openable: true})
	if err := ValidateProbe(m, r, nil); err != nil {
		t.Fatal(err)
	}
	r.RunID = "different"
	if err := ValidateProbe(m, r, nil); err == nil {
		t.Fatal("a different archive's probe was accepted")
	}
}

func TestMetadataMeasurementUsesActualFilesAndRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "library.sqlite"), make([]byte, 8193), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := MeasureMetadata(root)
	if err != nil || m.Files != 1 || m.LogicalBytes != 8193 || m.AllocatedBytes < 8193 {
		t.Fatalf("invalid measured allocation: %+v %v", m, err)
	}
	if err := os.Symlink("library.sqlite", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := MeasureMetadata(root); err == nil {
		t.Fatal("metadata symlink accepted")
	}
}
