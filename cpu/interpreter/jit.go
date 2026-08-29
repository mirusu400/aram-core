package interpreter

// This file implements ARAM's optional pure-Go dynamic recompiler ("JIT"): a
// second CPU execution strategy behind the same Backend. Instead of decoding
// and dispatching each Thumb instruction on every execution, it translates a
// straight run of instructions into a cached slice of closures (a translated
// block) once, then re-runs the closures on subsequent executions — the
// translate-cache-execute model of a dynamic binary translator, expressed in
// pure Go (no host code emission, so it stays Android/iOS-portable).
//
// This file holds the Thumb decoder; arm_jit.go adds ARM blocks behind the same
// cache/fallback contract. Any instruction they do not translate falls back to
// the interpreter one at a time. Every closure body mirrors the corresponding
// interpreter case exactly, and
// cpu/conformance plus the real-game reference tests guard bit-for-bit
// equivalence with the interpreter oracle.

import (
	"math/bits"

	"github.com/mirusu400/aram-core/cpu"
)

// jitExec runs one translated instruction. branched is true when it redirected
// control (so the run must re-dispatch at the new PC); reason/err mirror the
// interpreter's stop/fault contract.
type jitExec func(b *Backend) (branched bool, reason *cpu.StopReason, err error)

type jitInstr struct {
	pc        uint32
	condition uint8 // ARM only; Thumb's runner ignores it
	exec      jitExec
}

type jitBlock struct {
	start  uint32
	end    uint32
	instrs []jitInstr
}

const jitMaxBlock = 256

// smcInvalidate drops the translated-block cache when a write lands in
// executable memory, so self-modifying or freshly loaded code never runs from a
// stale translation. It is a cheap nil check when the JIT is disabled.
func (b *Backend) smcInvalidate(address, size uint32, perms cpu.Permissions) {
	if perms&cpu.PermissionExecute == 0 {
		return
	}
	if b.jitBlocks != nil || b.armJITBlocks != nil {
		// KTF/WIPI map the guest image read-write-execute, so the blitter's
		// thousands of framebuffer writes each land in an executable region.
		// Only a write that overlaps the span of code we have actually
		// translated can invalidate a block; a write outside it (framebuffer,
		// heap, stack) leaves the cache intact. jitCodeLo/Hi bound every
		// translated block, so this never keeps a stale translation.
		if size != 0 && uint64(address) < uint64(b.jitCodeHi) &&
			uint64(address)+uint64(size) > uint64(b.jitCodeLo) &&
			b.hasJITCodePages(address, size) {
			b.invalidateJITRange(address, size)
		}
	}
	if b.nativeBlocks != nil {
		// Same reasoning as the pure-Go JIT above: only a write that overlaps
		// the span of code the native JIT has actually translated can leave a
		// stale block behind. The guest blitter's stores into the same
		// read-write-execute image are not self-modifying code.
		if size != 0 && uint64(address) < uint64(b.nativeCodeHi) &&
			uint64(address)+uint64(size) > uint64(b.nativeCodeLo) &&
			b.hasCodePages(address, size) {
			b.nativeInvalidateRange(address, size)
		}
	}
}

// blockPageIndex maps a guest page to the start PCs of the translated blocks
// registered on it. Both kinds of invalidation arrive with an address and a
// size -- a self-modifying write, or one line of CP15 cache maintenance -- so
// without an index every one of them costs a walk of every block ever
// translated. A guest that keeps writing into its own read-write-execute image
// (KTF/WIPI do) would pay that per store.
//
// A block is registered only under the page its start PC lies on, so no entry
// is ever duplicated and removing one is a single bucket compaction. A scan
// therefore has to look one page further back than the range it is
// invalidating, which is exact because no block reaches a whole page past its
// start (see maxTranslatedBlockBytes).
//
// Buckets are compacted in place whenever they are scanned: a PC whose map
// entry has already gone is dropped then, so a bucket cannot outgrow the number
// of blocks actually starting on its page.
type blockPageIndex map[uint32][]uint32

// maxTranslatedBlockBytes bounds how far a translated block's decoded guest
// bytes reach past its start PC: both translators stop after jitMaxBlock /
// nativeMaxBlock instructions of at most four bytes each.
const maxTranslatedBlockBytes = 4 * jitMaxBlock

// Compile-time guards for the one-page lookback in blockPageIndex.scan. These
// fail to build (constant overflows uint) if a block could span more than one
// page boundary, or if the two translators stop agreeing about block length.
const (
	_ = uint(tlbPageSize - maxTranslatedBlockBytes)
	_ = uint(jitMaxBlock - nativeMaxBlock)
	_ = uint(nativeMaxBlock - jitMaxBlock)
)

// cacheJITBlock and its three siblings are the only sanctioned writes to a
// block map: each records a translation (or a nil "nothing translatable here"
// marker) and registers the PC in the matching page index. An entry missing
// from its index is invisible to range invalidation, so it would outlive the
// guest code it was decoded from.
func (b *Backend) cacheJITBlock(pc uint32, block *jitBlock) {
	b.jitBlocks[pc] = block
	b.jitBlockPages.add(pc)
	b.recordTranslatedBlock(block)
}

func (b *Backend) cacheARMJITBlock(pc uint32, block *jitBlock) {
	b.armJITBlocks[pc] = block
	b.armJITBlockPages.add(pc)
	b.recordTranslatedBlock(block)
}

func (b *Backend) recordTranslatedBlock(block *jitBlock) {
	if block == nil {
		return
	}
	b.executionStatistics.TranslatedBlocks++
	b.executionStatistics.TranslatedGuestBytes += uint64(block.end - block.start)
}

func (index blockPageIndex) add(pc uint32) {
	if index != nil {
		page := pc >> tlbPageBits
		index[page] = append(index[page], pc)
	}
}

// scan calls visit for every PC that could have decoded bytes inside
// [address, address+size), and drops the PC from the index when visit reports
// that it is gone. visit returns whether the entry has disappeared from its
// block map, either because it was already absent or because visit deleted it.
func (index blockPageIndex) scan(address, size uint32, visit func(pc uint32) bool) {
	if index == nil || size == 0 {
		return
	}
	first := uint64(address) >> tlbPageBits
	if first != 0 {
		first-- // a block starting on the previous page can reach into this one
	}
	last := (uint64(address) + uint64(size) - 1) >> tlbPageBits
	for page := first; page <= last; page++ {
		bucket := index[uint32(page)]
		if len(bucket) == 0 {
			continue
		}
		kept := bucket[:0]
		for _, pc := range bucket {
			if !visit(pc) {
				kept = append(kept, pc)
			}
		}
		if len(kept) == 0 {
			delete(index, uint32(page))
			continue
		}
		index[uint32(page)] = kept
	}
}

func (b *Backend) invalidateJITRange(address, size uint32) bool {
	changed := false
	drop := func(blocks map[uint32]*jitBlock, width uint32) func(uint32) bool {
		return func(pc uint32) bool {
			block, present := blocks[pc]
			if !present {
				return true // stale index entry: compact it away
			}
			overlaps := widthRangeOverlap(pc, width, address, size)
			if block != nil {
				overlaps = rangesOverlap(block.start, block.end, address, size)
			}
			if !overlaps {
				return false
			}
			delete(blocks, pc)
			changed = true
			return true
		}
	}
	b.jitBlockPages.scan(address, size, drop(b.jitBlocks, 2))
	b.armJITBlockPages.scan(address, size, drop(b.armJITBlocks, 4))
	if changed {
		b.jitGen++
	}
	return changed
}

// invalidateTranslationRange is used by line-granular I-cache maintenance and
// persistent native MMIO bail handling. It deliberately leaves conservative
// code-page bitmaps intact; doing so can cause a later harmless scan, but can
// never retain stale translated code or reopen a native write TLB entry.
//
// It takes smcInvalidate's guards before walking anything. Whole-system guests
// run CP15 c7,c5,1 as a loop over every line of a buffer they just filled, so
// an unguarded walk costs O(translated blocks) per 32 bytes maintained: a 1 MiB
// range is 32768 iterations over four maps. The lo/hi hull and the code-page
// bitmaps are conservative supersets of everything that has been translated, so
// short-circuiting on them cannot retain a stale block.
func (b *Backend) invalidateTranslationRange(address, size uint32) {
	if size == 0 {
		return
	}
	if b.jitBlocks != nil || b.armJITBlocks != nil {
		if uint64(address) < uint64(b.jitCodeHi) &&
			uint64(address)+uint64(size) > uint64(b.jitCodeLo) &&
			b.hasJITCodePages(address, size) {
			b.invalidateJITRange(address, size)
		}
	}
	if b.nativeBlocks != nil {
		if uint64(address) < uint64(b.nativeCodeHi) &&
			uint64(address)+uint64(size) > uint64(b.nativeCodeLo) &&
			b.hasCodePages(address, size) {
			b.nativeInvalidateRange(address, size)
		}
	}
}

// invalidateTranslations drops code decoded under an older virtual mapping or
// instruction-cache view. Whole-system guests can change both through CP15
// without writing a private mapped region, so ordinary SMC invalidation alone
// is not sufficient for them.
func (b *Backend) invalidateTranslations() {
	b.executionStatistics.TranslationInvalidations++
	if b.jitBlocks != nil {
		clear(b.jitBlocks)
		clear(b.jitBlockPages)
	}
	if b.armJITBlocks != nil {
		clear(b.armJITBlocks)
		clear(b.armJITBlockPages)
	}
	if b.jitBlocks != nil || b.armJITBlocks != nil {
		b.jitGen++
		clear(b.jitCodePages)
		b.jitCodeLo, b.jitCodeHi = ^uint32(0), 0
	}
	b.nativeInvalidate()
	// nativeInvalidate cannot drop the link slots themselves: its other caller
	// is the arena-full retry inside a translator, which by then has the slot
	// addresses baked into a buffer it has not appended yet, so releasing the
	// last Go reference would let the collector free memory that emitted code
	// is about to jump through. Nothing is mid-translation on this path, and
	// every block is gone, so here the map can be reclaimed - otherwise it only
	// ever grows, one entry per branch target ever translated.
	clear(b.nativeLinks)
}

// runThumbJIT executes Thumb from b.regs[PC] using translated blocks, retiring
// up to limit instructions. It falls back to the interpreter for any PC whose
// instruction it cannot translate.
func (b *Backend) runThumbJIT(limit uint64) (uint64, *cpu.StopReason, error) {
	var executed uint64
	wholeSystem := b.systemBus != nil
	hasExecutionTraps := len(b.executionTraps) != 0
	traced := b.tracing()
outer:
	for executed < limit {
		pc := b.regs[cpu.RegisterPC]
		if wholeSystem {
			if b.takePendingInterrupt() {
				return executed, nil, nil
			}
			pc = b.regs[cpu.RegisterPC]
			if hasExecutionTraps && b.executionTrapAt(cpu.ModeThumb, pc) {
				reason := cpu.StopExecutionTrap
				return executed, &reason, nil
			}
		}
		block := b.jitBlockAt(pc)
		if block == nil {
			// Untranslatable at pc: interpret exactly one instruction.
			n, reason, err := b.runThumb(1)
			executed += n
			if err != nil {
				return executed, nil, err
			}
			if reason != nil {
				return executed, reason, nil
			}
			if b.mode != cpu.ModeThumb {
				return executed, nil, nil
			}
			continue
		}
		blockInstructions := len(block.instrs)
		if remaining := limit - executed; uint64(blockInstructions) > remaining {
			blockInstructions = int(remaining)
		}
		for i := 0; i < blockInstructions; i++ {
			in := &block.instrs[i]
			if wholeSystem {
				// Dispatch checked the first instruction boundary already. Poll
				// again only after an instruction has had a chance to change IRQ
				// state or advance to another configured trap.
				if i != 0 {
					if b.takePendingInterrupt() {
						return executed, nil, nil
					}
					pc = b.regs[cpu.RegisterPC]
					if hasExecutionTraps && b.executionTrapAt(cpu.ModeThumb, pc) {
						reason := cpu.StopExecutionTrap
						return executed, &reason, nil
					}
				}
				if traced {
					b.recordPC(pc)
				}
				b.instructionAddress = pc
			} else if traced {
				b.recordPC(in.pc)
			}
			branched, reason, err := in.exec(b)
			if err != nil {
				return executed, nil, err
			}
			executed++
			if reason != nil {
				return executed, reason, nil
			}
			if branched {
				if b.mode != cpu.ModeThumb {
					return executed, nil, nil
				}
				continue outer
			}
		}
	}
	return executed, nil, nil
}

// jitBlockAt returns the translated block at pc, translating and caching on a
// miss. A nil entry is cached for a PC whose first instruction is untranslatable
// so it is not re-translated each time.
// jitCacheSize is the direct-mapped block-dispatch cache depth. A power of two
// so the index is a mask; large enough that a title's hot block set rarely
// collides, small enough to stay resident (each entry is 24 bytes).
const jitCacheSize = 8192

// jitCacheEntry caches one translated block for direct-mapped dispatch. gen ties
// it to the jitBlocks generation so an invalidation (which bumps b.jitGen) makes
// every stale entry miss without the invalidation path having to walk the array.
type jitCacheEntry struct {
	pc    uint32
	gen   uint64
	block *jitBlock
}

func (b *Backend) jitBlockAt(pc uint32) *jitBlock {
	slot := &b.jitCache[int(pc>>1)&(jitCacheSize-1)]
	if slot.block != nil && slot.pc == pc && slot.gen == b.jitGen {
		return slot.block
	}
	block, ok := b.jitBlocks[pc]
	if !ok {
		block = b.translateThumbBlock(pc)
		b.cacheJITBlock(pc, block)
		if block != nil {
			b.markJITCodePages(block.start, block.end-block.start)
			// Grow the translated-code span so smcInvalidate can tell a real
			// code write from an ordinary data/framebuffer write.
			if block.start < b.jitCodeLo {
				b.jitCodeLo = block.start
			}
			if block.end > b.jitCodeHi {
				b.jitCodeHi = block.end
			}
		}
	}
	slot.pc = pc
	slot.gen = b.jitGen
	slot.block = block
	return block
}

func (b *Backend) markJITCodePages(address, size uint32) bool {
	if b.jitCodePages == nil || size == 0 {
		return false
	}
	grew := false
	last := (uint64(address) + uint64(size) - 1) >> tlbPageBits
	for page := uint64(address) >> tlbPageBits; page <= last; page++ {
		word, mask := page>>6, uint64(1)<<(page&63)
		if b.jitCodePages[word]&mask == 0 {
			b.jitCodePages[word] |= mask
			grew = true
		}
	}
	return grew
}

func (b *Backend) hasJITCodePages(address, size uint32) bool {
	if b.jitCodePages == nil || size == 0 {
		return false
	}
	last := (uint64(address) + uint64(size) - 1) >> tlbPageBits
	for page := uint64(address) >> tlbPageBits; page <= last; page++ {
		if b.jitCodePages[page>>6]&(1<<(page&63)) != 0 {
			return true
		}
	}
	return false
}

func (b *Backend) translateThumbBlock(pc uint32) *jitBlock {
	var instrs []jitInstr
	cur := pc
	for len(instrs) < jitMaxBlock {
		word, err := b.fetch16(cur)
		if err != nil {
			break
		}
		exec, terminates, ok := b.translateThumbInstr(word, cur)
		if !ok {
			break
		}
		instrs = append(instrs, jitInstr{pc: cur, exec: exec})
		cur += 2
		if terminates {
			break
		}
	}
	if len(instrs) == 0 {
		return nil
	}
	return &jitBlock{start: pc, end: cur, instrs: instrs}
}

// translateThumbInstr returns a closure executing one Thumb instruction, whether
// it terminates a block (redirects control), and whether it could be translated
// at all. Each closure sets b.regs[PC] to the sequential successor first, then
// mirrors the interpreter's handling of the same encoding.
func (b *Backend) translateThumbInstr(instruction uint16, pc uint32) (jitExec, bool, bool) {
	next := pc + 2
	switch thumbInstructionClasses[instruction] {
	case thumbShiftImmediate:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			op := uint32(instruction>>11) & 3
			shift := uint32(instruction>>6) & 0x1f
			rs := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			value := b.regs[rs]
			var result uint32
			var carry bool
			switch op {
			case 0:
				if shift == 0 {
					result = value
					carry = b.carry()
				} else {
					result = value << shift
					carry = value&(uint32(1)<<(32-shift)) != 0
				}
			case 1:
				if shift == 0 {
					result = 0
					carry = value&flagN != 0
				} else {
					result = value >> shift
					carry = value&(uint32(1)<<(shift-1)) != 0
				}
			case 2:
				if shift == 0 {
					carry = value&flagN != 0
					if carry {
						result = ^uint32(0)
					}
				} else {
					result = uint32(int32(value) >> shift)
					carry = value&(uint32(1)<<(shift-1)) != 0
				}
			default:
				return false, nil, b.unsupportedThumb(pc, instruction)
			}
			b.regs[rd] = result
			b.setNZC(result, carry)
			return false, nil, nil
		}, false, true

	case thumbMoveImmediate:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			value := uint32(instruction & 0xff)
			b.regs[rd] = value
			b.setNZ(value)
			return false, nil, nil
		}, false, true

	case thumbCompareImmediate:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
			b.setNZCV(result, carry, overflow)
			return false, nil, nil
		}, false, true

	case thumbAddImmediate:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], uint32(instruction&0xff), 0)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
			return false, nil, nil
		}, false, true

	case thumbSubtractImmediate:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			result, carry, overflow := addWithCarry(b.regs[rd], ^uint32(instruction&0xff), 1)
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
			return false, nil, nil
		}, false, true

	case thumbAddSubtract:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			immediate := instruction&(1<<10) != 0
			subtract := instruction&(1<<9) != 0
			rnOrImmediate := uint32(instruction>>6) & 7
			rs := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			right := rnOrImmediate
			if !immediate {
				right = b.regs[rnOrImmediate]
			}
			var result uint32
			var carry, overflow bool
			if subtract {
				result, carry, overflow = addWithCarry(b.regs[rs], ^right, 1)
			} else {
				result, carry, overflow = addWithCarry(b.regs[rs], right, 0)
			}
			b.regs[rd] = result
			b.setNZCV(result, carry, overflow)
			return false, nil, nil
		}, false, true

	case thumbALU:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			op := (instruction >> 6) & 0xf
			rs := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			left, right := b.regs[rd], b.regs[rs]
			switch op {
			case 0x0:
				b.regs[rd] = left & right
				b.setNZ(b.regs[rd])
			case 0x1:
				b.regs[rd] = left ^ right
				b.setNZ(b.regs[rd])
			case 0x2:
				result, carry := shiftLSL(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x3:
				result, carry := shiftLSR(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x4:
				result, carry := shiftASR(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x5:
				carryIn := uint32(0)
				if b.carry() {
					carryIn = 1
				}
				result, carry, overflow := addWithCarry(left, right, carryIn)
				b.regs[rd] = result
				b.setNZCV(result, carry, overflow)
			case 0x6:
				carryIn := uint32(0)
				if b.carry() {
					carryIn = 1
				}
				result, carry, overflow := addWithCarry(left, ^right, carryIn)
				b.regs[rd] = result
				b.setNZCV(result, carry, overflow)
			case 0x7:
				result, carry := shiftROR(left, uint8(right), b.carry())
				b.regs[rd] = result
				b.setNZC(result, carry)
			case 0x8:
				b.setNZ(left & right)
			case 0x9:
				result, carry, overflow := addWithCarry(0, ^right, 1)
				b.regs[rd] = result
				b.setNZCV(result, carry, overflow)
			case 0xa:
				result, carry, overflow := addWithCarry(left, ^right, 1)
				b.setNZCV(result, carry, overflow)
			case 0xb:
				result, carry, overflow := addWithCarry(left, right, 0)
				b.setNZCV(result, carry, overflow)
			case 0xc:
				b.regs[rd] = left | right
				b.setNZ(b.regs[rd])
			case 0xd:
				b.regs[rd] = left * right
				b.setNZ(b.regs[rd])
			case 0xe:
				b.regs[rd] = left &^ right
				b.setNZ(b.regs[rd])
			case 0xf:
				b.regs[rd] = ^right
				b.setNZ(b.regs[rd])
			}
			return false, nil, nil
		}, false, true

	case thumbHighRegister:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			op := (instruction >> 8) & 3
			rs := uint32(instruction>>3)&7 | uint32(instruction>>6)&1<<3
			rd := uint32(instruction)&7 | uint32(instruction>>7)&1<<3
			switch op {
			case 0:
				result := b.readOperandRegister(rd, pc, cpu.ModeThumb) +
					b.readOperandRegister(rs, pc, cpu.ModeThumb)
				if rd == cpu.RegisterPC {
					result &^= 1
				}
				b.regs[rd] = result
			case 1:
				result, carry, overflow := addWithCarry(
					b.readOperandRegister(rd, pc, cpu.ModeThumb),
					^b.readOperandRegister(rs, pc, cpu.ModeThumb),
					1,
				)
				b.setNZCV(result, carry, overflow)
			case 2:
				result := b.readOperandRegister(rs, pc, cpu.ModeThumb)
				if rd == cpu.RegisterPC {
					result &^= 1
				}
				b.regs[rd] = result
			case 3:
				target := b.readOperandRegister(rs, pc, cpu.ModeThumb)
				if instruction&(1<<7) != 0 {
					b.regs[cpu.RegisterLR] = (pc + 2) | 1
				}
				b.branchExchange(target)
			}
			return true, nil, nil
		}, true, true

	case thumbLiteralLoad:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			address := ((pc + 4) &^ uint32(3)) + uint32(instruction&0xff)*4
			value, err := b.read32(address, cpu.PermissionRead)
			if err != nil {
				return false, nil, err
			}
			b.regs[rd] = value
			return false, nil, nil
		}, false, true

	case thumbRegisterTransfer:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			op := uint32(instruction>>9) & 7
			ro := uint32(instruction>>6) & 7
			rb := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			address := b.regs[rb] + b.regs[ro]
			switch op {
			case 0:
				if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			case 1:
				if err := b.write16(address, uint16(b.regs[rd]), cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			case 2:
				if err := b.write8(address, byte(b.regs[rd]), cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			case 3:
				value, err := b.read8(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(int32(int8(value)))
			case 4:
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = value
			case 5:
				value, err := b.read16(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(value)
			case 6:
				value, err := b.read8(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(value)
			case 7:
				value, err := b.read16(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(int32(int16(value)))
			}
			return false, nil, nil
		}, false, true

	case thumbImmediateTransfer:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			byteTransfer := instruction&(1<<12) != 0
			load := instruction&(1<<11) != 0
			offset := uint32(instruction>>6) & 0x1f
			rb := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			if !byteTransfer {
				offset *= 4
			}
			address := b.regs[rb] + offset
			switch {
			case load && byteTransfer:
				value, err := b.read8(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(value)
			case load:
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = value
			case byteTransfer:
				if err := b.write8(address, byte(b.regs[rd]), cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			default:
				if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			}
			return false, nil, nil
		}, false, true

	case thumbHalfwordTransfer:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			load := instruction&(1<<11) != 0
			offset := uint32(instruction>>6) & 0x1f
			rb := uint32(instruction>>3) & 7
			rd := uint32(instruction) & 7
			address := b.regs[rb] + offset*2
			if load {
				value, err := b.read16(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = uint32(value)
			} else if err := b.write16(address, uint16(b.regs[rd]), cpu.PermissionWrite); err != nil {
				return false, nil, err
			}
			return false, nil, nil
		}, false, true

	case thumbStackTransfer:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			load := instruction&(1<<11) != 0
			rd := uint32(instruction>>8) & 7
			address := b.regs[cpu.RegisterSP] + uint32(instruction&0xff)*4
			if load {
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[rd] = value
			} else if err := b.write32(address, b.regs[rd], cpu.PermissionWrite); err != nil {
				return false, nil, err
			}
			return false, nil, nil
		}, false, true

	case thumbAdjustStack:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			offset := uint32(instruction&0x7f) * 4
			if instruction&(1<<7) != 0 {
				b.regs[cpu.RegisterSP] -= offset
			} else {
				b.regs[cpu.RegisterSP] += offset
			}
			return false, nil, nil
		}, false, true

	case thumbPush:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			registers := uint16(instruction & 0xff)
			includeLR := instruction&(1<<8) != 0
			count := bits.OnesCount16(registers)
			if includeLR {
				count++
			}
			start := b.regs[cpu.RegisterSP] - uint32(count*4)
			address := start
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				if err := b.write32(address, b.regs[register], cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
				address += 4
			}
			if includeLR {
				if err := b.write32(address, b.regs[cpu.RegisterLR], cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
			}
			b.regs[cpu.RegisterSP] = start
			return false, nil, nil
		}, false, true

	case thumbPop:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			registers := uint16(instruction & 0xff)
			includePC := instruction&(1<<8) != 0
			address := b.regs[cpu.RegisterSP]
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.regs[register] = value
				address += 4
			}
			if includePC {
				value, err := b.read32(address, cpu.PermissionRead)
				if err != nil {
					return false, nil, err
				}
				b.branchExchange(value)
				address += 4
			}
			b.regs[cpu.RegisterSP] = address
			return includePC, nil, nil
		}, true, true

	case thumbAddPCSP:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			rd := uint32(instruction>>8) & 7
			base := b.regs[cpu.RegisterSP]
			if instruction&(1<<11) == 0 {
				base = (pc + 4) &^ uint32(3)
			}
			b.regs[rd] = base + uint32(instruction&0xff)*4
			return false, nil, nil
		}, false, true

	case thumbMultipleTransfer:
		if instruction&0xff == 0 {
			return nil, false, false
		}
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			load := instruction&(1<<11) != 0
			rb := uint32(instruction>>8) & 7
			registers := uint16(instruction & 0xff)
			address := b.regs[rb]
			for register := uint32(0); register < 8; register++ {
				if registers&(1<<register) == 0 {
					continue
				}
				if load {
					value, err := b.read32(address, cpu.PermissionRead)
					if err != nil {
						return false, nil, err
					}
					b.regs[register] = value
				} else if err := b.write32(address, b.regs[register], cpu.PermissionWrite); err != nil {
					return false, nil, err
				}
				address += 4
			}
			if !load || registers&(1<<rb) == 0 {
				b.regs[rb] = address
			}
			return false, nil, nil
		}, false, true

	case thumbConditionalBranch:
		condition := uint8(instruction>>8) & 0xf
		if condition == 0xe || condition == 0xf {
			// SWI / undefined: let the interpreter handle it precisely.
			return nil, false, false
		}
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			if b.conditionPassed(condition) {
				offset := int32(int8(instruction&0xff)) << 1
				b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + offset)
				return true, nil, nil
			}
			return false, nil, nil
		}, true, true

	case thumbUnconditionalBranch:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			offset := int32(instruction & 0x7ff)
			if offset&(1<<10) != 0 {
				offset |= ^int32(0x7ff)
			}
			b.regs[cpu.RegisterPC] = uint32(int32(pc+4) + (offset << 1))
			return true, nil, nil
		}, true, true

	case thumbBreakpoint:
		return func(b *Backend) (bool, *cpu.StopReason, error) {
			b.regs[cpu.RegisterPC] = next
			reason := cpu.StopBreakpoint
			return false, &reason, nil
		}, true, true

	default:
		// thumbLongBranch / thumbLongBranchSuffix / anything else: end the block
		// and let the interpreter execute it.
		return nil, false, false
	}
}
