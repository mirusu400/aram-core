// Package cheat provides host-side, title-keyed memory search, patch, and
// freeze controls for an emulated machine.
package cheat

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
)

var (
	ErrAddressOutsideRegions     = errors.New("address is outside configured cheat regions")
	ErrReadOnlyRegion            = errors.New("cheat region is read-only")
	ErrScanNotStarted            = errors.New("memory scan has not been started")
	ErrTooManyResults            = errors.New("memory scan produced too many results")
	ErrScanLimitExceeded         = errors.New("memory scan byte limit exceeded")
	ErrUnexpectedOriginal        = errors.New("memory does not match expected original bytes")
	ErrCodeAlreadyExists         = errors.New("cheat code already exists")
	ErrCodeNotFound              = errors.New("cheat code was not found")
	ErrWrongTarget               = errors.New("cheat code targets a different application")
	ErrTargetIdentityUnavailable = errors.New("target SHA-256 is unavailable")
)

const (
	DefaultMaxScanBytes = uint64(128 << 20)
	DefaultMaxResults   = 2_000_000
	MaxCodeBytes        = 1 << 20
)

// Endian is the byte order used to encode scalar values.
type Endian uint8

const (
	EndianLittle Endian = iota
	EndianBig
)

func (e Endian) valid() bool {
	return e == EndianLittle || e == EndianBig
}

func (e Endian) byteOrder() binary.ByteOrder {
	if e == EndianBig {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

// ValueType describes a scalar stored in guest memory.
type ValueType uint8

const (
	TypeUint8 ValueType = iota
	TypeInt8
	TypeUint16
	TypeInt16
	TypeUint32
	TypeInt32
	TypeUint64
	TypeInt64
	TypeFloat32
	TypeFloat64
)

func (t ValueType) Valid() bool {
	return t >= TypeUint8 && t <= TypeFloat64
}

func (t ValueType) Size() int {
	switch t {
	case TypeUint8, TypeInt8:
		return 1
	case TypeUint16, TypeInt16:
		return 2
	case TypeUint32, TypeInt32, TypeFloat32:
		return 4
	case TypeUint64, TypeInt64, TypeFloat64:
		return 8
	default:
		return 0
	}
}

func (t ValueType) signed() bool {
	switch t {
	case TypeInt8, TypeInt16, TypeInt32, TypeInt64:
		return true
	default:
		return false
	}
}

func (t ValueType) floating() bool {
	return t == TypeFloat32 || t == TypeFloat64
}

// Value stores a typed scalar as its exact guest bit pattern.
type Value struct {
	Type ValueType `json:"type"`
	Bits uint64    `json:"bits"`
}

func U8(value uint8) Value {
	return Value{Type: TypeUint8, Bits: uint64(value)}
}

func I8(value int8) Value {
	return Value{Type: TypeInt8, Bits: uint64(uint8(value))}
}

func U16(value uint16) Value {
	return Value{Type: TypeUint16, Bits: uint64(value)}
}

func I16(value int16) Value {
	return Value{Type: TypeInt16, Bits: uint64(uint16(value))}
}

func U32(value uint32) Value {
	return Value{Type: TypeUint32, Bits: uint64(value)}
}

func I32(value int32) Value {
	return Value{Type: TypeInt32, Bits: uint64(uint32(value))}
}

func U64(value uint64) Value {
	return Value{Type: TypeUint64, Bits: value}
}

func I64(value int64) Value {
	return Value{Type: TypeInt64, Bits: uint64(value)}
}

func F32(value float32) Value {
	return Value{Type: TypeFloat32, Bits: uint64(math.Float32bits(value))}
}

func F64(value float64) Value {
	return Value{Type: TypeFloat64, Bits: math.Float64bits(value)}
}

func (v Value) Validate() error {
	if !v.Type.Valid() {
		return fmt.Errorf("invalid value type %d", v.Type)
	}
	size := v.Type.Size()
	if size < 8 && v.Bits >= uint64(1)<<(size*8) {
		return fmt.Errorf("%d-bit value contains out-of-range bits 0x%x", size*8, v.Bits)
	}
	return nil
}

func (v Value) Encode(endian Endian) ([]byte, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	if !endian.valid() {
		return nil, fmt.Errorf("invalid byte order %d", endian)
	}
	output := make([]byte, v.Type.Size())
	switch len(output) {
	case 1:
		output[0] = byte(v.Bits)
	case 2:
		endian.byteOrder().PutUint16(output, uint16(v.Bits))
	case 4:
		endian.byteOrder().PutUint32(output, uint32(v.Bits))
	case 8:
		endian.byteOrder().PutUint64(output, v.Bits)
	}
	return output, nil
}

func Decode(valueType ValueType, data []byte, endian Endian) (Value, error) {
	if !valueType.Valid() {
		return Value{}, fmt.Errorf("invalid value type %d", valueType)
	}
	if !endian.valid() {
		return Value{}, fmt.Errorf("invalid byte order %d", endian)
	}
	if len(data) != valueType.Size() {
		return Value{}, fmt.Errorf(
			"decode %d-byte value from %d bytes",
			valueType.Size(),
			len(data),
		)
	}
	var bits uint64
	switch len(data) {
	case 1:
		bits = uint64(data[0])
	case 2:
		bits = uint64(endian.byteOrder().Uint16(data))
	case 4:
		bits = uint64(endian.byteOrder().Uint32(data))
	case 8:
		bits = endian.byteOrder().Uint64(data)
	}
	return Value{Type: valueType, Bits: bits}, nil
}

// Comparison controls how a scan candidate is retained.
type Comparison uint8

const (
	CompareUnknown Comparison = iota
	CompareEqual
	CompareNotEqual
	CompareGreater
	CompareLess
	CompareChanged
	CompareUnchanged
	CompareIncreased
	CompareDecreased
)

func (c Comparison) Valid() bool {
	return c >= CompareUnknown && c <= CompareDecreased
}

func (c Comparison) needsTarget() bool {
	switch c {
	case CompareEqual, CompareNotEqual, CompareGreater, CompareLess:
		return true
	default:
		return false
	}
}

func (c Comparison) needsPrevious() bool {
	switch c {
	case CompareChanged, CompareUnchanged, CompareIncreased, CompareDecreased:
		return true
	default:
		return false
	}
}

// Region is a mapped guest address interval exposed to the cheat engine.
type Region struct {
	Name      string `json:"name"`
	Start     uint32 `json:"start"`
	Size      uint32 `json:"size"`
	Writable  bool   `json:"writable"`
	Scannable bool   `json:"scannable"`
}

func (r Region) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("cheat region name is empty")
	}
	if r.Size == 0 {
		return fmt.Errorf("cheat region %q is empty", r.Name)
	}
	if uint64(r.Start)+uint64(r.Size) > uint64(1)<<32 {
		return fmt.Errorf("cheat region %q overflows the guest address space", r.Name)
	}
	return nil
}

func (r Region) contains(address uint32, size int) bool {
	if size <= 0 {
		return false
	}
	start := uint64(address)
	end := start + uint64(size)
	return start >= uint64(r.Start) &&
		end <= uint64(r.Start)+uint64(r.Size) &&
		end <= uint64(1)<<32
}

// Options configures a cheat engine. Regions must describe every address that
// may be read or changed through the engine.
type Options struct {
	TargetSHA256 string
	ByteOrder    Endian
	Regions      []Region
	MaxScanBytes uint64
	MaxResults   int
}

// ScanRequest starts a new scan over the selected regions. When Regions is
// empty, every region marked Scannable is used. Alignment defaults to the
// scalar size.
type ScanRequest struct {
	Type       ValueType
	Comparison Comparison
	Value      *Value
	Alignment  uint32
	Regions    []string
}

// NextScanRequest filters the current scan result set.
type NextScanRequest struct {
	Comparison Comparison
	Value      *Value
}

// Match is a current memory scan result.
type Match struct {
	Address uint32 `json:"address"`
	Region  string `json:"region"`
	Value   Value  `json:"value"`
}
