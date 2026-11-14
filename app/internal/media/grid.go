package media

import (
	"fmt"
	"strconv"
	"strings"
)

type probeGroup struct {
	ID          string           `json:"id"`
	Type        string           `json:"type"`
	Count       int              `json:"nb_streams"`
	Disposition probeDisposition `json:"disposition"`
	Components  []probeGrid      `json:"components"`
	Streams     []struct {
		Index int `json:"index"`
	} `json:"streams"`
}
type probeGrid struct {
	Count       int   `json:"nb_tiles"`
	CodedWidth  int64 `json:"coded_width"`
	CodedHeight int64 `json:"coded_height"`
	X           int64 `json:"horizontal_offset"`
	Y           int64 `json:"vertical_offset"`
	Width       int64 `json:"width"`
	Height      int64 `json:"height"`
	Tiles       []struct {
		Index int   `json:"stream_index"`
		X     int64 `json:"tile_horizontal_offset"`
		Y     int64 `json:"tile_vertical_offset"`
	} `json:"subcomponents"`
}
type tile struct {
	Index               int
	X, Y, Width, Height int64
}
type tileGrid struct {
	X, Y, Width, Height int64
	Tiles               []tile
	Transforms          []string
}

func (p *Processor) validShape(width, height int64) bool {
	return width > 0 && height > 0 && width <= 32768 && height <= 32768 && width <= p.config.MaxPixels/height
}

// ffprobe's tile stream_index is relative to group.Streams, not the input's
// global stream index. Keep that indirection even for a one-group fixture.
func (p *Processor) readGrid(document probeDocument, group probeGroup) (*tileGrid, *probeStream, error) {
	if group.Count != len(group.Streams) || group.Count < 1 || group.Count > maxMediaStreams || len(group.Components) != 1 {
		return nil, nil, ErrInvalidMedia
	}
	raw := group.Components[0]
	if raw.Count < 1 || raw.Count > maxMediaStreams || raw.Count != len(raw.Tiles) {
		return nil, nil, ErrInvalidMedia
	}
	if !p.validShape(raw.Width, raw.Height) || !p.validShape(raw.CodedWidth, raw.CodedHeight) {
		return nil, nil, ErrResourceLimit
	}
	if raw.X < 0 || raw.Y < 0 || raw.Width > raw.CodedWidth || raw.Height > raw.CodedHeight || raw.X > raw.CodedWidth-raw.Width || raw.Y > raw.CodedHeight-raw.Height {
		return nil, nil, ErrInvalidMedia
	}
	streams := make(map[int]*probeStream, len(document.Streams))
	for i := range document.Streams {
		s := &document.Streams[i]
		if _, exists := streams[s.Index]; exists || s.Index < 0 {
			return nil, nil, ErrInvalidMedia
		}
		streams[s.Index] = s
	}
	grid := &tileGrid{X: raw.X, Y: raw.Y, Width: raw.Width, Height: raw.Height}
	var first *probeStream
	var area int64
	for _, offset := range raw.Tiles {
		if offset.Index < 0 || offset.Index >= len(group.Streams) {
			return nil, nil, ErrInvalidMedia
		}
		stream := streams[group.Streams[offset.Index].Index]
		if stream == nil || stream.Type != "video" || stream.Disposition.Attached != 0 {
			return nil, nil, ErrInvalidMedia
		}
		if stream.Codec != "hevc" && stream.Codec != "av1" {
			return nil, nil, ErrUnsupportedCodec
		}
		if first == nil {
			first = stream
		}
		if !p.validShape(stream.Width, stream.Height) {
			return nil, nil, ErrResourceLimit
		}
		if stream.SAR != "" && stream.SAR != "1:1" || stream.Transfer == "smpte2084" || stream.Transfer == "arib-std-b67" {
			return nil, nil, ErrUnsupportedCodec
		}
		for _, side := range stream.SideData {
			if side.Rotation != "" && side.Rotation != "0" {
				return nil, nil, ErrUnsupportedCodec
			}
		}
		if offset.X < 0 || offset.Y < 0 || stream.Width > raw.CodedWidth || stream.Height > raw.CodedHeight || offset.X > raw.CodedWidth-stream.Width || offset.Y > raw.CodedHeight-stream.Height {
			return nil, nil, ErrInvalidMedia
		}
		next := tile{Index: stream.Index, X: offset.X, Y: offset.Y, Width: stream.Width, Height: stream.Height}
		for _, previous := range grid.Tiles {
			if next.X < previous.X+previous.Width && previous.X < next.X+next.Width && next.Y < previous.Y+previous.Height && previous.Y < next.Y+next.Height {
				// Overlay images need explicit alpha/order/background handling.
				return nil, nil, ErrUnsupportedCodec
			}
		}
		area += stream.Width * stream.Height
		grid.Tiles = append(grid.Tiles, next)
	}
	// Contained, nonoverlapping rectangles with equal total area cover the
	// canvas. This prevents xstack from inventing pixels for an incomplete grid.
	if area != raw.CodedWidth*raw.CodedHeight {
		return nil, nil, ErrInvalidMedia
	}
	return grid, first, nil
}

func (g *tileGrid) filter() string {
	var graph strings.Builder
	positions := make([]string, 0, len(g.Tiles))
	for _, tile := range g.Tiles {
		fmt.Fprintf(&graph, "[0:%d]", tile.Index)
		positions = append(positions, strconv.FormatInt(tile.X, 10)+"_"+strconv.FormatInt(tile.Y, 10))
	}
	if len(g.Tiles) > 1 {
		fmt.Fprintf(&graph, "xstack=inputs=%d:layout=%s,", len(g.Tiles), strings.Join(positions, "|"))
	}
	fmt.Fprintf(&graph, "crop=%d:%d:%d:%d:exact=1", g.Width, g.Height, g.X, g.Y)
	for _, transform := range g.Transforms {
		graph.WriteString("," + transform)
	}
	graph.WriteString("[primary]")
	return graph.String()
}
