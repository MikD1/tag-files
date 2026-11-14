package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHEIFMetadataBoundsRejectTruncationOversizeAndUnboundedBoxes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header []byte
		size   int64
		want   error
	}{
		{"short-header", []byte{0, 0, 0}, 3, ErrInvalidMedia},
		{"short-box", []byte{0, 0, 0, 4, 'm', 'e', 't', 'a'}, 8, ErrInvalidMedia},
		{"extended-truncated", []byte{0, 0, 0, 1, 'm', 'e', 't', 'a'}, 8, ErrInvalidMedia},
		{"extended-overflow", []byte{0, 0, 0, 1, 'm', 'e', 't', 'a', 255, 255, 255, 255, 255, 255, 255, 255}, 16, ErrInvalidMedia},
		{"large-metadata", []byte{0, 64, 0, 1, 'm', 'e', 't', 'a'}, maxHEIFMetadata + 1, ErrResourceLimit},
		{"many-boxes", bytes.Repeat([]byte{0, 0, 0, 8, 'f', 'r', 'e', 'e'}, maxHEIFBoxes+1), (maxHEIFBoxes + 1) * 8, ErrResourceLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "input")
			if err := os.WriteFile(path, tc.header, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(path, tc.size); err != nil {
				t.Fatal(err)
			}
			if _, err := heifMetadata(path); !errors.Is(err, tc.want) {
				t.Fatalf("metadata allocation/iteration bound bypassed: %v", err)
			}
		})
	}
}

func TestHEIFAssociationsSelectExactItemAndHandleWideIDs(t *testing.T) {
	// Version 1, 15-bit property indices, item 70000. The high bit means
	// essential; it is not part of the index. Item 1's property stays separate.
	data := []byte{1, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 1, 1, 0, 2, 0, 1, 17, 112, 2, 128, 3, 1, 4}
	indices, err := heifAssociations(data, 70000)
	if err != nil || len(indices) != 2 || indices[0] != 3 || indices[1] != 260 {
		t.Fatalf("wrong primary associations: %v %v", indices, err)
	}
	for length := 0; length < len(data); length++ {
		if _, err := heifAssociations(data[:length], 70000); err == nil {
			t.Fatalf("accepted truncated associations at %d", length)
		}
	}
}

func TestHEIFGridValidatesCoverageAndUsesGroupRelativeStreamIndices(t *testing.T) {
	p := &Processor{config: DefaultConfig()}
	for _, bad := range []string{"", "gap", "overlap", "crop", "missing-stream", "bad-index"} {
		t.Run(bad, func(t *testing.T) {
			var document probeDocument
			document.Streams = []probeStream{
				{Index: 2, Type: "video", Codec: "hevc", Width: 64, Height: 64},
				{Index: 7, Type: "video", Codec: "hevc", Width: 64, Height: 64},
			}
			var group probeGroup
			group.Count = 2
			group.Streams = append(group.Streams, struct {
				Index int `json:"index"`
			}{7}, struct {
				Index int `json:"index"`
			}{2})
			var raw probeGrid
			raw.Count, raw.CodedWidth, raw.CodedHeight, raw.Width, raw.Height = 2, 128, 64, 128, 64
			raw.Tiles = append(raw.Tiles, struct {
				Index int   `json:"stream_index"`
				X     int64 `json:"tile_horizontal_offset"`
				Y     int64 `json:"tile_vertical_offset"`
			}{0, 0, 0}, struct {
				Index int   `json:"stream_index"`
				X     int64 `json:"tile_horizontal_offset"`
				Y     int64 `json:"tile_vertical_offset"`
			}{1, 64, 0})
			switch bad {
			case "gap":
				raw.Count, raw.Tiles = 1, raw.Tiles[:1]
			case "overlap":
				raw.Tiles[1].X = 32
			case "crop":
				raw.X = 1
			case "missing-stream":
				group.Streams[1].Index = 9
			case "bad-index":
				raw.Tiles[0].Index = 7
			}
			group.Components = []probeGrid{raw}
			grid, stream, err := p.readGrid(document, group)
			if bad != "" {
				if err == nil {
					t.Fatal("invalid grid admitted")
				}
				return
			}
			if err != nil || stream.Index != 7 || len(grid.Tiles) != 2 || grid.Tiles[1].Index != 2 {
				t.Fatalf("group indices mistaken for global streams: %+v %+v %v", grid, stream, err)
			}
		})
	}
}

func FuzzHEIFBoxAndAssociationBounds(f *testing.F) {
	f.Add([]byte{0, 0, 0, 8, 'f', 'r', 'e', 'e'})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 1, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 16384 {
			t.Skip()
		}
		budget := maxHEIFBoxes
		_, _ = isoChildren(data, &budget)
		_, _ = heifAssociations(data, 1)
		if len(data) >= 16 {
			_, _, _ = boxSize(data[:16], binary.BigEndian.Uint64(data[8:16]))
		}
	})
}
