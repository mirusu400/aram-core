package system

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mirusu400/aram-core/cpu"
)

const MaxHandoffSeedBytes = 1 << 20

type RegisterSeed struct {
	Register uint32
	Value    uint32
}

type MemorySeed struct {
	Address uint32
	Bytes   []byte
}

// BootHandoff describes only the architectural effects owned by an
// unavailable earlier boot stage. It is named and retained as an explicit HLE
// boundary rather than being mistaken for execution of that missing ROM.
type BootHandoff struct {
	ID        string
	Entry     uint32
	Mode      cpu.Mode
	Registers []RegisterSeed
	Memory    []MemorySeed
}

func (h BootHandoff) Validate() error {
	if strings.TrimSpace(h.ID) == "" || len(h.ID) > 255 || strings.IndexByte(h.ID, 0) >= 0 ||
		!h.Mode.Valid() || h.Mode == cpu.ModeARM && h.Entry&3 != 0 ||
		h.Mode == cpu.ModeThumb && h.Entry&1 != 0 {
		return fmt.Errorf("invalid boot handoff identity or entry")
	}
	registers := make(map[uint32]struct{}, len(h.Registers))
	for _, seed := range h.Registers {
		if seed.Register > cpu.RegisterCPSR || seed.Register == cpu.RegisterPC {
			return fmt.Errorf("boot handoff %q has invalid register %d", h.ID, seed.Register)
		}
		if _, duplicate := registers[seed.Register]; duplicate {
			return fmt.Errorf("boot handoff %q repeats register %d", h.ID, seed.Register)
		}
		registers[seed.Register] = struct{}{}
	}
	memory := append([]MemorySeed(nil), h.Memory...)
	var total uint64
	for _, seed := range memory {
		if len(seed.Bytes) == 0 || uint64(seed.Address)+uint64(len(seed.Bytes)) > 1<<32 {
			return fmt.Errorf("boot handoff %q has invalid memory seed at 0x%x", h.ID, seed.Address)
		}
		total += uint64(len(seed.Bytes))
		if total > MaxHandoffSeedBytes {
			return fmt.Errorf("boot handoff %q exceeds seed-byte limit", h.ID)
		}
	}
	sort.Slice(memory, func(left, right int) bool { return memory[left].Address < memory[right].Address })
	for index := 1; index < len(memory); index++ {
		previousEnd := uint64(memory[index-1].Address) + uint64(len(memory[index-1].Bytes))
		if previousEnd > uint64(memory[index].Address) {
			return fmt.Errorf("boot handoff %q has overlapping memory seeds", h.ID)
		}
	}
	return nil
}

func (h BootHandoff) Apply(bus *Bus, backend cpu.Backend) error {
	if bus == nil || backend == nil {
		return fmt.Errorf("apply boot handoff %q: nil bus or backend", h.ID)
	}
	if err := h.Validate(); err != nil {
		return err
	}
	for _, seed := range h.Memory {
		for offset := 0; offset < len(seed.Bytes); {
			address := seed.Address + uint32(offset)
			width := 1
			if address&3 == 0 && len(seed.Bytes)-offset >= 4 {
				width = 4
			} else if address&1 == 0 && len(seed.Bytes)-offset >= 2 {
				width = 2
			}
			if err := bus.Write(address, seed.Bytes[offset:offset+width], cpu.PermissionWrite); err != nil {
				return fmt.Errorf("apply boot handoff %q memory: %w", h.ID, err)
			}
			offset += width
		}
	}
	for _, seed := range h.Registers {
		if err := backend.WriteRegister(seed.Register, seed.Value); err != nil {
			return fmt.Errorf("apply boot handoff %q register %d: %w", h.ID, seed.Register, err)
		}
	}
	return nil
}
