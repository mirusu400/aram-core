package interpreter

import (
	"encoding/binary"
	"math/bits"

	"github.com/mirusu400/aram-core/cpu"
)

// A full private span makes a stack transfer infallible. On a cache miss,
// crossing, fault, physical access or traced execution, the instruction's
// scalar implementation retains its ordered accesses and partial-fault state.
func (b *Backend) privateThumbStackSpan(address uint32, count int, permission cpu.Permissions) ([]byte, bool) {
	if b.systemBus != nil || b.physicalAccess || b.tlb != nil || b.tracing() || address&3 != 0 || count == 0 {
		return nil, false
	}
	size := count * 4
	if uint64(address)+uint64(size) > 1<<32 {
		return nil, false
	}
	data, offset, _, hit := b.privateDataHit(address, size, permission)
	if !hit {
		return nil, false
	}
	if permission&cpu.PermissionWrite != 0 {
		// A stack mapped RWX is common. Writes into translated code must still
		// invalidate in scalar order, even when later stores overwrite it again.
		if b.nativeBlocks != nil ||
			(uint64(address) < uint64(b.jitCodeHi) && uint64(address)+uint64(size) > uint64(b.jitCodeLo) && b.hasJITCodePages(address, uint32(size))) {
			return nil, false
		}
	}
	return data[offset : offset+size], true
}

func (b *Backend) pushPrivateThumbRegisters(instruction uint16) bool {
	mask := uint32(instruction & 0xff)
	if instruction&(1<<8) != 0 {
		mask |= 1 << cpu.RegisterLR
	}
	count := bits.OnesCount32(mask)
	start := b.regs[cpu.RegisterSP] - uint32(count*4)
	data, hit := b.privateThumbStackSpan(b.blockTransferAddress(start), count, cpu.PermissionWrite)
	if !hit {
		return false
	}
	for offset := 0; mask != 0; offset += 4 {
		register := bits.TrailingZeros32(mask)
		binary.LittleEndian.PutUint32(data[offset:offset+4], b.regs[register])
		mask &= mask - 1
	}
	b.regs[cpu.RegisterSP] = start
	return true
}

func (b *Backend) popPrivateThumbRegisters(instruction uint16) bool {
	mask := uint32(instruction & 0xff)
	includePC := instruction&(1<<8) != 0
	if includePC {
		mask |= 1 << cpu.RegisterPC
	}
	count := bits.OnesCount32(mask)
	start := b.regs[cpu.RegisterSP]
	data, hit := b.privateThumbStackSpan(b.blockTransferAddress(start), count, cpu.PermissionRead)
	if !hit {
		return false
	}
	for offset := 0; mask != 0; offset += 4 {
		register := bits.TrailingZeros32(mask)
		value := binary.LittleEndian.Uint32(data[offset : offset+4])
		if register == int(cpu.RegisterPC) {
			b.branchExchange(value)
		} else {
			b.regs[register] = value
		}
		mask &= mask - 1
	}
	// Preserve the original base's low bits, even when the accesses were
	// aligned down with CP15.A clear. The complete span proved every read
	// infallible before any register or writeback was changed.
	b.regs[cpu.RegisterSP] = start + uint32(count*4)
	return true
}
