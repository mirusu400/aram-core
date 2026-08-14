package guest

import (
	"fmt"

	"github.com/mirusu400/aram-core/cpu"
)

func WriteMemoryState(
	writer *StateWriter,
	backend cpu.Backend,
	address uint32,
	size uint32,
) error {
	buffer := make([]byte, min(uint32(64<<10), size))
	var offset uint32
	for offset < size {
		count := min(uint32(len(buffer)), size-offset)
		chunk := buffer[:count]
		if err := backend.ReadMemory(address+offset, chunk); err != nil {
			return fmt.Errorf(
				"save guest memory 0x%08x at +0x%x: %w",
				address,
				offset,
				err,
			)
		}
		writer.Write(chunk)
		if writer.Err != nil {
			return fmt.Errorf("save state at offset 0x%x: %w", writer.Offset, writer.Err)
		}
		offset += count
	}
	return nil
}
