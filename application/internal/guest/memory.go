package guest

import (
	"encoding/binary"
	"errors"
	"time"

	"github.com/mirusu400/aram-core/cpu"
)

var ErrInvalidState = errors.New("invalid application machine state")

const WIPIFrameDuration = 16 * time.Millisecond

const DefaultProfileID = "wipi-1.2.1/generic"

const (
	DefaultStackBase = uint32(0x7ff00000)
	DefaultStackSize = uint32(0x00100000)

	ReturnSentinel = uint32(0x0110f000)

	HeapBase = uint32(0x10000000)
	HeapSize = uint32(0x02000000)
)

func ZeroMemory(backend cpu.Backend, address, size uint32) error {
	zeros := make([]byte, min(uint32(64<<10), size))
	var offset uint32
	for offset < size {
		count := min(uint32(len(zeros)), size-offset)
		if err := backend.WriteMemory(address+offset, zeros[:count]); err != nil {
			return err
		}
		offset += count
	}
	return nil
}

func Clamp(value, lower, upper int) int {
	return min(max(value, lower), upper)
}

func ReadU32(backend cpu.Backend, address uint32) (uint32, error) {
	var encoded [4]byte
	if err := backend.ReadMemory(address, encoded[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(encoded[:]), nil
}

func WriteU32(backend cpu.Backend, address, value uint32) error {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	return backend.WriteMemory(address, encoded[:])
}
