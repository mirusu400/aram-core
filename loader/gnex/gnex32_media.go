package gnex

import (
	"bytes"
	"errors"
	"fmt"
)

var ErrGNEX32ReadOnlyMedia = errors.New("gnex: write to GNEX32 read-only media")
var ErrGNEX32MediaByteOutOfBounds = errors.New("gnex: GNEX32 media byte outside resource")

type gnex32MediaCell struct {
	flags uint32
	data  []byte
}

// GNEX32MediaMemory holds per-run copies of version-4 media resources. Flags
// 1 and 0x101 are provisionally writable, based on their mostly empty records
// in the supplied image; the exact native mutation rules are unverified.
type GNEX32MediaMemory struct {
	resources []gnex32MediaCell
	total     uint64
}

func (image GNEX32Image) NewMediaMemory() (*GNEX32MediaMemory, error) {
	state := &GNEX32MediaMemory{resources: make([]gnex32MediaCell, len(image.Media))}
	for index, resource := range image.Media {
		switch resource.Flags {
		case 0, 1, 0x100, 0x101, 0x200:
		default:
			return nil, fmt.Errorf("gnex: GNEX32 media %d has unsupported flags %#x", index, resource.Flags)
		}
		state.total += uint64(len(resource.Data))
		if state.total > MaxMemberSize {
			return nil, fmt.Errorf("gnex: GNEX32 media initializers exceed size limit")
		}
		state.resources[index] = gnex32MediaCell{flags: resource.Flags, data: bytes.Clone(resource.Data)}
	}
	return state, nil
}

func (state *GNEX32MediaMemory) cell(index int) (*gnex32MediaCell, error) {
	if state == nil || index < 0 || index >= len(state.resources) {
		return nil, fmt.Errorf("gnex: GNEX32 media index %d out of range", index)
	}
	return &state.resources[index], nil
}

// Read returns a copy so callers cannot mutate an image or runtime resource.
func (state *GNEX32MediaMemory) Read(index int) ([]byte, uint32, error) {
	resource, err := state.cell(index)
	if err != nil {
		return nil, 0, err
	}
	return bytes.Clone(resource.data), resource.flags, nil
}

// Replace writes one candidate mutable resource, with a bounded total size.
func (state *GNEX32MediaMemory) Replace(index int, data []byte) error {
	resource, err := state.cell(index)
	if err != nil {
		return err
	}
	if resource.flags&1 == 0 {
		return fmt.Errorf("%w: %d", ErrGNEX32ReadOnlyMedia, index)
	}
	newTotal := state.total - uint64(len(resource.data)) + uint64(len(data))
	if newTotal > MaxMemberSize {
		return fmt.Errorf("gnex: GNEX32 media exceeds size limit")
	}
	resource.data = bytes.Clone(data)
	state.total = newTotal
	return nil
}

// Resize changes a writable resource's byte length, preserving its prefix and
// zero-filling any new bytes. Native GNEX allocation behavior is not yet known.
func (state *GNEX32MediaMemory) Resize(index, size int) error {
	resource, err := state.cell(index)
	if err != nil {
		return err
	}
	if resource.flags&1 == 0 {
		return fmt.Errorf("%w: %d", ErrGNEX32ReadOnlyMedia, index)
	}
	if size < 0 || uint64(size) > MaxMemberSize ||
		state.total-uint64(len(resource.data))+uint64(size) > MaxMemberSize {
		return fmt.Errorf("gnex: GNEX32 media exceeds size limit")
	}
	data := make([]byte, size)
	copy(data, resource.data)
	state.total = state.total - uint64(len(resource.data)) + uint64(size)
	resource.data = data
	return nil
}

// MediaByte accesses one byte without exposing the resource's backing storage.
func (state *GNEX32MediaMemory) MediaByte(index, offset int) (byte, error) {
	resource, err := state.cell(index)
	if err != nil {
		return 0, err
	}
	if offset < 0 || offset >= len(resource.data) {
		return 0, fmt.Errorf("%w: media %d offset %d", ErrGNEX32MediaByteOutOfBounds, index, offset)
	}
	return resource.data[offset], nil
}

// SetMediaByte changes one byte in a writable resource.
func (state *GNEX32MediaMemory) SetMediaByte(index, offset int, value byte) error {
	resource, err := state.cell(index)
	if err != nil {
		return err
	}
	if resource.flags&1 == 0 {
		return fmt.Errorf("%w: %d", ErrGNEX32ReadOnlyMedia, index)
	}
	if offset < 0 || offset >= len(resource.data) {
		return fmt.Errorf("%w: media %d offset %d", ErrGNEX32MediaByteOutOfBounds, index, offset)
	}
	resource.data[offset] = value
	return nil
}

// SetPaletteColorRGB changes one entry of an RGB palette in a runtime image.
// GNEX permits this operation on constant images, so it bypasses the general
// media write flag while retaining the per-run copy of the source image.
// Invalid images or palette indices are reported as a guest-level failure.
func (state *GNEX32MediaMemory) SetPaletteColorRGB(index, paletteIndex int, red, green, blue byte) bool {
	resource, err := state.cell(index)
	if err != nil || len(resource.data) < 7 {
		return false
	}
	data := resource.data
	if data[0] != 0x09 && data[0] != 0x0a && data[0] != 0x0b {
		return false
	}
	if _, err := DecodeGNEX32IndexedImage(data); err != nil {
		return false
	}
	if paletteIndex < 0 || paletteIndex >= int(data[6]) {
		return false
	}
	offset := 7 + paletteIndex*3
	data[offset], data[offset+1], data[offset+2] = red, green, blue
	return true
}
