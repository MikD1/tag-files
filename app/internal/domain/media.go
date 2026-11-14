package domain

import (
	"encoding/hex"
	"strings"
)

func mediaHash(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && strings.ToLower(value) == value
}

type MediaOutput struct {
	Kind                          MediaKind
	ContentType, SHA256           string
	Size, Width, Height, Rotation int64
	DurationTicks                 *int64
}

func PrimaryExtension(kind MediaKind, contentType string) string {
	if kind == Video && contentType == "video/mp4" {
		return "mp4"
	}
	if kind != Image {
		return ""
	}
	switch contentType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/avif":
		return "avif"
	}
	return ""
}
func (out MediaOutput) ValidatePrimary() error {
	if PrimaryExtension(out.Kind, out.ContentType) == "" || out.Size < 1 || out.Size > MaxSafeInteger || !mediaHash(out.SHA256) || out.Width < 1 || out.Width > MaxSafeInteger || out.Height < 1 || out.Height > MaxSafeInteger || out.Rotation < -MaxSafeInteger || out.Rotation > MaxSafeInteger || (out.DurationTicks != nil && (*out.DurationTicks < 0 || out.Kind != Video)) {
		return Invalid("output", "Invalid validated primary output descriptor")
	}
	return nil
}
