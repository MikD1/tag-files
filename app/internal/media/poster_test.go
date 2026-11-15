package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
)

func readPoster(t *testing.T, prepared Prepared, width, height int64) image.Image {
	t.Helper()
	data, err := os.ReadFile(prepared.Path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if int64(img.Bounds().Dx()) != width || int64(img.Bounds().Dy()) != height || prepared.Output.Width != width || prepared.Output.Height != height || prepared.Output.Rotation != 0 || prepared.Output.Kind != domain.Image || prepared.Output.ContentType != "image/jpeg" || prepared.Output.DurationTicks != nil || prepared.Output.Size != int64(len(data)) || prepared.Output.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("invalid poster descriptor: %+v bounds=%v", prepared, img.Bounds())
	}
	return img
}

// These cases catch hidden upscaling, portrait distortion and descriptors
// copied from the primary rather than measured from the actual JPEG.
func TestNativePosterFits640WithoutUpscaleAndPreservesPrimary(t *testing.T) {
	p := nativeProcessor(t)
	for _, tc := range []struct {
		width, height         int
		wantWidth, wantHeight int64
	}{
		{13, 9, 13, 9}, {1280, 720, 640, 360}, {720, 1280, 360, 640}, {1001, 1000, 640, 639},
	} {
		input := pngFixture(t, tc.width, tc.height)
		before, err := os.ReadFile(input)
		if err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(t.TempDir(), "poster")
		got, err := p.PreparePoster(context.Background(), input, output)
		if err != nil {
			t.Fatal(err)
		}
		readPoster(t, got, tc.wantWidth, tc.wantHeight)
		after, err := os.ReadFile(input)
		if err != nil || !bytes.Equal(after, before) {
			t.Fatal("poster changed primary")
		}
		if _, err := p.PreparePoster(context.Background(), input, output); !errors.Is(err, os.ErrExist) {
			t.Fatalf("poster overwritten: %v", err)
		}
		unchanged, _ := os.ReadFile(output)
		if sum := sha256.Sum256(unchanged); hex.EncodeToString(sum[:]) != got.Output.SHA256 {
			t.Fatal("failed retry changed output")
		}
	}
}

func TestNativePosterCompositesTransparencyOnWhite(t *testing.T) {
	p := nativeProcessor(t)
	input := filepath.Join(t.TempDir(), "transparent.png")
	img := image.NewNRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < 24; y++ {
		for x := 8; x < 16; x++ {
			img.SetNRGBA(x, y, color.NRGBA{G: 255, A: 128})
		}
		for x := 16; x < 32; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, A: 255})
		}
	}
	file, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	prepared, err := p.PreparePoster(context.Background(), input, filepath.Join(t.TempDir(), "poster"))
	if err != nil {
		t.Fatal(err)
	}
	poster := readPoster(t, prepared, 32, 24)
	r, g, b, _ := poster.At(4, 12).RGBA()
	if r < 60000 || g < 60000 || b < 60000 {
		t.Fatalf("transparent pixels became dark: %d %d %d", r, g, b)
	}
	r, g, b, _ = poster.At(26, 12).RGBA()
	if r < 55000 || g > 6000 || b > 6000 {
		t.Fatalf("opaque pixels changed: %d %d %d", r, g, b)
	}
	r, g, b, _ = poster.At(12, 12).RGBA()
	if r < 29000 || r > 36000 || g < 60000 || b < 29000 || b > 36000 {
		t.Fatalf("partial alpha was discarded instead of composited: %d %d %d", r, g, b)
	}
}

func TestNativeVideoPosterUsesRepresentativeFrameAndDisplayOrientation(t *testing.T) {
	p := nativeProcessor(t)
	directory := t.TempDir()
	input := filepath.Join(directory, "base.mp4")
	rotated := filepath.Join(directory, "rotated.mp4")
	fixtureFFmpeg(t, "-f", "lavfi", "-i", "color=red:s=80x48:r=10:d=0.1", "-f", "lavfi", "-i", "color=blue:s=80x48:r=10:d=1.9", "-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0", "-c:v", "libx264", "-pix_fmt", "yuv420p", input)
	fixtureFFmpeg(t, "-display_rotation:v:0", "90", "-i", input, "-c", "copy", rotated)
	prepared, err := p.PreparePoster(context.Background(), rotated, filepath.Join(directory, "poster"))
	if err != nil {
		t.Fatal(err)
	}
	poster := readPoster(t, prepared, 48, 80)
	r, g, b, _ := poster.At(24, 40).RGBA()
	if b < 55000 || r > 6000 || g > 6000 {
		t.Fatalf("poster selected initial red frame: %d %d %d", r, g, b)
	}
}

func TestNativePosterFailureKeepsPrimaryAndRemovesPartialOutput(t *testing.T) {
	_ = nativeProcessor(t)
	config := DefaultConfig()
	config.MaxOutputBytes = 20
	p, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	input := pngFixture(t, 20, 20)
	output := filepath.Join(t.TempDir(), "poster")
	before, _ := os.ReadFile(input)
	if _, err := p.PreparePoster(context.Background(), input, output); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("unbounded poster: %v", err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial poster remains")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.PreparePoster(ctx, input, output); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	after, _ := os.ReadFile(input)
	if !bytes.Equal(before, after) {
		t.Fatal("poster failure changed primary")
	}
}

func TestNativePosterPreservesWholeHEIFCanvasAndTransforms(t *testing.T) {
	p := nativeProcessor(t)
	for _, tc := range []struct {
		name          string
		width, height int64
		corners       [4]int
	}{
		{"single.heic", 128, 96, [4]int{0, 1, 2, 3}},
		{"grid.heic", 128, 96, [4]int{0, 1, 2, 3}},
		{"grid24.heic", 384, 256, [4]int{0, 1, 2, 3}},
		{"thumbnail.heic", 128, 96, [4]int{0, 1, 2, 3}},
		{"single.avif", 128, 96, [4]int{0, 1, 2, 3}},
		{"grid.avif", 128, 96, [4]int{0, 1, 2, 3}},
		{"grid-rotated.heic", 96, 128, [4]int{1, 3, 0, 2}},
		{"grid-mirrored.heic", 128, 96, [4]int{2, 3, 0, 1}},
		{"grid-horizontal.heic", 128, 96, [4]int{1, 0, 3, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepared, err := p.PreparePoster(context.Background(), heifFixture(t, tc.name), filepath.Join(t.TempDir(), "poster"))
			if err != nil {
				t.Fatal(err)
			}
			assertPosterCorners(t, readPoster(t, prepared, tc.width, tc.height), tc.corners)
		})
	}
}

func TestNativePosterAppliesAllJPEGExifOrientations(t *testing.T) {
	p := nativeProcessor(t)
	for _, tc := range []struct {
		orientation   byte
		width, height int64
		corners       [4]int
	}{
		{1, 128, 96, [4]int{0, 1, 2, 3}}, {2, 128, 96, [4]int{1, 0, 3, 2}},
		{3, 128, 96, [4]int{3, 2, 1, 0}}, {4, 128, 96, [4]int{2, 3, 0, 1}},
		{5, 96, 128, [4]int{0, 2, 1, 3}}, {6, 96, 128, [4]int{2, 0, 3, 1}},
		{7, 96, 128, [4]int{3, 1, 2, 0}}, {8, 96, 128, [4]int{1, 3, 0, 2}},
	} {
		t.Run(strconv.Itoa(int(tc.orientation)), func(t *testing.T) {
			prepared, err := p.PreparePoster(context.Background(), jpegExifFixture(t, tc.orientation), filepath.Join(t.TempDir(), "poster"))
			if err != nil {
				t.Fatal(err)
			}
			assertPosterCorners(t, readPoster(t, prepared, tc.width, tc.height), tc.corners)
		})
	}
}

func TestNativePosterUsesFirstAnimationFrameWithoutChangingPrimary(t *testing.T) {
	p := nativeProcessor(t)
	for _, format := range []string{"apng", "gif"} {
		t.Run(format, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "animated."+format)
			fixtureFFmpeg(t, "-f", "lavfi", "-i", "color=red:s=32x24:r=5:d=0.2", "-f", "lavfi", "-i", "color=blue:s=32x24:r=5:d=0.4", "-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0", "-r", "5", "-f", format, input)
			before, _ := os.ReadFile(input)
			prepared, err := p.PreparePoster(context.Background(), input, filepath.Join(t.TempDir(), "poster"))
			if err != nil {
				t.Fatal(err)
			}
			img := readPoster(t, prepared, 32, 24)
			r, g, b, _ := img.At(16, 12).RGBA()
			if r < 55000 || g > 6000 || b > 6000 {
				t.Fatal("first animation frame changed")
			}
			after, _ := os.ReadFile(input)
			if !bytes.Equal(before, after) {
				t.Fatal("animation primary changed")
			}
		})
	}
}

func assertPosterCorners(t *testing.T, img image.Image, corners [4]int) {
	t.Helper()
	colors := [4][3]int{{240, 10, 10}, {10, 240, 10}, {10, 10, 240}, {240, 240, 10}}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	for i, point := range [4][2]int{{8, 8}, {w - 9, 8}, {8, h - 9}, {w - 9, h - 9}} {
		got := color.NRGBAModel.Convert(img.At(point[0], point[1])).(color.NRGBA)
		want := colors[corners[i]]
		if abs(int(got.R)-want[0]) > 25 || abs(int(got.G)-want[1]) > 25 || abs(int(got.B)-want[2]) > 25 {
			t.Fatalf("lost primary corner %d: %+v", i, got)
		}
	}
}
