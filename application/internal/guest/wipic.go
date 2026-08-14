package guest

import (
	"sort"

	shared "github.com/mirusu400/aram-core/runtime"
)

// WIPIReturn carries a WIPI-C native call's primary and secondary return
// words back to the guest.
type WIPIReturn struct {
	Low  uint32
	High uint32
}

const (
	WIPIShortBuffer    = int32(-2)
	WIPINoEntry        = int32(-4)
	WIPIExists         = int32(-6)
	WIPIInvalid        = int32(-8)
	WIPINoMemory       = int32(-12)
	WIPIBadFormat      = int32(-20)
	WIPIImageDone      = int32(1)
	WIPIImageFrameDone = int32(0)
)

func WIPIReturnCode(code int32) uint32 {
	return uint32(code)
}

func Abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func SortedStringKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func SortedUint32Keys[V any](values map[uint32]V) []uint32 {
	keys := make([]uint32, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func FontHeight(font uint32) int {
	switch font & 0x1f {
	case 8:
		return 10
	case 16:
		return 18
	default:
		return 14
	}
}

func PointInWIPIArc(dx, dy, start, sweep int) bool {
	return shared.PointInArc(int64(dx), int64(dy), int32(start), int32(sweep))
}

const MaxStateContext = 1 << 20
