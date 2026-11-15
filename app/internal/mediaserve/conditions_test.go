package mediaserve

import (
	"math"
	"testing"
)

func TestRangeAmplificationCannotOverflowAndBypassTotalBound(t *testing.T) {
	rs, requested := ranges("bytes=0-9223372036854775806,0-9223372036854775806", math.MaxInt64)
	if requested || len(rs) != 0 {
		t.Fatal("overflow bypassed range amplification bound")
	}
}

func FuzzByteRangesStayInsideRepresentation(f *testing.F) {
	f.Add("bytes=0-4", int64(10))
	f.Add("bytes=-999999999999999999999999", int64(36))
	f.Add("bytes=0-9223372036854775806,0-9223372036854775806", int64(math.MaxInt64))
	f.Fuzz(func(t *testing.T, value string, size int64) {
		if size < 0 || len(value) > 8192 {
			t.Skip()
		}
		rs, _ := ranges(value, size)
		var total int64
		for _, r := range rs {
			if r.start < 0 || r.length <= 0 || r.length > size || r.start > size-r.length || r.length > size-total {
				t.Fatalf("invalid bounded range: %+v size=%d", r, size)
			}
			total += r.length
		}
	})
}
