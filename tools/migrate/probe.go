package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/mikd1/tag-files/app/internal/process"
)

type ProbeResult struct {
	ItemID   int64           `json:"itemId"`
	SourceID string          `json:"sourceId"`
	SHA256   string          `json:"sha256"`
	Openable bool            `json:"openable"`
	Error    string          `json:"error,omitempty"`
	Streams  json.RawMessage `json:"streams,omitempty"`
}
type ProbeReport struct {
	Version   int           `json:"version"`
	RunID     string        `json:"runId"`
	Tool      string        `json:"tool"`
	Complete  bool          `json:"complete"`
	Results   []ProbeResult `json:"results"`
	CheckedAt string        `json:"checkedAt"`
}

// ValidateExceptions requires named owner decisions for every blocking source
// issue. Unknown schemas cannot be accepted as an automatic data-loss decision.
func ValidateExceptions(m *Manifest, exceptions []Exception) error {
	if _, err := resolve(m, exceptions); err != nil {
		return err
	}
	for _, issue := range m.Issues {
		if issue.Code == "unknown_schema" {
			return fmt.Errorf("unsupported schema requires a matching reviewed tool; raw archive retained")
		}
	}
	return nil
}

// ValidateProbe retains failures in the report, but permits an absent primary
// only when the matching source issue has an explicit missing_media decision.
func ValidateProbe(m *Manifest, report ProbeReport, exceptions []Exception) error {
	if err := ValidateExceptions(m, exceptions); err != nil {
		return err
	}
	if report.RunID != m.RunID {
		return fmt.Errorf("media inspection archive identity differs")
	}
	expected := map[int64]bool{}
	for _, item := range m.Model.Items {
		if item.Kind == "Image" || item.Kind == "Video" {
			expected[item.ID] = true
		}
	}
	seen := map[int64]bool{}
	for _, result := range report.Results {
		if !expected[result.ItemID] || seen[result.ItemID] {
			return fmt.Errorf("unexpected or duplicate media inspection result")
		}
		seen[result.ItemID] = true
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("media inspection incomplete: checked %d of %d items", len(seen), len(expected))
	}
	decisions, _ := resolve(m, exceptions)
	var failures []error
	for _, result := range report.Results {
		if result.Openable {
			continue
		}
		entity := fmt.Sprintf("item:%d", result.ItemID)
		if result.Error == "missing primary" && decisions["missing_primary\x00"+entity] == "missing_media" {
			continue
		}
		archived := false
		for _, item := range m.Model.Items {
			if item.ID == result.ItemID && !item.Valid {
				archived = decisions["invalid_row\x00"+entity] == "archive_only" || decisions["invalid_duration\x00"+entity] == "archive_only"
			}
		}
		if !archived {
			failures = append(failures, fmt.Errorf("media inspection failed for item %d: %s", result.ItemID, result.Error))
		}
	}
	return errors.Join(failures...)
}

// Probe checks the existing bytes without transcoding or changing the archive.
// This opening check supplements the complete source/destination hashes.
func Probe(ctx context.Context, root, ffprobe string) (ProbeReport, error) {
	report := ProbeReport{Version: 1, CheckedAt: now()}
	m, e := LoadArchive(ctx, root)
	if e != nil {
		return report, e
	}
	report.RunID = m.RunID
	path, e := exec.LookPath(ffprobe)
	if e != nil {
		return report, e
	}
	version, e := process.Run(ctx, process.Command{Path: path, Args: []string{"-version"}}, process.DefaultLimits())
	if e != nil {
		return report, e
	}
	report.Tool = string(version.Stdout)
	latest := map[string]SourceObject{}
	for _, o := range m.Objects {
		if o.Latest && !o.DeleteMarker {
			latest[o.Bucket+"\x00"+o.Key] = o
		}
	}
	var failures []error
	for _, i := range m.Model.Items {
		if i.Kind != "Image" && i.Kind != "Video" {
			continue
		}
		o, ok := latest["library\x00"+i.FileName]
		result := ProbeResult{ItemID: i.ID, SourceID: o.Identity(), SHA256: o.SHA256}
		if !ok {
			result.Error = "missing primary"
		} else {
			p, e := safePath(root, o.ArchivePath)
			if e != nil {
				return report, e
			}
			bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
			r, runErr := process.Run(bounded, process.Command{Path: path, Args: []string{"-v", "error", "-protocol_whitelist", "file,pipe", "-show_entries", "stream=index,codec_name,codec_type,width,height", "-of", "json", p}}, process.DefaultLimits())
			cancel()
			var doc struct {
				Streams []struct {
					CodecType string `json:"codec_type"`
				}
			}
			if runErr != nil {
				result.Error = fmt.Sprintf("ffprobe: %v; %s", runErr, r.Stderr)
			} else if e = json.Unmarshal(r.Stdout, &doc); e != nil {
				result.Error = "invalid ffprobe output"
			} else {
				for _, s := range doc.Streams {
					if s.CodecType == "video" {
						result.Openable = true
					}
				}
				if !result.Openable {
					result.Error = "no image/video stream"
				} else {
					result.Streams = json.RawMessage(r.Stdout)
				}
			}
		}
		if !result.Openable {
			failures = append(failures, fmt.Errorf("item %d: %s", i.ID, result.Error))
		}
		report.Results = append(report.Results, result)
	}
	report.Complete = len(failures) == 0
	return report, errors.Join(failures...)
}
