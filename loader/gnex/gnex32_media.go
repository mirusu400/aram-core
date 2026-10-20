package gnex

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
)

var ErrGNEX32ReadOnlyMedia = errors.New("gnex: write to GNEX32 read-only media")
var ErrGNEX32MediaByteOutOfBounds = errors.New("gnex: GNEX32 media byte outside resource")

type gnex32MediaCell struct {
	flags            uint32
	data             []byte
	paletteOverrides map[uint8][3]byte
}

// GNEX32MediaMemory holds per-run copies of version-4 media resources. The two
// writable storage classes support ordinary byte writes and resizing; all
// source-image payloads remain immutable.
type GNEX32MediaMemory struct {
	resources []gnex32MediaCell
	total     uint64
}

type GNEX32MediaResourceState struct {
	Flags            uint32
	Data             []byte
	PaletteOverrides []GNEX32PaletteRGB
}

// GNEX32PaletteRGB records one local image-palette override. Selector-backed
// image kinds cannot represent arbitrary RGB values in their encoded bytes, so
// the runtime keeps those SetPaletteColorRGB changes beside the media payload.
type GNEX32PaletteRGB struct {
	Index            uint8
	Red, Green, Blue byte
}

// GNEX32MediaMemoryState includes writable strings and palette edits made to
// otherwise read-only image resources.
type GNEX32MediaMemoryState struct {
	Resources []GNEX32MediaResourceState
}

func (image GNEX32Image) NewMediaMemory() (*GNEX32MediaMemory, error) {
	state := &GNEX32MediaMemory{resources: make([]gnex32MediaCell, len(image.Media))}
	for index, resource := range image.Media {
		if !GNEX32MediaFlagsSupported(resource.Flags) {
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

// ReadImage returns an isolated payload and the local RGB overrides that must
// be applied while rendering it.
func (state *GNEX32MediaMemory) ReadImage(index int) ([]byte, []GNEX32PaletteRGB, error) {
	resource, err := state.cell(index)
	if err != nil {
		return nil, nil, err
	}
	return bytes.Clone(resource.data), snapshotGNEX32PaletteOverrides(resource.paletteOverrides), nil
}

// Replace writes one mutable resource, with a bounded total size.
func (state *GNEX32MediaMemory) Replace(index int, data []byte) error {
	resource, err := state.cell(index)
	if err != nil {
		return err
	}
	if !GNEX32MediaFlagsWritable(resource.flags) {
		return fmt.Errorf("%w: %d", ErrGNEX32ReadOnlyMedia, index)
	}
	newTotal := state.total - uint64(len(resource.data)) + uint64(len(data))
	if newTotal > MaxMemberSize {
		return fmt.Errorf("gnex: GNEX32 media exceeds size limit")
	}
	resource.data = bytes.Clone(data)
	resource.paletteOverrides = nil
	state.total = newTotal
	return nil
}

// Resize changes a writable resource's byte length, preserving its prefix and
// zero-filling any new bytes.
func (state *GNEX32MediaMemory) Resize(index, size int) error {
	resource, err := state.cell(index)
	if err != nil {
		return err
	}
	if !GNEX32MediaFlagsWritable(resource.flags) {
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
	resource.paletteOverrides = nil
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
	if !GNEX32MediaFlagsWritable(resource.flags) {
		return fmt.Errorf("%w: %d", ErrGNEX32ReadOnlyMedia, index)
	}
	if offset < 0 || offset >= len(resource.data) {
		return fmt.Errorf("%w: media %d offset %d", ErrGNEX32MediaByteOutOfBounds, index, offset)
	}
	resource.data[offset] = value
	resource.paletteOverrides = nil
	return nil
}

// SetPaletteColorRGB changes one local palette entry in a runtime image.
// GNEX permits this operation on constant images, so it bypasses the general
// media write flag while retaining the per-run copy of the source image.
// RGB-triplet images are updated in place. Selector-backed mono, 2bpp, and 4bpp
// images retain a per-run RGB override because their byte layout has no place
// to encode an arbitrary color. Invalid or non-palette images fail explicitly.
func (state *GNEX32MediaMemory) SetPaletteColorRGB(index, paletteIndex int, red, green, blue byte) bool {
	resource, err := state.cell(index)
	if err != nil || len(resource.data) == 0 {
		return false
	}
	data := resource.data
	switch data[0] {
	case 0x02, 0x05:
		if _, err := DecodeGNEX32MonoImage(data); err != nil || paletteIndex < 0 || paletteIndex >= 2 {
			return false
		}
	case 0x06, 0x07:
		image, err := DecodeGNEX32IndexedImage(data)
		if err != nil || paletteIndex < 0 || paletteIndex >= len(image.PaletteIndices) {
			return false
		}
	case 0x09, 0x0a, 0x0b:
		image, err := DecodeGNEX32IndexedImage(data)
		if err != nil || paletteIndex < 0 || paletteIndex >= len(image.PaletteTriplets)/3 {
			return false
		}
		offset := 7 + paletteIndex*3
		data[offset], data[offset+1], data[offset+2] = red, green, blue
		return true
	default:
		return false
	}
	if resource.paletteOverrides == nil {
		resource.paletteOverrides = make(map[uint8][3]byte)
	}
	resource.paletteOverrides[uint8(paletteIndex)] = [3]byte{red, green, blue}
	return true
}

func snapshotGNEX32PaletteOverrides(overrides map[uint8][3]byte) []GNEX32PaletteRGB {
	result := make([]GNEX32PaletteRGB, 0, len(overrides))
	for index, rgb := range overrides {
		result = append(result, GNEX32PaletteRGB{Index: index, Red: rgb[0], Green: rgb[1], Blue: rgb[2]})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result
}

func (state *GNEX32MediaMemory) Snapshot() GNEX32MediaMemoryState {
	result := GNEX32MediaMemoryState{Resources: make([]GNEX32MediaResourceState, len(state.resources))}
	for index, resource := range state.resources {
		result.Resources[index] = GNEX32MediaResourceState{
			Flags:            resource.flags,
			Data:             bytes.Clone(resource.data),
			PaletteOverrides: snapshotGNEX32PaletteOverrides(resource.paletteOverrides),
		}
	}
	return result
}

// Restore validates flags, resource count, and the aggregate byte limit before
// committing any media mutation.
func (state *GNEX32MediaMemory) Restore(saved GNEX32MediaMemoryState) error {
	if state == nil || len(saved.Resources) != len(state.resources) {
		return fmt.Errorf("gnex: GNEX32 media state shape mismatch")
	}
	replacement := make([]gnex32MediaCell, len(saved.Resources))
	var total uint64
	for index, resource := range saved.Resources {
		if resource.Flags != state.resources[index].flags {
			return fmt.Errorf("gnex: GNEX32 media %d state flags mismatch", index)
		}
		total += uint64(len(resource.Data))
		if total > MaxMemberSize {
			return fmt.Errorf("gnex: GNEX32 media state exceeds size limit")
		}
		cell := gnex32MediaCell{flags: resource.Flags, data: bytes.Clone(resource.Data)}
		for _, override := range resource.PaletteOverrides {
			if cell.paletteOverrides != nil {
				if _, duplicate := cell.paletteOverrides[override.Index]; duplicate {
					return fmt.Errorf("gnex: GNEX32 media %d has duplicate palette override %d", index, override.Index)
				}
			}
			if !gnex32PaletteOverrideIndexValid(cell.data, int(override.Index)) {
				return fmt.Errorf("gnex: GNEX32 media %d has invalid palette override %d", index, override.Index)
			}
			if cell.paletteOverrides == nil {
				cell.paletteOverrides = make(map[uint8][3]byte)
			}
			cell.paletteOverrides[override.Index] = [3]byte{override.Red, override.Green, override.Blue}
		}
		replacement[index] = cell
	}
	state.resources = replacement
	state.total = total
	return nil
}

func gnex32PaletteOverrideIndexValid(data []byte, paletteIndex int) bool {
	if paletteIndex < 0 || len(data) == 0 {
		return false
	}
	switch data[0] {
	case 0x02, 0x05:
		_, err := DecodeGNEX32MonoImage(data)
		return err == nil && paletteIndex < 2
	case 0x06, 0x07:
		image, err := DecodeGNEX32IndexedImage(data)
		return err == nil && paletteIndex < len(image.PaletteIndices)
	default:
		return false
	}
}
