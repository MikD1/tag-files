package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/process"
)

func TestTicksUseExactRationalArithmeticAndNullableUnknown(t *testing.T) {
	for _, tc := range []struct {
		value, base  string
		want         int64
		unknown, bad bool
	}{
		{value: "3003", base: "1/90000", want: 333667},
		{value: "9223372036854775807", base: "1/10000000", want: 9223372036854775807},
		{value: "922337203685.4775807", want: 9223372036854775807},
		{value: "0.00000005", want: 1},
		{value: "1.200000", want: 12000000},
		{value: "N/A", unknown: true}, {value: "", unknown: true},
		{value: "-1", bad: true}, {value: "922337203685.4775808", bad: true}, {value: "1", base: "1/0", bad: true},
	} {
		t.Run(tc.value+"_"+tc.base, func(t *testing.T) {
			got, err := durationTicks(tc.value, tc.base)
			if tc.bad {
				if err == nil {
					t.Fatal("invalid/overflowing duration accepted")
				}
				return
			}
			if err != nil || (got == nil) != tc.unknown {
				t.Fatalf("duration %v %v", got, err)
			}
			if !tc.unknown && *got != tc.want {
				t.Fatalf("exact ticks: got %d want %d", *got, tc.want)
			}
		})
	}
}
func nativeProcessor(t *testing.T) *Processor {
	t.Helper()
	if os.Getenv("TAG_FILES_MEDIA_INTEGRATION") != "1" {
		t.Skip("run scripts/check-media for actual FFmpeg fixtures")
	}
	p, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func pngFixture(t *testing.T, width, height int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "not-a-photo.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 7), G: uint8(y * 9), A: 255})
		}
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
func fixtureFFmpeg(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := process.Run(ctx, process.Command{Path: "ffmpeg", Args: append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-filter_threads", "1", "-y"}, args...)}, process.DefaultLimits())
	if err != nil {
		t.Fatalf("fixture FFmpeg: %v %s", err, result.Stderr)
	}
}
func TestNativeProbeUsesBytesAndRejectsAudioUnsupportedCodecAndCorruptInput(t *testing.T) {
	p := nativeProcessor(t)
	ctx := context.Background()
	input := pngFixture(t, 13, 9)
	info, err := p.Probe(ctx, input)
	if err != nil || info.Kind != domain.Image || info.Width != 13 || info.Height != 9 || info.ContentType != "image/png" || info.DurationTicks != nil {
		t.Fatalf("real PNG byte probe: %+v %v", info, err)
	}
	directory := t.TempDir()
	audio := filepath.Join(directory, "audio.mp4")
	fixtureFFmpeg(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-c:a", "aac", audio)
	if _, err := p.Probe(ctx, audio); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("audio-only MP4 admitted as video: %v", err)
	}
	unsupported := filepath.Join(directory, "unsupported.mkv")
	fixtureFFmpeg(t, "-f", "lavfi", "-i", "testsrc=size=32x24:rate=5:duration=0.2", "-c:v", "ffv1", unsupported)
	if _, err := p.Probe(ctx, unsupported); !errors.Is(err, ErrUnsupportedCodec) {
		t.Fatalf("unsupported decoder admitted: %v", err)
	}
	bad := filepath.Join(directory, "claimed.mp4")
	if err := os.WriteFile(bad, []byte("not any media"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Probe(ctx, bad); err == nil {
		t.Fatal("filename claim admitted corrupt bytes")
	}
}

func TestNativeProbeRefusesPlaylistNetworkAndAudioCoverArt(t *testing.T) {
	p := nativeProcessor(t)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	playlist := filepath.Join(t.TempDir(), "playlist.m3u8")
	if err := os.WriteFile(playlist, []byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\n"+server.URL+"/segment.ts\n#EXT-X-ENDLIST\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Probe(context.Background(), playlist); err == nil || requests.Load() != 0 {
		t.Fatal("scratch probe fetched an external source")
	}
	cover := pngFixture(t, 13, 9)
	audio := filepath.Join(t.TempDir(), "cover.m4a")
	fixtureFFmpeg(t, "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-i", cover, "-map", "0:a:0", "-map", "1:v:0", "-c:a", "aac", "-c:v", "copy", "-disposition:v:0", "attached_pic", audio)
	if _, err := p.Probe(context.Background(), audio); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("audio cover art admitted as primary video: %v", err)
	}
}

func TestDurationRejectsNonDecimalToolMetadata(t *testing.T) {
	for _, value := range []string{"1e8", "1/5", "0x1p8"} {
		if _, err := durationTicks(value, ""); err == nil {
			t.Fatalf("non-decimal metadata accepted: %s", value)
		}
	}
}
func TestScratchCopyStopsAtCancellationBeforeReadingMoreMedia(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &cancelReader{Reader: strings.NewReader(strings.Repeat("x", 1024*1024)), cancel: cancel}
	var output bytes.Buffer
	n, err := copyMedia(ctx, &output, reader, 1024*1024)
	if !errors.Is(err, context.Canceled) || n < 1 || n >= 1024*1024 {
		t.Fatalf("cancelled scratch IO continued: bytes=%d error=%v", n, err)
	}
}

type cancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *cancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}
