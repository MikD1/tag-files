package media

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strconv"
)

// FFmpeg 8 exposes a grid's coded side data internally but ffprobe omits it.
// Read only bounded HEIF metadata for the selected item, never media payloads.
const maxHEIFMetadata = 4 * 1024 * 1024
const maxHEIFBoxes = 4096

type isoBox struct {
	kind string
	data []byte
}

func boxSize(header []byte, remaining uint64) (uint64, int, error) {
	if len(header) < 8 {
		return 0, 0, ErrInvalidMedia
	}
	size, prefix := uint64(binary.BigEndian.Uint32(header)), 8
	if size == 1 {
		if len(header) < 16 {
			return 0, 0, ErrInvalidMedia
		}
		size, prefix = binary.BigEndian.Uint64(header[8:]), 16
	} else if size == 0 {
		size = remaining
	}
	if size < uint64(prefix) || size > remaining {
		return 0, 0, ErrInvalidMedia
	}
	return size, prefix, nil
}

func isoChildren(data []byte, budget *int) ([]isoBox, error) {
	var result []isoBox
	for len(data) > 0 {
		*budget = *budget - 1
		if *budget < 0 {
			return nil, ErrResourceLimit
		}
		size, prefix, err := boxSize(data, uint64(len(data)))
		if err != nil {
			return nil, err
		}
		result = append(result, isoBox{kind: string(data[4:8]), data: data[prefix:size]})
		data = data[size:]
	}
	return result, nil
}

func heifMetadata(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	var metadata []byte
	for position, count := int64(0), 0; position < stat.Size(); count++ {
		if count >= maxHEIFBoxes {
			return nil, ErrResourceLimit
		}
		var header [16]byte
		if _, err := file.ReadAt(header[:8], position); err != nil {
			return nil, ErrInvalidMedia
		}
		if binary.BigEndian.Uint32(header[:]) == 1 {
			if _, err := file.ReadAt(header[8:], position+8); err != nil {
				return nil, ErrInvalidMedia
			}
		}
		size, prefix, err := boxSize(header[:], uint64(stat.Size()-position))
		if err != nil {
			return nil, err
		}
		if string(header[4:8]) == "meta" {
			if metadata != nil || size < uint64(prefix+4) {
				return nil, ErrInvalidMedia
			}
			if size > maxHEIFMetadata {
				return nil, ErrResourceLimit
			}
			metadata = make([]byte, int(size)-prefix)
			if _, err := file.ReadAt(metadata, position+int64(prefix)); err != nil {
				return nil, err
			}
			if binary.BigEndian.Uint32(metadata[:4]) != 0 {
				return nil, ErrUnsupportedCodec
			}
			metadata = metadata[4:]
		}
		position += int64(size)
	}
	if metadata == nil {
		return nil, ErrInvalidMedia
	}
	return metadata, nil
}

func heifProperties(path, item string) ([]isoBox, bool, error) {
	id, err := strconv.ParseUint(item, 0, 32)
	if err != nil || id == 0 {
		return nil, false, ErrInvalidMedia
	}
	metadata, err := heifMetadata(path)
	if err != nil {
		return nil, false, err
	}
	budget := maxHEIFBoxes
	children, err := isoChildren(metadata, &budget)
	if err != nil {
		return nil, false, err
	}
	var properties, associations []isoBox
	for _, child := range children {
		if child.kind != "iprp" {
			continue
		}
		entries, err := isoChildren(child.data, &budget)
		if err != nil {
			return nil, false, err
		}
		for _, entry := range entries {
			if entry.kind == "ipco" {
				if properties != nil {
					return nil, false, ErrInvalidMedia
				}
				properties, err = isoChildren(entry.data, &budget)
				if err != nil {
					return nil, false, err
				}
			} else if entry.kind == "ipma" {
				associations = append(associations, entry)
			}
		}
	}
	var selected []isoBox
	auxiliary := false
	for _, property := range properties {
		auxiliary = auxiliary || property.kind == "auxC"
	}
	for _, association := range associations {
		indices, err := heifAssociations(association.data, uint32(id))
		if err != nil {
			return nil, false, err
		}
		for _, index := range indices {
			if index == 0 {
				continue
			}
			if index > len(properties) {
				return nil, false, ErrInvalidMedia
			}
			selected = append(selected, properties[index-1])
		}
	}
	if len(selected) == 0 {
		return nil, false, ErrInvalidMedia
	}
	return selected, auxiliary, nil
}

func heifAssociations(data []byte, selected uint32) ([]int, error) {
	reader := bytes.NewReader(data)
	var flags, count uint32
	if binary.Read(reader, binary.BigEndian, &flags) != nil || binary.Read(reader, binary.BigEndian, &count) != nil {
		return nil, ErrInvalidMedia
	}
	version := flags >> 24
	if version > 1 || flags&0xffffff > 1 {
		return nil, ErrUnsupportedCodec
	}
	if count > maxHEIFBoxes {
		return nil, ErrResourceLimit
	}
	var indices []int
	for i := uint32(0); i < count; i++ {
		var id uint32
		if version == 0 {
			var short uint16
			if binary.Read(reader, binary.BigEndian, &short) != nil {
				return nil, ErrInvalidMedia
			}
			id = uint32(short)
		} else if binary.Read(reader, binary.BigEndian, &id) != nil {
			return nil, ErrInvalidMedia
		}
		var n uint8
		if binary.Read(reader, binary.BigEndian, &n) != nil {
			return nil, ErrInvalidMedia
		}
		for j := 0; j < int(n); j++ {
			var index uint16
			if flags&1 == 0 {
				var small uint8
				if binary.Read(reader, binary.BigEndian, &small) != nil {
					return nil, ErrInvalidMedia
				}
				index = uint16(small & 127)
			} else {
				if binary.Read(reader, binary.BigEndian, &index) != nil {
					return nil, ErrInvalidMedia
				}
				index &= 32767
			}
			if id == selected {
				indices = append(indices, int(index))
			}
		}
	}
	if position, _ := reader.Seek(0, io.SeekCurrent); position != int64(len(data)) {
		return nil, ErrInvalidMedia
	}
	return indices, nil
}

func gridTransforms(properties []isoBox) ([]string, int64, error) {
	var filters []string
	var rotation int64
	for _, property := range properties {
		switch property.kind {
		case "irot":
			if len(property.data) < 1 || property.data[0] > 3 {
				return nil, 0, ErrInvalidMedia
			}
			rotation = (rotation + int64(property.data[0])*90) % 360
			switch property.data[0] {
			case 1:
				filters = append(filters, "transpose=cclock")
			case 2:
				filters = append(filters, "hflip", "vflip")
			case 3:
				filters = append(filters, "transpose=clock")
			}
		case "imir":
			if len(property.data) < 1 || property.data[0] > 1 {
				return nil, 0, ErrInvalidMedia
			}
			if property.data[0] == 0 {
				filters = append(filters, "vflip")
			} else {
				filters = append(filters, "hflip")
			}
		case "clap", "iscl":
			return nil, 0, ErrUnsupportedCodec
		}
	}
	return filters, rotation, nil
}

func heifConversionProfile(properties []isoBox) error {
	for _, property := range properties {
		switch property.kind {
		case "colr":
			if len(property.data) < 4 {
				return ErrInvalidMedia
			}
			switch string(property.data[:4]) {
			case "prof", "rICC":
				// This conversion does not carry an ICC profile into PNG yet.
				return ErrUnsupportedCodec
			case "nclx":
				if len(property.data) < 11 {
					return ErrInvalidMedia
				}
				transfer := binary.BigEndian.Uint16(property.data[6:8])
				if transfer == 16 || transfer == 18 {
					return ErrUnsupportedCodec
				}
			default:
				return ErrUnsupportedCodec
			}
		case "pasp":
			if len(property.data) != 8 {
				return ErrInvalidMedia
			}
			if binary.BigEndian.Uint32(property.data[:4]) != binary.BigEndian.Uint32(property.data[4:]) {
				return ErrUnsupportedCodec
			}
		case "clap", "iscl":
			return ErrUnsupportedCodec
		}
	}
	return nil
}
