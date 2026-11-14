package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mikd1/tag-files/app/internal/domain"
)

type Prepared struct {
	Path   string
	Output domain.MediaOutput
}

func (p *Processor) decode(ctx context.Context, path string, info Info) error {
	args := append([]string{"-hide_banner", "-v", "error", "-nostdin", "-xerror", "-max_error_rate", "0", "-filter_threads", "1", "-filter_complex_threads", "1", "-err_detect", "explode"}, p.inputArgs()...)
	if info.grid != nil {
		args = append(args, "-threads", "1")
	}
	args = append(args, "-i", path)
	if info.grid != nil {
		for _, tile := range info.grid.Tiles {
			args = append(args, "-map", "0:"+strconv.Itoa(tile.Index))
		}
	} else {
		args = append(args, "-map", "0:"+strconv.Itoa(info.VideoIndex))
	}
	if info.Kind == domain.Video && info.AudioIndex >= 0 {
		args = append(args, "-map", "0:"+strconv.Itoa(info.AudioIndex))
	}
	args = append(args, "-threads", strconv.Itoa(p.config.Threads), "-f", "null", "-")
	result, err := p.invoke(ctx, p.config.FFmpeg, args)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %s", ErrInvalidMedia, result.Stderr)
	}
	return nil
}
func (p *Processor) Prepare(ctx context.Context, input, output string) (prepared Prepared, err error) {
	input, err = localFile(input)
	if err != nil {
		return prepared, err
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return prepared, err
	}
	info, err := p.Probe(ctx, input)
	if err != nil {
		return prepared, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.config.ConvertTimeout)
	defer cancel()
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return prepared, err
	}
	if err := file.Close(); err != nil {
		os.Remove(output)
		return prepared, err
	}
	defer func() {
		if err != nil {
			os.Remove(output)
		}
	}()
	mime := info.ContentType
	passthrough := info.Kind == domain.Image && !info.bakeOrientation && (mime == "image/jpeg" || mime == "image/png" || mime == "image/webp" || mime == "image/gif" || mime == "image/avif")
	if passthrough {
		if err = p.decode(ctx, input, info); err != nil {
			return prepared, err
		}
		source, openErr := os.Open(input)
		if openErr != nil {
			return prepared, openErr
		}
		defer source.Close()
		destination, openErr := os.OpenFile(output, os.O_WRONLY, 0600)
		if openErr != nil {
			return prepared, openErr
		}
		n, copyErr := copyMedia(ctx, destination, source, p.config.MaxOutputBytes+1)
		closeErr := destination.Close()
		if copyErr != nil || closeErr != nil {
			return prepared, errors.Join(copyErr, closeErr)
		}
		if n > p.config.MaxOutputBytes {
			return prepared, ErrResourceLimit
		}
	} else {
		if info.Kind == domain.Video && (info.Width%2 != 0 || info.Height%2 != 0) {
			return prepared, ErrUnsupportedCodec
		}
		args := append([]string{"-hide_banner", "-v", "error", "-nostdin", "-y", "-xerror", "-max_error_rate", "0", "-filter_threads", "1", "-filter_complex_threads", "1", "-err_detect", "explode"}, p.inputArgs()...)
		if info.grid != nil {
			// Do not create a decoder thread pool for every tile.
			args = append(args, "-threads", "1")
		}
		args = append(args, "-i", input)
		if info.grid != nil {
			args = append(args, "-filter_complex", info.grid.filter(), "-map", "[primary]")
		} else {
			args = append(args, "-map", "0:"+strconv.Itoa(info.VideoIndex))
		}
		if info.Kind == domain.Video {
			if info.AudioIndex >= 0 {
				args = append(args, "-map", "0:"+strconv.Itoa(info.AudioIndex))
			}
			args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", "-metadata:s:v:0", "rotate=0", "-map_metadata", "-1", "-threads", strconv.Itoa(p.config.Threads), "-fs", strconv.FormatInt(p.config.MaxOutputBytes+1, 10), "-f", "mp4", output)
			mime = "video/mp4"
		} else {
			args = append(args, "-frames:v", "1", "-map_metadata", "-1", "-c:v", "png", "-threads", "1", "-fs", strconv.FormatInt(p.config.MaxOutputBytes+1, 10), "-f", "image2", output)
			mime = "image/png"
		}
		result, runErr := p.invoke(ctx, p.config.FFmpeg, args)
		if runErr != nil {
			if ctx.Err() != nil {
				return prepared, ctx.Err()
			}
			return prepared, fmt.Errorf("%w: %s", ErrInvalidMedia, result.Stderr)
		}
		converted, probeErr := p.Probe(ctx, output)
		if probeErr != nil {
			return prepared, probeErr
		}
		width, height := info.Width, info.Height
		if info.Rotation == 90 || info.Rotation == 270 {
			width, height = height, width
		}
		if converted.Width != width || converted.Height != height || converted.Rotation != 0 || converted.Kind != info.Kind {
			return prepared, ErrInvalidMedia
		}
		if info.Kind == domain.Video {
			if converted.VideoCodec != "h264" || (info.AudioIndex >= 0 && converted.AudioCodec != "aac") {
				return prepared, ErrInvalidMedia
			}
			if info.DurationTicks != nil && converted.DurationTicks != nil && *converted.DurationTicks+5000000 < *info.DurationTicks {
				return prepared, ErrResourceLimit
			}
		}
		if err = p.decode(ctx, output, converted); err != nil {
			return prepared, err
		}
		info = converted
	}
	handle, err := os.Open(output)
	if err != nil {
		return prepared, err
	}
	defer handle.Close()
	stat, err := handle.Stat()
	if err != nil {
		return prepared, err
	}
	if stat.Size() < 1 || stat.Size() > p.config.MaxOutputBytes {
		return prepared, ErrResourceLimit
	}
	digest := sha256.New()
	n, err := copyMedia(ctx, digest, handle, p.config.MaxOutputBytes+1)
	if err != nil {
		return prepared, err
	}
	if n != stat.Size() {
		return prepared, ErrInvalidMedia
	}
	descriptor := domain.MediaOutput{Kind: info.Kind, ContentType: mime, Size: n, SHA256: hex.EncodeToString(digest.Sum(nil)), Width: info.Width, Height: info.Height, Rotation: info.Rotation, DurationTicks: info.DurationTicks}
	if err = descriptor.ValidatePrimary(); err != nil {
		return prepared, err
	}
	return Prepared{Path: output, Output: descriptor}, nil
}
