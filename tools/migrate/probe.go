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
