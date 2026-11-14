//go:build ignore

// Run from app/internal/media/testdata with: go run generate.go
// Uses libheif's standalone encoder only to create owned synthetic fixtures.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
)

func main() {
	quadrants("quadrants.png", 128, 96)
	encode("single.heic", "quadrants.png", nil)
	encode("grid.heic", "quadrants.png", []string{"--cut-tiles", "64"})
	transformGrid("grid-rotated.heic", "irot", 1)
	transformGrid("grid-mirrored.heic", "imir", 0)
	transformGrid("grid-horizontal.heic", "imir", 1)
	encode("single.avif", "quadrants.png", []string{"-A"})
	encode("grid.avif", "quadrants.png", []string{"-A", "--cut-tiles", "64"})
	encode("thumbnail.heic", "quadrants.png", []string{"--thumb", "32"})
	encode("hdr.heic", "quadrants.png", []string{"--transfer_characteristic", "16", "--colour_primaries", "9", "--matrix_coefficients", "9"})
	alpha := image.NewNRGBA(image.Rect(0, 0, 128, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 128; x++ {
			alpha.SetNRGBA(x, y, color.NRGBA{R: 240, G: 10, B: 10, A: uint8(x * 2)})
		}
	}
	f, err := os.Create("alpha.png")
	must(err)
	must(png.Encode(f, alpha))
	must(f.Close())
	encode("alpha.heic", "alpha.png", nil)
	must(os.Remove("alpha.png"))
	quadrants("large.png", 384, 256)
	encode("grid24.heic", "large.png", []string{"--cut-tiles", "64"})
	must(os.Remove("large.png"))
}

// Retain all lengths/iloc offsets. Replace the primary's colr property with
// a transform and replace other images' colr associations with their existing
// pixi property. Both decoders accept the redundant pixi association.
func transformGrid(name, kind string, value byte) {
	data, err := os.ReadFile("grid.heic")
	must(err)
	property := bytes.Index(data, []byte("colr"))
	if property < 4 || binary.BigEndian.Uint32(data[property-4:property]) != 19 {
		panic("unexpected owned fixture colr layout")
	}
	copy(data[property:property+4], kind)
	clear(data[property+4 : property+15])
	data[property+4] = value
	association := bytes.Index(data, []byte("ipma"))
	if association < 4 || binary.BigEndian.Uint32(data[association+4:association+8]) != 0 {
		panic("unexpected owned fixture ipma layout")
	}
	count := binary.BigEndian.Uint32(data[association+8 : association+12])
	position := association + 12
	for i := uint32(0); i < count; i++ {
		id := binary.BigEndian.Uint16(data[position : position+2])
		n := int(data[position+2])
		position += 3
		for j := 0; j < n; j++ {
			if id != 1 && data[position+j]&127 == 3 {
				data[position+j] = 5
			}
		}
		position += n
	}
	must(os.WriteFile(name, data, 0644))
}
func quadrants(name string, width, height int) {
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	colors := []color.NRGBA{{R: 240, G: 10, B: 10, A: 255}, {R: 10, G: 240, B: 10, A: 255}, {R: 10, G: 10, B: 240, A: 255}, {R: 240, G: 240, B: 10, A: 255}}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.SetNRGBA(x, y, colors[(y/(height/2))*2+x/(width/2)])
		}
	}
	file, err := os.Create(name)
	must(err)
	must(png.Encode(file, source))
	must(file.Close())
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func encode(name, source string, extra []string) {
	args := []string{"-q", "90", "--no-alpha", "-p", "x265:pools=1", "-p", "x265:frame-threads=1", "-p", "x265:log-level=error"}
	if name == "alpha.heic" {
		args = []string{"-q", "90", "-p", "x265:pools=1", "-p", "x265:frame-threads=1", "-p", "x265:log-level=error"}
	}
	if len(name) >= 5 && name[len(name)-5:] == ".avif" {
		args = []string{"-q", "90", "--no-alpha", "-p", "threads=1"}
	}
	args = append(args, extra...)
	args = append(args, "-o", name, source)
	command := exec.Command("heif-enc", args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	must(command.Run())
}
