// Package runtime implements deterministic, guest-neutral services shared by
// ARAM runtime adapters. It deliberately contains no guest addresses, Java
// references, carrier object layouts, or host device handles.
package runtime

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	DefaultMaxObjects     = uint32(4096)
	DefaultMaxEvents      = 1024
	DefaultMaxEventData   = 64 << 10
	DefaultMaxTraceEvents = 4096
	DefaultMaxTraceData   = 4 << 10
	DefaultMaxStreams     = 64
	DefaultFrameDuration  = 16 * time.Millisecond
)

var (
	ErrInvalidArgument = errors.New("runtime service invalid argument")
	ErrLimitExceeded   = errors.New("runtime service limit exceeded")
	ErrNotFound        = errors.New("runtime service object not found")
	ErrWrongKind       = errors.New("runtime service object has the wrong kind")
	ErrWrongOwner      = errors.New("runtime service object has the wrong owner")
	ErrStaleID         = errors.New("runtime service ID is stale")
	ErrReadOnly        = errors.New("runtime service object is read-only")
	ErrInvalidState    = errors.New("runtime service state is invalid")
)

// OwnerID identifies an adapter-owned service-object namespace. Zero is
// reserved for objects owned by the runtime coordinator itself.
type OwnerID uint32

const CoordinatorOwner OwnerID = 0

// ObjectKind is stable save-state data. New kinds may be added, but existing
// spellings must not be changed.
type ObjectKind string

const (
	KindSurface    ObjectKind = "surface"
	KindImage      ObjectKind = "image"
	KindFont       ObjectKind = "font"
	KindFile       ObjectKind = "file"
	KindRecordBase ObjectKind = "record-store"
	KindClip       ObjectKind = "clip"
	KindTimer      ObjectKind = "timer"
	KindSocket     ObjectKind = "socket"
	KindHTTP       ObjectKind = "http"
	KindSerial     ObjectKind = "serial"
)

func (k ObjectKind) Validate() error {
	if strings.TrimSpace(string(k)) == "" || len(k) > 64 {
		return fmt.Errorf("%w: invalid object kind %q", ErrInvalidArgument, k)
	}
	return nil
}

// ServiceID is a stable service identifier, not a guest pointer. The low
// 32 bits select a slot and the high 32 bits are its nonzero generation.
type ServiceID uint64

func makeServiceID(slot, generation uint32) ServiceID {
	return ServiceID(uint64(generation)<<32 | uint64(slot))
}

func (id ServiceID) Slot() uint32 {
	return uint32(id)
}

func (id ServiceID) Generation() uint32 {
	return uint32(uint64(id) >> 32)
}

func (id ServiceID) Valid() bool {
	return id.Slot() != 0 && id.Generation() != 0
}

func (id ServiceID) String() string {
	return fmt.Sprintf("%08x:%08x", id.Generation(), id.Slot())
}

// Rectangle uses an exclusive right and bottom edge.
type Rectangle struct {
	X      int32
	Y      int32
	Width  int32
	Height int32
}

func (r Rectangle) Valid() bool {
	return r.Width >= 0 && r.Height >= 0
}

func (r Rectangle) Empty() bool {
	return r.Width == 0 || r.Height == 0
}

func (r Rectangle) Right() int64 {
	return int64(r.X) + int64(r.Width)
}

func (r Rectangle) Bottom() int64 {
	return int64(r.Y) + int64(r.Height)
}

func (r Rectangle) Intersect(other Rectangle) Rectangle {
	left := max(int64(r.X), int64(other.X))
	top := max(int64(r.Y), int64(other.Y))
	right := min(r.Right(), other.Right())
	bottom := min(r.Bottom(), other.Bottom())
	if right <= left || bottom <= top ||
		left < math.MinInt32 || left > math.MaxInt32 ||
		top < math.MinInt32 || top > math.MaxInt32 {
		return Rectangle{}
	}
	return Rectangle{
		X:      int32(left),
		Y:      int32(top),
		Width:  int32(right - left),
		Height: int32(bottom - top),
	}
}

func (r Rectangle) Union(other Rectangle) Rectangle {
	if r.Empty() {
		return other
	}
	if other.Empty() {
		return r
	}
	left := min(int64(r.X), int64(other.X))
	top := min(int64(r.Y), int64(other.Y))
	right := max(r.Right(), other.Right())
	bottom := max(r.Bottom(), other.Bottom())
	if left < math.MinInt32 || left > math.MaxInt32 ||
		top < math.MinInt32 || top > math.MaxInt32 ||
		right-left > math.MaxInt32 || bottom-top > math.MaxInt32 {
		return Rectangle{}
	}
	return Rectangle{
		X:      int32(left),
		Y:      int32(top),
		Width:  int32(right - left),
		Height: int32(bottom - top),
	}
}

// Color is the canonical unassociated-alpha service color. Surface storage may
// use another declared pixel format.
type Color struct {
	R uint8
	G uint8
	B uint8
	A uint8
}

func RGB(red, green, blue uint8) Color {
	return Color{R: red, G: green, B: blue, A: 0xff}
}

func (c Color) RGBA32() uint32 {
	return uint32(c.R)<<24 | uint32(c.G)<<16 | uint32(c.B)<<8 | uint32(c.A)
}

func ColorFromRGBA32(value uint32) Color {
	return Color{
		R: uint8(value >> 24),
		G: uint8(value >> 16),
		B: uint8(value >> 8),
		A: uint8(value),
	}
}

func cloneBytes(data []byte) []byte {
	return append([]byte(nil), data...)
}
