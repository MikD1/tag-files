package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
)

func TestNativePreparePreservesUsablePhotoBytesAndRefusesOverwrite(t *testing.T) {
	p := nativeProcessor(t)
	input := pngFixture(t, 13, 9)
	output := filepath.Join(t.TempDir(), "output")
	prepared, err := p.Prepare(context.Background(), input, output)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(got)
	if !bytes.Equal(got, original) || prepared.Output.Kind != domain.Image || prepared.Output.Width != 13 || prepared.Output.Height != 9 || prepared.Output.SHA256 != hex.EncodeToString(sum[:]) || prepared.Output.Size != int64(len(got)) {
		t.Fatalf("photo changed or descriptor not derived: %+v", prepared)
	}
	if _, err := p.Prepare(context.Background(), input, output); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing output overwritten: %v", err)
	}
	unchanged, _ := os.ReadFile(output)
	if !bytes.Equal(unchanged, got) {
		t.Fatal("failed prepare changed immutable output")
	}
}
func TestNativePrepareConvertsVideoWithAACFaststartAndPreservesResolution(t *testing.T) {
	p := nativeProcessor(t)
	directory := t.TempDir()
	input := filepath.Join(directory, "input.mov")
	output := filepath.Join(directory, "primary")
	fixtureFFmpeg(t, "-f", "lavfi", "-i", "testsrc=size=80x48:rate=10:duration=0.6", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.6", "-c:v", "mpeg4", "-c:a", "pcm_s16le", "-shortest", input)
	prepared, err := p.Prepare(context.Background(), input, output)
	if err != nil {
		t.Fatal(err)
	}
	info, err := p.Probe(context.Background(), output)
	if err != nil || info.Kind != domain.Video || info.VideoCodec != "h264" || info.AudioCodec != "aac" || info.Width != 80 || info.Height != 48 || info.Rotation != 0 || prepared.Output.DurationTicks == nil || *prepared.Output.DurationTicks != 6000000 {
		t.Fatalf("video profile: %+v %+v %v", info, prepared, err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if moov, mdat := bytes.Index(data, []byte("moov")), bytes.Index(data, []byte("mdat")); moov < 0 || mdat < 0 || moov > mdat {
		t.Fatal("MP4 did not put metadata before playback data")
	}
}
func TestNativePrepareAppliesRotationWithoutResizingOrDoubleRotation(t *testing.T) {
	p := nativeProcessor(t)
	directory := t.TempDir()
	base := filepath.Join(directory, "base.mp4")
	input := filepath.Join(directory, "rotated.mp4")
	output := filepath.Join(directory, "output")
	fixtureFFmpeg(t, "-f", "lavfi", "-i", "testsrc=size=80x48:rate=10:duration=0.3", "-c:v", "libx264", "-pix_fmt", "yuv420p", base)
	fixtureFFmpeg(t, "-display_rotation:v:0", "90", "-i", base, "-c", "copy", input)
	info, err := p.Probe(context.Background(), input)
	if err != nil || info.Rotation == 0 {
		t.Fatal("rotation fixture has no rotation", err)
	}
	prepared, err := p.Prepare(context.Background(), input, output)
	if err != nil {
		t.Fatal(err)
	}
	info, err = p.Probe(context.Background(), output)
	if err != nil || info.Width != 48 || info.Height != 80 || info.Rotation != 0 || prepared.Output.Width != 48 || prepared.Output.Height != 80 {
		t.Fatalf("orientation/resolution changed: %+v %+v %v", info, prepared, err)
	}
}

func TestNativePrepareConvertsBMPAndRejectsTruncatedPixels(t *testing.T) {
	p := nativeProcessor(t)
	directory := t.TempDir()
	input := filepath.Join(directory, "input.bmp")
	source := pngFixture(t, 13, 9)
	fixtureFFmpeg(t, "-i", source, "-frames:v", "1", "-c:v", "bmp", input)
	prepared, err := p.Prepare(context.Background(), input, filepath.Join(directory, "converted"))
	if err != nil || prepared.Output.ContentType != "image/png" || prepared.Output.Width != 13 || prepared.Output.Height != 9 {
		t.Fatalf("nonbrowser photo not converted losslessly at same dimensions: %+v %v", prepared, err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(directory, "truncated.png")
	if err := os.WriteFile(broken, data[:len(data)/2], 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(directory, "not-published")
	if _, err := p.Prepare(context.Background(), broken, output); err == nil {
		t.Fatal("truncated pixel data was accepted")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed prepare left output available to publish")
	}
}

func TestNativePrepareResourceFailurePreservesInputAndRemovesPartialOutput(t *testing.T) {
	nativeProcessor(t)
	config := DefaultConfig()
	config.MaxOutputBytes = 16
	p, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	input := pngFixture(t, 13, 9)
	before, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "bounded-output")
	if _, err := p.Prepare(context.Background(), input, output); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("output budget ignored: %v", err)
	}
	after, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("output budget failure changed input")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("oversized partial output retained")
	}
	config = DefaultConfig()
	config.MaxPixels = 32
	p, err = New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Probe(context.Background(), input); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("pixel allocation budget ignored: %v", err)
	}
}

func TestNativePhotoAtExactConfiguredOutputLimitRemainsValid(t *testing.T) {
	nativeProcessor(t)
	input := pngFixture(t, 13, 9)
	stat, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.MaxOutputBytes = stat.Size()
	p, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := p.Prepare(context.Background(), input, filepath.Join(t.TempDir(), "exact-limit"))
	if err != nil || prepared.Output.Size != stat.Size() {
		t.Fatalf("inclusive output bound rejected intact media: %+v %v", prepared, err)
	}
}

func TestNativeAnimatedPNGAndGIFPreserveEveryFrame(t *testing.T) {
	p := nativeProcessor(t)
	for _, format := range []string{"apng", "gif"} {
		t.Run(format, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "animated."+format)
			fixtureFFmpeg(t, "-f", "lavfi", "-i", "testsrc=size=32x24:rate=5:duration=0.6", "-f", format, input)
			output := filepath.Join(t.TempDir(), "primary")
			prepared, err := p.Prepare(context.Background(), input, output)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) || prepared.Output.Kind != domain.Image || prepared.Output.Width != 32 || prepared.Output.Height != 24 {
				t.Fatalf("animated photo was flattened or changed: %+v", prepared.Output)
			}
		})
	}
}

func TestNativeJPEGExifOrientationAndMirroringKeepDisplayPixels(t *testing.T) {
	p := nativeProcessor(t)
	// Corner order is top-left, top-right, bottom-left, bottom-right after
	// applying the EXIF orientation, derived from the four source quadrants.
	for _, tc := range []struct {
		orientation   byte
		width, height int
		corners       [4]int
	}{
		{1, 128, 96, [4]int{0, 1, 2, 3}}, {2, 128, 96, [4]int{1, 0, 3, 2}},
		{3, 128, 96, [4]int{3, 2, 1, 0}}, {4, 128, 96, [4]int{2, 3, 0, 1}},
		{5, 96, 128, [4]int{0, 2, 1, 3}}, {6, 96, 128, [4]int{2, 0, 3, 1}},
		{7, 96, 128, [4]int{3, 1, 2, 0}}, {8, 96, 128, [4]int{1, 3, 0, 2}},
	} {
		t.Run(string(rune('0'+tc.orientation)), func(t *testing.T) {
			input := jpegExifFixture(t, tc.orientation)
			output := filepath.Join(t.TempDir(), "primary")
			prepared, err := p.Prepare(context.Background(), input, output)
			if err != nil {
				t.Fatal(err)
			}
			if prepared.Output.Width != int64(tc.width) || prepared.Output.Height != int64(tc.height) || prepared.Output.Rotation != 0 {
				t.Fatalf("wrong displayed photo shape: %+v", prepared.Output)
			}
			if tc.orientation == 1 {
				original, _ := os.ReadFile(input)
				actual, _ := os.ReadFile(output)
				if !bytes.Equal(original, actual) {
					t.Fatal("normal JPEG was needlessly re-encoded")
				}
			} else {
				if prepared.Output.ContentType != "image/png" {
					t.Fatal("oriented JPEG was not normalized for consistent display/preview")
				}
				assertCorners(t, output, tc.width, tc.height, tc.corners)
			}
		})
	}
}

func jpegExifFixture(t *testing.T, orientation byte) string {
	t.Helper()
	source, err := heifFixtures.ReadFile("testdata/quadrants.png")
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	// Minimal little-endian TIFF IFD with one SHORT Orientation entry.
	exif := []byte{'E', 'x', 'i', 'f', 0, 0, 'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0, orientation, 0, 0, 0, 0, 0, 0, 0}
	var photo bytes.Buffer
	photo.Write(encoded.Bytes()[:2])
	photo.Write([]byte{255, 225})
	binary.Write(&photo, binary.BigEndian, uint16(len(exif)+2))
	photo.Write(exif)
	photo.Write(encoded.Bytes()[2:])
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, photo.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
