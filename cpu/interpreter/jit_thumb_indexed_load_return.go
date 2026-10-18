package interpreter

import (
	"encoding/binary"
	"math/bits"

	"github.com/mirusu400/aram-core/cpu"
)

// This describes four literal Thumb operations, not an array API or a guest
// loop. Registers, shifts, load offsets, POP masks and return values remain
// ordinary architectural operands. No title identity, address or data value
// participates in classification.
type jitThumbIndexedLoadReturn struct {
	temporary       uint8
	index           uint8
	base            uint8
	loadDestination uint8
	shift           uint8
	loadByteOffset  uint8
	pop             uint16
}

func classifyThumbIndexedLoadReturn(block *jitBlock) *jitThumbIndexedLoadReturn {
	if block == nil || len(block.thumb) != 4 || len(block.arm) != 0 ||
		block.extraLoop != nil || block.countedLoop != nil || block.compareBranchTail != nil ||
		uint64(block.start)+8 >= 1<<32 || block.end != block.start+8 {
		return nil
	}
	w := block.thumb
	for index := range w {
		if w[index].pc != block.start+uint32(index)*2 {
			return nil
		}
	}
	// LSLS tmp,index,#amount; ADDS tmp,tmp,base; LDR dst,[tmp,#offset];
	// POP {...,PC}. The full ADD flags are live at the potentially faulting
	// load, while the preceding shift flags were proved dead by translation.
	if w[0].op != thumbShiftImmediate || w[0].raw&0xf800 != 0 ||
		w[0].appOp != thumbShiftImmediate+thumbDeadFlagsOffset ||
		w[1].op != thumbAddSubtract || w[1].raw&0xfe00 != 0x1800 ||
		w[1].appOp != thumbAddSubtract ||
		w[2].op != thumbImmediateTransfer || w[2].raw&0xf800 != 0x6800 ||
		w[2].appOp != thumbAppImmediateTransferBase+1 ||
		w[3].op != thumbPop || w[3].raw&0xff00 != 0xbd00 || w[3].appOp != thumbPop {
		return nil
	}
	temporary := uint8(w[0].raw & 7)
	if uint8(w[1].raw&7) != temporary || uint8(w[1].raw>>3&7) != temporary ||
		uint8(w[2].raw>>3&7) != temporary {
		return nil
	}
	return &jitThumbIndexedLoadReturn{
		temporary: temporary, index: uint8(w[0].raw >> 3 & 7),
		base: uint8(w[1].raw >> 6 & 7), loadDestination: uint8(w[2].raw & 7),
		shift: uint8(w[0].raw >> 6 & 31), loadByteOffset: uint8(w[2].raw>>6&31) * 4,
		pop: w[3].raw,
	}
}

// tryThumbApplicationIndexedLoadReturn is called only after the application
// runner's complete-block budget and generation boundaries. A declined attempt
// has no architectural, memory, cache or statistics effects, so the original
// instruction loop can execute the block exactly once. A successful attempt
// still retires all four instructions through that runner's ordinary blockDone
// path and records their original diagnostic classes exactly once.
func (b *Backend) tryThumbApplicationIndexedLoadReturn(block *jitBlock) bool {
	return b.executeThumbApplicationIndexedLoadReturn(block, 0)
}

// The generated dead-LSLS case already recorded instruction zero and stored
// its ordinary PC+2. No LSLS register or flag write has run at this boundary.
func (b *Backend) tryThumbApplicationIndexedLoadReturnAfterFirst(block *jitBlock) bool {
	return b.executeThumbApplicationIndexedLoadReturn(block, 1)
}

// recordStart is private to the two entries above: zero for a direct entry,
// one after the generated first-instruction prelude. It affects diagnostics
// only; normal collectThumbMicro=false compilation removes that use.
func (b *Backend) executeThumbApplicationIndexedLoadReturn(block *jitBlock, recordStart int) bool {
	if block == nil || block.indexedLoadReturn == nil || b.mode != cpu.ModeThumb ||
		b.systemBus != nil || b.physicalAccess || b.tlb != nil || b.tracing() {
		return false
	}
	p := block.indexedLoadReturn
	shifted := b.regs[p.index] << p.shift
	base := b.regs[p.base]
	if p.base == p.temporary {
		// ADDS reads the result of LSLS when its second source aliases tmp.
		base = shifted
	}
	address, carry, overflow := addWithCarry(shifted, base, 0)
	loadAddress := address + uint32(p.loadByteOffset)
	entry := &b.privateDataCache[privateDataCacheIndex(loadAddress, cpu.PermissionRead)]
	loaded, hit := cachedJITWord(entry, loadAddress)
	if !hit {
		return false
	}
	mask := uint32(p.pop&0xff) | 1<<cpu.RegisterPC
	count := bits.OnesCount32(mask)
	stackTop := b.regs[cpu.RegisterSP]
	stack, hit := b.privateThumbStackSpan(b.blockTransferAddress(stackTop), count, cpu.PermissionRead)
	if !hit {
		return false
	}
	// Both reads now belong to stable private mappings under Backend.Run's
	// mutex. No store, callback, mapping change or fault can occur in this
	// four-op sequence; reading overlapping data/stack is therefore safe.
	b.regs[p.temporary] = address
	b.setNZCV(address, carry, overflow)
	b.regs[p.loadDestination] = loaded
	b.regs[cpu.RegisterPC] = block.end // Architectural PC immediately before POP.
	for offset := 0; mask != 0; offset += 4 {
		register := bits.TrailingZeros32(mask)
		value := binary.LittleEndian.Uint32(stack[offset : offset+4])
		if register == int(cpu.RegisterPC) {
			b.branchExchange(value)
		} else {
			b.regs[register] = value
		}
		mask &= mask - 1
	}
	// Match POP's writeback of the original SP, including its low bits and
	// the uint32 wrap at the top of the address space.
	b.regs[cpu.RegisterSP] = stackTop + uint32(count*4)
	if collectThumbMicro {
		for index := recordStart; index < len(block.thumb); index++ {
			b.recordThumbMicro(&block.thumb[index])
		}
	}
	return true
}
