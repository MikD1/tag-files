package media

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
)

//go:embed testdata/*.heic testdata/*.avif testdata/quadrants.png
var heifFixtures embed.FS

func heifFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := heifFixtures.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Choosing only the first tile would lose three quadrants; selecting the
// thumbnail would lose resolution. Expectations come from our source pixels.
func TestNativeHEIFUsesWholePrimaryCanvas(t *testing.T) {
	p := nativeProcessor(t)
	for _, tc := range []struct {
		name          string
		width, height int64
	}{
		{"single.heic", 128, 96}, {"grid.heic", 128, 96},
		{"grid24.heic", 384, 256}, {"thumbnail.heic", 128, 96},
		{"single.avif", 128, 96}, {"grid.avif", 128, 96},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := heifFixture(t, tc.name)
			info, err := p.Probe(context.Background(), input)
			if err != nil || info.Kind != domain.Image || info.Width != tc.width || info.Height != tc.height {
				t.Fatalf("primary canvas: %+v %v; want %dx%d", info, err, tc.width, tc.height)
			}
			output := filepath.Join(t.TempDir(), "prepared")
			prepared, err := p.Prepare(context.Background(), input, output)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Output.Width != tc.width || prepared.Output.Height != tc.height {
				t.Fatalf("published only part of primary: %+v", prepared.Output)
			}
			if filepath.Ext(tc.name) == ".avif" {
				original, _ := os.ReadFile(input)
				actual, _ := os.ReadFile(output)
				if !bytes.Equal(original, actual) || prepared.Output.ContentType != "image/avif" {
					t.Fatal("usable AVIF primary was not preserved")
				}
			}
			decoded := output
			if prepared.Output.ContentType != "image/png" {
				decoded = filepath.Join(t.TempDir(), "pixels.png")
				if tc.name == "grid.avif" {
					fixtureFFmpeg(t, "-i", output, "-filter_complex_threads", "1", "-filter_complex", "[0:0][0:1][0:2][0:3]xstack=inputs=4:layout=0_0|64_0|0_64|64_64,crop=128:96:0:0:exact=1[pixels]", "-map", "[pixels]", "-frames:v", "1", decoded)
				} else {
					fixtureFFmpeg(t, "-i", output, "-frames:v", "1", decoded)
				}
			}
			assertQuadrantPixels(t, decoded, int(tc.width), int(tc.height))
		})
	}
}

func TestNativeHEIFGridAppliesPrimaryRotationAndMirror(t *testing.T) {
	p := nativeProcessor(t)
	for _, tc := range []struct {
		name          string
		width, height int
		corners       [4]int
	}{
		{"grid-rotated.heic", 96, 128, [4]int{1, 3, 0, 2}},
		{"grid-mirrored.heic", 128, 96, [4]int{2, 3, 0, 1}},
		{"grid-horizontal.heic", 128, 96, [4]int{1, 0, 3, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepared, err := p.Prepare(context.Background(), heifFixture(t, tc.name), filepath.Join(t.TempDir(), "primary"))
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Output.Width != int64(tc.width) || prepared.Output.Height != int64(tc.height) || prepared.Output.Rotation != 0 {
				t.Fatalf("ignored primary transform: %+v", prepared.Output)
			}
			assertCorners(t, prepared.Path, tc.width, tc.height, tc.corners)
		})
	}
}

func TestNativeHEIFRefusesUnimplementedAlphaAndHDRConversions(t *testing.T) {
	p := nativeProcessor(t)
	for _, name := range []string{"alpha.heic", "hdr.heic"} {
		t.Run(name, func(t *testing.T) {
			input := heifFixture(t, name)
			output := filepath.Join(t.TempDir(), "primary")
			if _, err := p.Prepare(context.Background(), input, output); !errors.Is(err, ErrUnsupportedCodec) {
				t.Fatalf("silently discarded alpha or HDR color: %v", err)
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unsupported conversion left a publishable output")
			}
			if _, err := os.Stat(input); err != nil {
				t.Fatal("unsupported conversion removed its input")
			}
		})
	}
}

func TestNativeHEIFRejectsBrokenLastTileAndAppliesCanvasBudget(t *testing.T) {
	p := nativeProcessor(t)
	input := heifFixture(t, "grid.avif")
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	// The first tile still decodes; only the final tile's AV1 OBU is damaged.
	for i := len(data) - 40; i < len(data); i++ {
		data[i] = 255
	}
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Probe(context.Background(), input); err != nil {
		t.Fatalf("fixture must reach full decoding, not fail metadata parsing: %v", err)
	}
	output := filepath.Join(t.TempDir(), "primary")
	if _, err := p.Prepare(context.Background(), input, output); !errors.Is(err, ErrInvalidMedia) {
		t.Fatalf("only the first tile was validated: %v", err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("broken grid left a publishable output")
	}
	config := DefaultConfig()
	config.MaxPixels = 8000 // Every 64x64 tile fits, the whole canvas does not.
	bounded, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.Probe(context.Background(), heifFixture(t, "grid.heic")); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("pixel bound applied only to individual tiles: %v", err)
	}
}

func assertQuadrantPixels(t *testing.T, path string, width, height int) {
	assertCorners(t, path, width, height, [4]int{0, 1, 2, 3})
}
func assertCorners(t *testing.T, path string, width, height int, corners [4]int) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != width || img.Bounds().Dy() != height {
		t.Fatalf("pixels cropped to %v, want %dx%d", img.Bounds(), width, height)
	}
	colors := [4][3]int{{240, 10, 10}, {10, 240, 10}, {10, 10, 240}, {240, 240, 10}}
	for i, point := range []struct{ x, y int }{
		{8, 8}, {width - 9, 8}, {8, height - 9}, {width - 9, height - 9},
	} {
		got := color.NRGBAModel.Convert(img.At(point.x, point.y)).(color.NRGBA)
		want := colors[corners[i]]
		if abs(int(got.R)-want[0]) > 20 || abs(int(got.G)-want[1]) > 20 || abs(int(got.B)-want[2]) > 20 || got.A != 255 {
			t.Fatalf("lost/reordered tile at %d,%d: %+v", point.x, point.y, got)
		}
	}
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
