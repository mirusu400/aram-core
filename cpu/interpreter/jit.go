package interpreter

// This file implements ARAM's optional pure-Go dynamic recompiler ("JIT"): a
// second CPU execution strategy behind the same Backend. Instead of decoding
// and dispatching each Thumb instruction on every execution, it translates a
// straight run of instructions into a cached translated block once, then
// re-runs compact Thumb micro-ops or decoded ARM operations from that block:
// the translate-cache-execute model of a dynamic binary translator, expressed in
// pure Go (no host code emission, so it stays Android/iOS-portable).
//
// This file holds Thumb block dispatch; jit_thumb_micro.go executes its compact
// operations, while arm_jit.go adds ARM blocks behind the same cache/fallback
// contract. Any instruction they do not translate falls back to
// the interpreter one at a time. Every closure body mirrors the corresponding
// interpreter case exactly, and
// cpu/conformance plus the real-game reference tests guard bit-for-bit
// equivalence with the interpreter oracle.

import "github.com/mirusu400/aram-core/cpu"

// jitExec runs one translated instruction. branched is true when it redirected
// control (so the run must re-dispatch at the new PC); reason/err mirror the
// interpreter's stop/fault contract.
type jitExec func(b *Backend) (branched bool, reason *cpu.StopReason, err error)

type armMicroOp uint8

const (
	armMicroClosure armMicroOp = iota
	armMicroSingleTransfer
	armMicroHalfwordTransfer
	armMicroBlockTransfer
)

type jitInstr struct {
	pc        uint32
	raw       uint32
	condition uint8 // ARM only; Thumb's runner ignores it
	op        armMicroOp
	exec      jitExec
}

type thumbMicroInstr struct {
	pc  uint32
	raw uint16
	op  thumbInstructionClass
}

type jitBlock struct {
	start uint32
	end   uint32
	arm   []jitInstr
	thumb []thumbMicroInstr
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

// Translation invalidation is indexed at the host/code page granularity. It
// is deliberately independent of the native data TLB, whose ARM926-aligned
// 1 KiB subpages are smaller.
const (
	codePageBits = 12
	codePageSize = 1 << codePageBits
)

// Compile-time guards for the one-page lookback in blockPageIndex.scan. These
// fail to build (constant overflows uint) if a block could span more than one
// page boundary, or if the two translators stop agreeing about block length.
const (
	_ = uint(codePageSize - maxTranslatedBlockBytes)
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
		page := pc >> codePageBits
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
	first := uint64(address) >> codePageBits
	if first != 0 {
		first-- // a block starting on the previous page can reach into this one
	}
	last := (uint64(address) + uint64(size) - 1) >> codePageBits
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
		blockInstructions := len(block.thumb)
		if remaining := limit - executed; uint64(blockInstructions) > remaining {
			blockInstructions = int(remaining)
		}
		retired, branched, reason, err := b.executeThumbMicroBlock(
			block, blockInstructions, wholeSystem, hasExecutionTraps, traced,
		)
		executed += uint64(retired)
		if err != nil {
			return executed, nil, err
		}
		if reason != nil {
			return executed, reason, nil
		}
		if b.mode != cpu.ModeThumb {
			return executed, nil, nil
		}
		if branched {
			continue outer
		}
	}
	return executed, nil, nil
}

// jitBlockAt returns the translated block at pc, translating and caching on a
// miss. A nil entry is cached for a PC whose first instruction is untranslatable
// so it is not re-translated each time.
// jitCacheSize is the number of two-way block-dispatch cache sets. A power of
// two keeps indexing to a mask. Two ways retain hot PCs that alias in the low
// address bits, which is common when firmware mirrors code at fixed offsets.
const jitCacheSize = 8192

// jitCacheEntry caches one translated block. valid is separate from block so a
// nil translation is a real negative cache hit rather than a map lookup on every
// execution. gen ties entries to jitBlocks without clearing the sets on a rare
// invalidation.
type jitCacheEntry struct {
	block *jitBlock
	gen   uint64
	pc    uint32
	valid bool
}

type jitCacheSet [2]jitCacheEntry

func (set *jitCacheSet) lookup(pc uint32, gen uint64) (*jitBlock, bool) {
	if entry := &set[0]; entry.valid && entry.pc == pc && entry.gen == gen {
		return entry.block, true
	}
	if entry := &set[1]; entry.valid && entry.pc == pc && entry.gen == gen {
		set[0], set[1] = set[1], set[0]
		return set[0].block, true
	}
	return nil, false
}

func (set *jitCacheSet) store(pc uint32, gen uint64, block *jitBlock) {
	set[1] = set[0]
	set[0] = jitCacheEntry{block: block, gen: gen, pc: pc, valid: true}
}

func (b *Backend) jitBlockAt(pc uint32) *jitBlock {
	slot := &b.jitCache[int(pc>>1)&(jitCacheSize-1)]
	if block, ok := slot.lookup(pc, b.jitGen); ok {
		return block
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
	slot.store(pc, b.jitGen, block)
	return block
}

func (b *Backend) markJITCodePages(address, size uint32) bool {
	if b.jitCodePages == nil || size == 0 {
		return false
	}
	grew := false
	last := (uint64(address) + uint64(size) - 1) >> codePageBits
	for page := uint64(address) >> codePageBits; page <= last; page++ {
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
	last := (uint64(address) + uint64(size) - 1) >> codePageBits
	for page := uint64(address) >> codePageBits; page <= last; page++ {
		if b.jitCodePages[page>>6]&(1<<(page&63)) != 0 {
			return true
		}
	}
	return false
}

func (b *Backend) translateThumbBlock(pc uint32) *jitBlock {
	var instrs []thumbMicroInstr
	cur := pc
	for len(instrs) < jitMaxBlock {
		word, err := b.fetch16(cur)
		if err != nil {
			break
		}
		op, terminates, ok := translateThumbMicroOp(word)
		if !ok {
			break
		}
		instrs = append(instrs, thumbMicroInstr{pc: cur, raw: word, op: op})
		cur += 2
		if terminates {
			break
		}
	}
	if len(instrs) == 0 {
		return nil
	}
	return &jitBlock{start: pc, end: cur, thumb: instrs}
}
