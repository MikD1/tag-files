package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
)

// poster-v1 is a JPEG within a 640px square without upscaling, on white for
// transparent photos. The primary remains authoritative and is never modified.
const PosterMaxEdge int64 = 640
const PosterMaxBytes int64 = 8 * 1024 * 1024

func (p *Processor) PosterOutputLimit() int64 {
	return min(p.config.MaxOutputBytes, PosterMaxBytes)
}

func posterShape(info Info) (int64, int64) {
	w, h := info.Width, info.Height
	if info.Rotation == 90 || info.Rotation == 270 {
		w, h = h, w
	}
	if w > PosterMaxEdge || h > PosterMaxEdge {
		if w >= h {
			h, w = max(1, h*PosterMaxEdge/w), PosterMaxEdge
		} else {
			w, h = max(1, w*PosterMaxEdge/h), PosterMaxEdge
		}
	}
	return w, h
}

func (p *Processor) PreparePoster(ctx context.Context, input, output string) (prepared Prepared, err error) {
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
	ctx, cancel := context.WithTimeout(ctx, min(p.config.ConvertTimeout, 5*time.Minute))
	defer cancel()
	file, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return prepared, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(output)
		return prepared, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(output)
		}
	}()
	w, h := posterShape(info)
	args := append([]string{"-hide_banner", "-v", "error", "-nostdin", "-y", "-xerror", "-filter_threads", "1", "-filter_complex_threads", "1", "-err_detect", "explode"}, p.inputArgs()...)
	if info.Kind == domain.Video && info.DurationTicks != nil && *info.DurationTicks > 0 {
		// Integer 100ns ticks avoid overflow/loss of precision in legacy times.
		ticks := *info.DurationTicks / 10
		args = append(args, "-ss", fmt.Sprintf("%d.%07d", ticks/10000000, ticks%10000000))
	}
	if info.grid != nil {
		args = append(args, "-threads", "1")
	}
	args = append(args, "-i", input)
	graph := ""
	selected := "[0:" + strconv.Itoa(info.VideoIndex) + "]"
	if info.grid != nil {
		graph = info.grid.filter() + ";"
		selected = "[primary]"
	}
	size := strconv.FormatInt(w, 10) + ":" + strconv.FormatInt(h, 10)
	// A seek between video frames leaves the first decoded frame at a positive
	// timestamp. Align it with the white canvas so frame zero contains the image.
	graph += selected + "setpts=PTS-STARTPTS,scale=" + size + ":flags=lanczos,setsar=1,format=rgba[scaled];color=c=white:s=" + strconv.FormatInt(w, 10) + "x" + strconv.FormatInt(h, 10) + ":r=1,format=rgba[white];[white][scaled]overlay=shortest=1:format=auto,format=yuvj444p[poster]"
	args = append(args, "-filter_complex", graph, "-map", "[poster]", "-frames:v", "1", "-an", "-sn", "-dn", "-map_metadata", "-1", "-metadata:s:v:0", "rotate=0", "-c:v", "mjpeg", "-q:v", "3", "-pix_fmt", "yuvj444p", "-threads", "1", "-fs", strconv.FormatInt(p.PosterOutputLimit()+1, 10), "-f", "image2", output)
	result, runErr := p.invoke(ctx, p.config.FFmpeg, args)
	if runErr != nil {
		if ctx.Err() != nil {
			return prepared, ctx.Err()
		}
		return prepared, fmt.Errorf("%w: %s", ErrInvalidMedia, result.Stderr)
	}
	file, err = os.Open(output)
	if err != nil {
		return prepared, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return prepared, err
	}
	if stat.Size() < 1 || stat.Size() > p.PosterOutputLimit() {
		return prepared, ErrResourceLimit
	}
	poster, err := p.Probe(ctx, output)
	if err != nil {
		return prepared, err
	}
	if poster.Kind != domain.Image || poster.ContentType != "image/jpeg" || poster.Width != w || poster.Height != h || poster.Rotation != 0 {
		return prepared, ErrInvalidMedia
	}
	if err := p.decode(ctx, output, poster); err != nil {
		return prepared, err
	}
	digest := sha256.New()
	n, err := copyMedia(ctx, digest, file, p.PosterOutputLimit()+1)
	if err != nil {
		return prepared, err
	}
	if n != stat.Size() {
		return prepared, ErrInvalidMedia
	}
	return Prepared{Path: output, Output: domain.MediaOutput{Kind: domain.Image, ContentType: "image/jpeg", Size: n, SHA256: hex.EncodeToString(digest.Sum(nil)), Width: w, Height: h}}, nil
}
