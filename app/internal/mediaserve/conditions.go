package mediaserve

import (
	"math"
	"net/http"
	"strings"
	"time"
)

type byteRange struct{ start, length int64 }

func (r byteRange) last() int64 { return r.start + r.length - 1 }
func decimal(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		v := int64(c - '0')
		if n > (math.MaxInt64-v)/10 {
			n = math.MaxInt64
		} else {
			n = n*10 + v
		}
	}
	return n, true
}
func ranges(value string, size int64) ([]byteRange, bool) {
	unit, set, ok := strings.Cut(value, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(unit), "bytes") {
		return nil, false
	}
	if len(value) > 4096 || strings.Count(set, ",") >= 16 {
		return nil, false
	}
	var out []byteRange
	var total int64
	for _, part := range strings.Split(set, ",") {
		first, last, ok := strings.Cut(strings.TrimSpace(part), "-")
		if !ok {
			return nil, true
		}
		var start, end int64
		if first == "" {
			n, ok := decimal(last)
			if !ok {
				return nil, true
			}
			if n == 0 || size == 0 {
				continue
			}
			n = min(n, size)
			start, end = size-n, size-1
		} else {
			n, ok := decimal(first)
			if !ok {
				return nil, true
			}
			start = n
			end = size - 1
			if last != "" {
				n, ok = decimal(last)
				if !ok || n < start {
					return nil, true
				}
				end = min(n, end)
			}
			if start >= size {
				continue
			}
		}
		r := byteRange{start: start, length: end - start + 1}
		if r.length > size-total {
			return nil, false
		}
		out = append(out, r)
		total += r.length
	}
	if total > size {
		return nil, false
	}
	return out, true
}
func takeTag(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	offset := 0
	if strings.HasPrefix(value, "W/") {
		offset = 2
	}
	if len(value) <= offset || value[offset] != '"' {
		return "", value, false
	}
	for i := offset + 1; i < len(value); i++ {
		if value[i] == '"' {
			return value[:i+1], strings.TrimSpace(value[i+1:]), true
		}
		if value[i] < 0x21 || value[i] == 0x7f {
			return "", value, false
		}
	}
	return "", value, false
}
func tagMatches(value, etag string, strong bool) (bool, error) {
	value = strings.TrimSpace(value)
	if len(value) > 4096 {
		return false, badHeader()
	}
	if value == "*" {
		return true, nil
	}
	matched := false
	for {
		tag, rest, ok := takeTag(value)
		if !ok {
			return false, badHeader()
		}
		if strong {
			matched = matched || (!strings.HasPrefix(tag, "W/") && tag == etag)
		} else {
			matched = matched || strings.TrimPrefix(tag, "W/") == strings.TrimPrefix(etag, "W/")
		}
		if rest == "" {
			return matched, nil
		}
		if rest[0] != ',' {
			return false, badHeader()
		}
		value = rest[1:]
	}
}
func conditions(r *http.Request, etag string, modified time.Time) (int, error) {
	if value := strings.Join(r.Header.Values("If-Match"), ","); value != "" {
		match, err := tagMatches(value, etag, true)
		if err != nil {
			return 0, err
		}
		if !match {
			return 412, nil
		}
	} else if value := r.Header.Get("If-Unmodified-Since"); value != "" && !modified.IsZero() {
		if date, err := http.ParseTime(value); err == nil && modified.After(date) {
			return 412, nil
		}
	}
	if value := strings.Join(r.Header.Values("If-None-Match"), ","); value != "" {
		match, err := tagMatches(value, etag, false)
		if err != nil {
			return 0, err
		}
		if match {
			return 304, nil
		}
	} else if value := r.Header.Get("If-Modified-Since"); value != "" && !modified.IsZero() {
		if date, err := http.ParseTime(value); err == nil && !modified.After(date) {
			return 304, nil
		}
	}
	return 200, nil
}
func allowRange(value, etag string, modified time.Time) bool {
	if value == "" {
		return true
	}
	tag, rest, ok := takeTag(value)
	if ok {
		return rest == "" && !strings.HasPrefix(tag, "W/") && tag == etag
	}
	date, err := http.ParseTime(value)
	return err == nil && !modified.IsZero() && modified.Equal(date)
}
