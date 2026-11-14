// Package media probes and validates local scratch media before publication.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/process"
)

var ErrUnsupportedMedia = errors.New("unsupported media type")
var ErrUnsupportedCodec = errors.New("unsupported media codec or display profile")
var ErrInvalidMedia = errors.New("media did not pass full decoding")
var ErrResourceLimit = errors.New("media exceeds configured resource limits")

const inputFormats = "jpeg_pipe,png_pipe,webp_pipe,gif,gif_pipe,apng,bmp_pipe,tiff_pipe,mov,matroska,avi,mpegts"
const inputCodecs = "mjpeg,png,webp,gif,apng,bmp,tiff,h264,hevc,mpeg4,mpeg2video,vp8,vp9,av1,libdav1d,libaom-av1,aac,aac_latm,mp3,mp3float,opus,vorbis,ac3,eac3,flac,alac,pcm_s16le,pcm_s24le,pcm_s32le,pcm_f32le,pcm_f64le"

type Config struct {
	FFmpeg, FFprobe              string
	Threads                      int
	MaxPixels, MaxOutputBytes    int64
	ProbeTimeout, ConvertTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{FFmpeg: "ffmpeg", FFprobe: "ffprobe", Threads: 2, MaxPixels: 64000000, MaxOutputBytes: 20 * 1024 * 1024 * 1024, ProbeTimeout: 30 * time.Second, ConvertTimeout: 6 * time.Hour}
}

type Processor struct{ config Config }

func New(config Config) (*Processor, error) {
	if config.Threads < 1 || config.Threads > 16 || config.MaxPixels < 1 || config.MaxPixels > 256000000 || config.MaxOutputBytes < 1 || config.MaxOutputBytes > domain.MaxSafeInteger || config.ProbeTimeout <= 0 || config.ConvertTimeout <= 0 {
		return nil, domain.Invalid("media", "Invalid bounded media profile")
	}
	var err error
	config.FFmpeg, err = exec.LookPath(config.FFmpeg)
	if err != nil {
		return nil, err
	}
	config.FFprobe, err = exec.LookPath(config.FFprobe)
	if err != nil {
		return nil, err
	}
	return &Processor{config: config}, nil
}

type Info struct {
	Kind                                                   domain.MediaKind
	ContentType, VideoCodec, AudioCodec, SampleAspectRatio string
	Width, Height, Rotation                                int64
	DurationTicks                                          *int64
	VideoIndex, AudioIndex                                 int
}
type probeStream struct {
	Index         int    `json:"index"`
	Codec         string `json:"codec_name"`
	Type          string `json:"codec_type"`
	Width, Height int64
	DurationTS    json.Number `json:"duration_ts"`
	TimeBase      string      `json:"time_base"`
	Duration      string      `json:"duration"`
	SAR           string      `json:"sample_aspect_ratio"`
	Transfer      string      `json:"color_transfer"`
	Disposition   struct {
		Default  int `json:"default"`
		Attached int `json:"attached_pic"`
	} `json:"disposition"`
	SideData []struct {
		Rotation json.Number `json:"rotation"`
	} `json:"side_data_list"`
}
type probeDocument struct {
	Streams []probeStream `json:"streams"`
	Format  struct {
		Name     string            `json:"format_name"`
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

func durationTicks(value, base string) (*int64, error) {
	if value == "" || value == "N/A" {
		return nil, nil
	}
	if len(value) > 64 || len(base) > 64 {
		return nil, ErrInvalidMedia
	}
	// ffprobe emits decimal seconds or an integer timestamp/rational base.
	// Reject exponents and alternative big.Rat syntaxes before allocation.
	decimal := func(text string, fraction bool) bool {
		digits, dots := 0, 0
		for _, ch := range text {
			if ch >= '0' && ch <= '9' {
				digits++
			} else if ch == '.' && fraction {
				dots++
			} else {
				return false
			}
		}
		return digits > 0 && dots <= 1
	}
	if !decimal(value, base == "") {
		return nil, ErrInvalidMedia
	}
	if base != "" {
		parts := strings.Split(base, "/")
		if len(parts) != 2 || !decimal(parts[0], false) || !decimal(parts[1], false) {
			return nil, ErrInvalidMedia
		}
	}
	number, ok := new(big.Rat).SetString(value)
	if !ok || number.Sign() < 0 {
		return nil, ErrInvalidMedia
	}
	if base != "" {
		ratio, ok := new(big.Rat).SetString(base)
		if !ok || ratio.Sign() <= 0 {
			return nil, ErrInvalidMedia
		}
		number.Mul(number, ratio)
	}
	number.Mul(number, big.NewRat(10000000, 1))
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(number.Num(), number.Denom(), remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(number.Denom()) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	if !whole.IsInt64() {
		return nil, ErrInvalidMedia
	}
	ticks := whole.Int64()
	return &ticks, nil
}
func localFile(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 {
		return "", ErrInvalidMedia
	}
	return path, nil
}
func (p *Processor) inputArgs() []string {
	return []string{"-max_alloc", "268435456", "-threads", strconv.Itoa(p.config.Threads), "-max_pixels", strconv.FormatInt(p.config.MaxPixels, 10), "-protocol_whitelist", "file", "-format_whitelist", inputFormats, "-codec_whitelist", inputCodecs, "-probesize", "8388608", "-analyzeduration", "5000000", "-max_streams", "16"}
}
func (p *Processor) invoke(ctx context.Context, path string, args []string) (process.Result, error) {
	return process.Run(ctx, process.Command{Path: path, Args: args, Env: []string{"AV_LOG_FORCE_NOCOLOR=1", "LC_ALL=C"}}, process.DefaultLimits())
}
func (p *Processor) Probe(ctx context.Context, path string) (Info, error) {
	path, err := localFile(path)
	if err != nil {
		return Info{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.config.ProbeTimeout)
	defer cancel()
	args := append([]string{"-v", "error"}, p.inputArgs()...)
	args = append(args, "-show_entries", "format=format_name,duration:format_tags=major_brand:stream=index,codec_name,codec_type,width,height,duration_ts,time_base,duration,sample_aspect_ratio,color_transfer:stream_disposition=default,attached_pic:stream_side_data=rotation", "-of", "json", path)
	result, err := p.invoke(ctx, p.config.FFprobe, args)
	if err != nil {
		if ctx.Err() != nil {
			return Info{}, ctx.Err()
		}
		cause := ErrUnsupportedMedia
		if strings.Contains(string(result.Stderr), "Codec") && strings.Contains(string(result.Stderr), "whitelist") {
			cause = ErrUnsupportedCodec
		}
		if result.StdoutTruncated || strings.Contains(string(result.Stderr), "max_pixels") {
			cause = ErrResourceLimit
		}
		return Info{}, fmt.Errorf("%w: %s", cause, result.Stderr)
	}
	var document probeDocument
	if json.Unmarshal(result.Stdout, &document) != nil || len(document.Streams) > 16 {
		return Info{}, ErrInvalidMedia
	}
	var video, audio *probeStream
	for i := range document.Streams {
		stream := &document.Streams[i]
		if stream.Type == "video" && stream.Disposition.Attached == 0 && (video == nil || (stream.Disposition.Default != 0 && video.Disposition.Default == 0)) {
			video = stream
		}
		if stream.Type == "audio" && (audio == nil || (stream.Disposition.Default != 0 && audio.Disposition.Default == 0)) {
			audio = stream
		}
	}
	if video == nil {
		return Info{}, ErrUnsupportedMedia
	}
	if video.Width < 1 || video.Height < 1 || video.Width > 32768 || video.Height > 32768 || video.Width > p.config.MaxPixels/video.Height {
		return Info{}, ErrResourceLimit
	}
	info := Info{Kind: domain.Video, ContentType: "video/mp4", VideoCodec: video.Codec, Width: video.Width, Height: video.Height, VideoIndex: video.Index, AudioIndex: -1, SampleAspectRatio: video.SAR}
	imageTypes := map[string]string{"jpeg_pipe": "image/jpeg", "png_pipe": "image/png", "webp_pipe": "image/webp", "gif": "image/gif", "gif_pipe": "image/gif", "apng": "image/apng", "bmp_pipe": "image/bmp", "tiff_pipe": "image/tiff"}
	if mime := imageTypes[document.Format.Name]; mime != "" {
		info.Kind = domain.Image
		info.ContentType = mime
	}
	switch strings.TrimSpace(document.Format.Tags["major_brand"]) {
	case "avif", "avis":
		info.Kind = domain.Image
		info.ContentType = "image/avif"
	case "heic", "heix", "hevc", "hevx", "mif1", "msf1":
		info.Kind = domain.Image
		info.ContentType = "image/heic"
	}
	for _, side := range video.SideData {
		if side.Rotation != "" {
			angle, err := strconv.ParseInt(string(side.Rotation), 10, 64)
			if err != nil || angle%90 != 0 {
				return Info{}, ErrUnsupportedCodec
			}
			info.Rotation = ((angle % 360) + 360) % 360
		}
	}
	if audio != nil {
		info.AudioCodec = audio.Codec
		info.AudioIndex = audio.Index
	}
	if info.Kind == domain.Video {
		if video.Transfer == "smpte2084" || video.Transfer == "arib-std-b67" {
			return Info{}, ErrUnsupportedCodec
		}
		if video.DurationTS != "" {
			info.DurationTicks, err = durationTicks(string(video.DurationTS), video.TimeBase)
		} else if video.Duration != "" {
			info.DurationTicks, err = durationTicks(video.Duration, "")
		} else {
			info.DurationTicks, err = durationTicks(document.Format.Duration, "")
		}
		if err != nil {
			return Info{}, err
		}
	}
	return info, nil
}
