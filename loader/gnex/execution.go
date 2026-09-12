package gnex

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// ErrUnsupportedExecutionVariant distinguishes recognized but unmodeled layouts.
var ErrUnsupportedExecutionVariant = errors.New("gnex: unsupported execution image variant")

// ExecutionSymbol describes initial storage, not a validated symbol operation.
// Mutable storage is independent RAM. Otherwise Data aliases the owned Buffer.
type ExecutionSymbol struct {
	Type         byte // original descriptor byte, native runtime normalizes nonzero to 1
	BufferOffset int  // -1 for independent RAM, otherwise offset in Buffer
	Mutable      bool
	Data         []byte
}

// ExecutionMedia preserves the observed tag without assigning a codec meaning.
type ExecutionMedia struct {
	Type         byte // original descriptor byte, native runtime normalizes nonzero to 1
	BufferOffset int  // -1 for independent RAM, otherwise offset in Buffer
	Mutable      bool
	Tag          byte
	Data         []byte
}

// ExecutionImage owns a decoded normal-mode v2 storage layout. It does not
// initialize reserved symbols, decode media, deliver events, or start a game.
// Buffer and descriptor Data slices belong to the caller and are intentionally
// mutable. Modifying Buffer updates alias descriptors but not allocated RAM.
type ExecutionImage struct {
	Header   Header
	Buffer   []byte
	Entry    uint16
	Symbols  []ExecutionSymbol
	Media    []ExecutionMedia
	RAMBytes int
}

// DecodeExecutionImage models only the hash-qualified normal, unprefixed v2
// descriptor layout described in docs/gvm-execution-subset.md. Unlike native
// pointer arithmetic, it rejects malformed spans, incomplete records, file
// overreads and overlapping descriptor/data regions as emulator safety policy.
// No recognition or factory behavior changes merely by calling this function.
func DecodeExecutionImage(data []byte) (ExecutionImage, error) {
	bad := func(offset int, reason string) (ExecutionImage, error) {
		return ExecutionImage{}, formatError("SGS", int64(offset), reason)
	}
	if len(data) < 0x34 || len(data) > 128<<10 {
		return bad(0, "execution image size outside supported bounds")
	}
	if data[0] != 2 || data[2] != 12 || data[5] != 1 {
		return ExecutionImage{}, ErrUnsupportedExecutionVariant
	}
	header, err := ParseHeader(data)
	if err != nil {
		return bad(0, "execution header not recognized")
	}
	if header.PrefixOffset != 0 {
		return ExecutionImage{}, ErrUnsupportedExecutionVariant
	}
	word := func(offset int) int { return int(binary.LittleEndian.Uint16(data[offset : offset+2])) }
	ds, ps, dm, pm := word(0x2c), word(0x2e), word(0x30), word(0x32)
	if ds < 0x34 || ds > ps || ps > dm || dm > pm || pm > len(data) {
		return bad(0x2c, "invalid execution table ordering")
	}
	if (ps-ds)%4 != 0 || (pm-dm)%4 != 0 {
		return bad(0x2c, "incomplete execution descriptor")
	}
	entry := word(0x1c)
	if entry != 0 && entry >= len(data) {
		return bad(0x1c, "entry outside execution buffer")
	}
	ns, nm := (ps-ds)/4, (pm-dm)/4
	ram := 6*ns + 8*nm
	if ram > 0x4000 {
		return bad(0x2c, "execution descriptor RAM exceeds limit")
	}
	result := ExecutionImage{Header: header, Buffer: bytes.Clone(data), Entry: uint16(entry), Symbols: make([]ExecutionSymbol, 0, ns), Media: make([]ExecutionMedia, 0, nm)}
	cursor := ps
	for i := 0; i < ns; i++ {
		offset := ds + 4*i
		mutable := data[offset] != 0
		size := 2 * int(data[offset+1])
		initialized := !mutable || data[offset+2] != 0
		if mutable {
			ram += size
			if ram > 0x4000 {
				return bad(offset, "execution symbol RAM exceeds limit")
			}
		}
		if initialized && size > dm-cursor {
			return bad(offset, "execution symbol data exceeds span")
		}
		var storage []byte
		if mutable {
			storage = make([]byte, size)
			if initialized {
				copy(storage, data[cursor:cursor+size])
			}
		} else {
			storage = result.Buffer[cursor : cursor+size]
		}
		storageOffset := cursor
		if mutable {
			storageOffset = -1
		}
		if initialized {
			cursor += size
		}
		result.Symbols = append(result.Symbols, ExecutionSymbol{Type: data[offset], BufferOffset: storageOffset, Mutable: mutable, Data: storage})
	}
	cursor = pm
	for i := 0; i < nm; i++ {
		offset := dm + 4*i
		mutable := data[offset] != 0
		size := int(binary.LittleEndian.Uint16(data[offset+2 : offset+4]))
		if size > len(data)-cursor {
			return bad(offset, "execution media data exceeds span")
		}
		if mutable {
			ram += size
			if ram > 0x4000 {
				return bad(offset, "execution media RAM exceeds limit")
			}
		}
		storage := result.Buffer[cursor : cursor+size]
		if mutable {
			storage = bytes.Clone(storage)
		}
		storageOffset := cursor
		if mutable {
			storageOffset = -1
		}
		result.Media = append(result.Media, ExecutionMedia{Type: data[offset], BufferOffset: storageOffset, Mutable: mutable, Tag: data[offset+1], Data: storage})
		cursor += size
	}
	result.RAMBytes = ram
	return result, nil
}
