package ktf

import (
	"context"
	"encoding/binary"
	"errors"
)

var ktfWIPICInputModes = [...]string{"EN/S", "EN/L", "N123", "KO"}

func ktfWIPICInputGetSupportedModeCount(
	_ context.Context,
	_ *Runtime,
) (uint32, error) {
	return uint32(len(ktfWIPICInputModes)), nil
}

func ktfWIPICInputGetSupportedModes(
	_ context.Context,
	runtime *Runtime,
) (uint32, error) {
	if runtime.wipicInputModes != 0 {
		return runtime.wipicInputModes, nil
	}
	size := len(ktfWIPICInputModes) * 4
	for _, mode := range ktfWIPICInputModes {
		size += len(mode) + 1
	}
	address, err := runtime.allocateJavaHeapBytes(uint32(size), true)
	if err != nil {
		return 0, err
	}
	if address == 0 {
		return 0, errors.New("KTF guest heap exhausted allocating input modes")
	}

	encoded := make([]byte, size)
	cursor := len(ktfWIPICInputModes) * 4
	for index, mode := range ktfWIPICInputModes {
		binary.LittleEndian.PutUint32(
			encoded[index*4:],
			address+uint32(cursor),
		)
		copy(encoded[cursor:], mode)
		cursor += len(mode) + 1
	}
	if err := runtime.CPU.WriteMemory(address, encoded); err != nil {
		runtime.Heap.Release(address)
		return 0, err
	}
	runtime.wipicInputModes = address
	return address, nil
}
