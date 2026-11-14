// Package domain contains shared value boundaries, independent of transport.
package domain

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

var (
	ErrNotFound     = errors.New("resource not found")
	ErrStaleVersion = errors.New("resource version has changed")
)

type ValidationError struct{ Field, Code, Message string }

func (e *ValidationError) Error() string { return e.Message }

func Invalid(field, message string) error {
	return &ValidationError{Field: field, Code: "validation_failed", Message: message}
}

type MediaKind string

const (
	Unknown MediaKind = "Unknown"
	Text    MediaKind = "Text"
	Image   MediaKind = "Image"
	Video   MediaKind = "Video"
	Audio   MediaKind = "Audio"
)

func (k MediaKind) Validate() error {
	switch k {
	case Unknown, Text, Image, Video, Audio:
		return nil
	}
	return Invalid("mediaKind", "Invalid media kind")
}

func ValidateName(name string) error {
	n := 0
	for _, r := range name {
		n++
		if r > 0xffff {
			n++
		}
	}
	if !utf8.ValidString(name) || n < 1 || n > 200 {
		return Invalid("name", "Name must contain 1 to 200 characters")
	}
	return nil
}

const MaxSafeInteger int64 = 9007199254740991

func ValidateID(id int64) error {
	if id < 1 || id > MaxSafeInteger {
		return Invalid("id", fmt.Sprintf("ID must be between 1 and %d", MaxSafeInteger))
	}
	return nil
}

func ValidateUUID(id string) error {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		return Invalid("eventId", "Expected a UUID")
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil || len(decoded) != 16 {
		return Invalid("eventId", "Expected a UUID")
	}
	return nil
}
