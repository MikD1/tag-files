package migrate

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAutomaticArchiveProbeChecksEveryLegacyImageAndVideo(t *testing.T) {
	ffprobe, e := exec.LookPath("ffprobe")
	if e != nil {
		t.Skip("ffprobe unavailable")
	}
	ffmpeg, e := exec.LookPath("ffmpeg")
	if e != nil {
		t.Skip("ffmpeg unavailable")
	}
	p := legacyFixture(t)
	s := fixtureSource()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var pngBytes bytes.Buffer
	if e = png.Encode(&pngBytes, img); e != nil {
		t.Fatal(e)
	}
	video := filepath.Join(t.TempDir(), "fixture.mp4")
	out, e := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=16x16:d=0.2", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-y", video).CombinedOutput()
	if e != nil {
		t.Fatalf("fixture ffmpeg: %v %s", e, out)
	}
	videoBytes, e := os.ReadFile(video)
	if e != nil {
		t.Fatal(e)
	}
	for idx, o := range s.objects {
		var b []byte
		switch o.Key {
		case "image.jpg":
			b = pngBytes.Bytes()
		case "video.mp4":
			b = videoBytes
		}
		if b != nil {
			s.objects[idx].Size = int64(len(b))
			s.data[o.Identity()] = b
		}
	}
	dir := filepath.Join(t.TempDir(), "archive")
	if _, e = Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "probe", RollbackReference: "cold", RollbackVerified: true}, s); e != nil {
		t.Fatal(e)
	}
	report, e := Probe(ctx, dir, ffprobe)
	if e != nil || !report.Complete || len(report.Results) != 2 {
		t.Fatal(report, e)
	}
}
func TestArchiveProbeReportsCorruptBytesWithoutDiscardingThem(t *testing.T) {
	m, _, dir := scanned(t)
	_ = m
	report, e := Probe(ctx, dir, "ffprobe")
	if e == nil || report.Complete || len(report.Results) != 2 {
		t.Fatal(report, e)
	}
	if _, e = LoadArchive(ctx, dir); e != nil {
		t.Fatal("probe changed original archive", e)
	}
}
