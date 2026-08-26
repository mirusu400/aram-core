package interpreter

import (
	"sync/atomic"
	"unsafe"

	"github.com/mirusu400/aram-core/cpu"
)

// This file holds the portable, build-tag-free pieces of the optional native
// JIT backend (see native_windows_amd64.go / native_android_arm64.go for the
// per-host machine-code emitters). It compiles on every target so the shared
// Backend struct, Run dispatch, and SMC/lifecycle hooks reference the same
// types everywhere; the emitters themselves are behind //go:build tags and a
// stub keeps non-native builds (Android arm64 cross-check aside) clean.
//
// The native JIT is ARAM's third CPU execution strategy behind the same
// cpu.Backend as the tree-walking interpreter (the accuracy oracle) and the
// pure-Go closure JIT. It translates straight ARM and Thumb runs into host
// machine code once, caches them, and re-runs them; supported RAM accesses use
// a guarded inline TLB and unsupported instructions fall back one at a time.
// It is validated bit-for-bit
// against the interpreter by cpu/conformance. Its value is speed on hosts where
// emitting native code beats Go dispatch; iOS (JIT forbidden) never compiles an
// emitter and always falls back to the precise interpreter.

const (
	nativeMaxBlock     = 256 // guest instructions per translated block
	nativeStatusNorm   = 0   // block completed; resume dispatch at regs[PC]
	nativeStatusBKPT   = 1   // block hit BKPT; stop with StopBreakpoint
	nativeStatusBudget = 2   // remaining budget < next block; interpret the tail
	nativeStatusBail   = 3   // software-TLB miss; interpret this one instruction
	nativeStatusIRQ    = 4   // serviceable IRQ/FIQ at an instruction boundary
)

// nativeArenaSize is the executable arena's capacity: the bytes of translated
// code held before a flush drops every block.
//
// It is a working-set budget, not a memory-frugality knob. A flush re-emits the
// whole working set, so an arena smaller than a title's hot code turns
// translation into a permanent treadmill. The original 8 MiB held about 5900
// blocks, while 영웅서기3 steadily executes ~23000 (38 MiB of emitted code): it
// flushed and re-translated everything about every 30 frames, spending 45% of
// the frame in the translator. At 128 MiB it never flushes and the frame is
// 2.0x faster.
//
// The arena is reserved, not resident. The windows path commits it a chunk at a
// time and the mmap paths fault pages in on demand, so a title whose code fits
// in a megabyte (메이플스토리 uses 1.2 MiB) still occupies only that much.
const nativeArenaSize = uintptr(128 << 20)

// nativeBailStatus packs the block status a software-TLB miss returns: the low
// byte is nativeStatusBail and the rest is retired, the number of instructions
// the block had already retired when it bailed. The budget gate at the top of a
// block subtracts the WHOLE block's instruction count up front, so the Run loop
// needs retired to give back the part that never ran.
func nativeBailStatus(retired int) uint32 {
	return uint32(nativeStatusBail) | uint32(retired)<<8
}

func nativeInterruptStatus(retired int) uint32 {
	return uint32(nativeStatusIRQ) | uint32(retired)<<8
}

func (b *Backend) refundNativeTail(status uintptr) {
	retired := uint32(status >> 8)
	if retired <= b.nativeActiveCount {
		b.nativeRemain += b.nativeActiveCount - retired
	}
}

func (b *Backend) interruptLinesBase() uintptr {
	if b.systemBus == nil {
		return 0
	}
	return uintptr(unsafe.Pointer(&b.interruptLines))
}

// memAccess describes one Thumb single load/store for the emitters' inline
// software-TLB path. The interpreter's deliberately linear unaligned access is
// reproduced exactly: the host load/store is unaligned-capable and the emitted
// page-crossing check sends anything that would straddle two pages (the only
// case where the interpreter's byte-wise copyOut/copyIn path could differ) back
// to the interpreter.
type memAccess struct {
	store    bool   // store rather than load
	size     uint8  // 1, 2 or 4 bytes
	signed   bool   // LDRSB/LDRSH sign-extend the loaded value
	rd       uint32 // value register (always a low register here)
	base     uint32 // guest base register; RegisterSP for the SP-relative form
	index    uint32 // guest index register, when hasIndex
	hasIndex bool
	offset   uint32 // constant offset added to the base
	subtract bool   // subtract index/offset from the base (ARM U=0)
	absolute bool   // the address is exactly offset (PC-relative literal load)
}

// multiAccess describes one Thumb multi-register transfer (PUSH, POP, STMIA,
// LDMIA) for the emitters' inline path. The whole list is a contiguous run of
// words from one base, so it costs a single software-TLB probe plus a range
// check covering every word - anything that would leave the page bails and the
// interpreter services the whole instruction.
//
// Bailing on the whole instruction is what keeps a partial fault exact: the
// interpreter transfers one word at a time and stops at the first bad address,
// leaving the earlier words written and the base register not yet updated. The
// inline path only runs when no word can fault, so it is all-or-nothing.
type multiAccess struct {
	store     bool     // store the list rather than load it
	regs      []uint32 // guest registers in ascending transfer order
	base      uint32   // base register (RegisterSP for PUSH/POP)
	preDec    bool     // PUSH: the transfer starts 4*len(regs) below the base
	writeback bool     // write the final address back to base
}

// nativeARMDataOp is the decoded subset shared by the x86-64 and AArch64 ARM
// emitters. operand is either a translation-time constant or one unshifted
// guest register. carry is -1 when a logical flag update preserves C, or 0/1
// when a rotated immediate supplies a known shifter carry-out.
type nativeARMDataOp struct {
	opcode     uint8
	setFlags   bool
	rd         uint32
	rn         uint32
	operand    uint32
	operandReg bool
	pcValue    uint32
	carry      int8
}

// nativeARMDataOpEmittable is the single authority on which data-processing
// opcodes armDataProcessing covers, consulted by the decoder before it opens a
// condition site and by both emitters as their own guard. The two must not
// drift: conditionStart emits a branch that only conditionEnd supplies a target
// for, so an emitter that declined after the site opened would leave that
// branch unpatched - a harmless fall-through on x86-64, but a branch-to-itself
// (a guest hang, not a fault) on AArch64.
func nativeARMDataOpEmittable(opcode uint8) bool {
	// ADC/SBC/RSC read the incoming carry, which neither emitter models.
	return opcode < 5 || opcode > 7
}

// emitter appends host machine code for one translated Thumb instruction at a
// time. Each method hides the host's scratch-register choreography; the decoder
// (emitThumb, native_jit.go) only extracts operand fields and calls these. A
// block is a leaf host function that takes &regs[0], mutates the 17-word
// register file in place (regs[16] is CPSR with eager N/Z/C/V), and returns a
// status (see nativeStatus*). Register/ALU classes update flags to match the
// interpreter exactly. Direct RAM memory ops use a guarded software TLB;
// MMIO, faults, mode switches, and unhandled ops bail before side effects, so
// a native block never faults midway through an instruction.
//
// It lives here (all platforms) rather than in the build-tagged JIT core so the
// arm64 encoder ??whose bytes are pure Go and are unit-tested on the amd64 dev
// host ??can implement and assert it without an android build. Only the JIT
// core that consumes an emitter is build-tagged to the native hosts.
type emitter interface {
	prologue()           // set up the base registers (regs ptr in RCX/X0, remain ptr in RDX/X1)
	mark() int           // current code length, for computing internal jump displacements
	appendCode(b []byte) // splice a body emitter's bytes into this one

	// gate is the per-block budget check emitted at the loop header (after the
	// prologue). If the remaining instruction budget is < count it exits with
	// nativeStatusBudget (PC left at startPC); otherwise it subtracts count and
	// falls through into the body. A self-loop's back-edge jumps to this offset,
	// so the budget is re-checked every iteration and the loop stops exactly.
	gate(count int, startPC uint32)

	// interruptPoll is emitted before each guest instruction in a whole-system
	// block. It exits with PC at that instruction and reports how many earlier
	// instructions retired when an asserted IRQ/FIQ is not masked in CPSR.
	// Application blocks omit it entirely.
	interruptPoll(pc uint32, retired int)
	conditionStart(condition uint8) int
	conditionEnd(site int)

	// Body ops (non-terminators) reproduce the interpreter's semantics exactly.
	moveImm(rd, imm uint32)                             // MOVS rd,#imm      -> setNZ
	addImm(rd, imm uint32)                              // ADDS rd,#imm      -> setNZCV
	subImm(rd, imm uint32)                              // SUBS rd,#imm      -> setNZCV
	cmpImm(rd, imm uint32)                              // CMP  rd,#imm      -> setNZCV (no writeback)
	addSub(rd, rs, rn uint32, immediate, subtract bool) // ADD/SUB           -> setNZCV
	shiftImm(rd, rs, op, shift uint32)                  // LSL/LSR/ASR #imm  -> setNZC
	alu(op, rd, rs uint32) bool                         // data-processing; false = bail (unhandled sub-op)
	adjustStack(sub bool, offset uint32)                // ADD/SUB SP,#imm   (no flags)
	addSPImm(rd, offset uint32)                         // rd = SP + imm     (no flags)
	setRegConst(rd, value uint32)                       // rd = const        (no flags; PC-relative add)

	// highRegister translates the ADD/CMP/MOV forms that can name a high
	// register. pcValue is what reading R15 yields here (the interpreter's
	// instruction address + 4). It returns false, emitting nothing, for the
	// forms that are really branches - BX/BLX, and ADD/MOV writing PC - which
	// stay with the interpreter. These are worth translating because they end
	// nearly half of all blocks otherwise, and a block that ends is a full
	// dispatch round trip.
	highRegister(op, rd, rs, pcValue uint32) bool
	armDataProcessing(op nativeARMDataOp) bool

	// memory translates one single load/store inline through the software TLB
	// (native_tlb.go): address computation, a direct-mapped page probe, a
	// page-crossing check, then the host access. On a miss the emitted code
	// leaves PC at pc and returns nativeBailStatus(retired), so the Run loop
	// restores the budget the gate over-subtracted and hands exactly this
	// instruction to the interpreter - which installs the page, so the next
	// execution hits. Nothing else in the block is disturbed, so a bail is a
	// slow path, never a correctness fork.
	memory(a memAccess, pc uint32, retired int)

	// multi translates one PUSH/POP/STMIA/LDMIA inline, with the same probe,
	// bail and budget-restore contract as memory. Function prologues and
	// epilogues are the single largest remaining source of block terminations,
	// and each one that stays untranslated costs a full dispatch round trip.
	multi(a multiAccess, pc uint32, retired int)

	// Terminators end a block. exit* set PC and return to the Go dispatcher;
	// selfLoop* jump back to the gate offset (staying in native code).
	selfLoopUncond(gateOff int)                          // B to own start: jump to gate
	selfLoopCond(cond uint8, gateOff int, nextPC uint32) // Bcc to own start: taken->gate, else exit at nextPC
	exitBranch(pc uint32)                                // set PC=pc, return NORM
	exitCondBranch(cond uint8, takenPC, nextPC uint32)   // external Bcc: PC = cond?taken:next, return NORM
	exitLinked(slot uintptr, pc uint32)                  // jump to a published target gate, else dispatch
	exitCondLinked(cond uint8, takenSlot uintptr, takenPC uint32, nextSlot uintptr, nextPC uint32)
	exitBkpt(nextPC uint32)             // set PC=nextPC, return BKPT
	exitBranchLink(link, target uint32) // BL: LR=link, PC=target, return NORM
	exitBranchLinkLinked(link uint32, slot uintptr, target uint32)

	code() []byte // finished block bytes
}

// codeArena is a fixed executable-memory region a native emitter fills with
// translated blocks by bumping off. The three closures are installed by the
// per-host constructor (they wrap VirtualProtect/mprotect and VirtualFree/munmap)
// so the portable lifecycle hooks below can flip protection and release memory
// without importing an OS package here. A nil arena means the native JIT is not
// active on this backend.
type codeArena struct {
	base uintptr // start of the executable mapping
	size uintptr // total bytes reserved
	off  uintptr // next free byte (bump allocator)
	mem  []byte  // backing slice when the host maps one (android mmap); nil on windows

	// commit makes [0,end) usable on a host that reserves the arena without
	// backing it (windows). It reports whether the range is now committed; a
	// host that commits the whole arena up front leaves it nil.
	commit func(end uintptr) bool
	// release returns the mapping to the OS.
	release func()
}

// reserve makes sure the arena is backed up to off+n, growing the committed
// prefix in chunks so a large reservation costs only the pages a title actually
// fills. It reports false when the arena is full or the host refuses to back
// the range.
func (a *codeArena) reserve(off, n uintptr) bool {
	if off+n > a.size {
		return false
	}
	if a.commit == nil {
		return true
	}
	return a.commit(off + n)
}

// nativeBlock is one translated straight-line ARM or Thumb run: entry is the host
// address to call (via the per-host callBlock), and count is the number of
// guest instructions it retires. A native block always terminates at the first
// control-flow instruction (or right before an untranslatable one), so it
// retires exactly count instructions on every invocation ??the invariant the
// Run loop relies on to account retired instructions without the block
// reporting them.
type nativeBlock struct {
	start uint32
	end   uint32
	mode  cpu.Mode
	count int
	entry uintptr
	gate  uintptr
}

type nativeLinkKey struct {
	mode cpu.Mode
	pc   uint32
}

type nativeSlowState struct {
	address  uint32
	count    uint8
	resident bool
}

const nativeSlowThreshold = 3

// nativeCacheSize is the direct-mapped dispatch cache depth: a power of two so
// the index is a mask, sized like the pure-Go JIT's equivalent.
const nativeCacheSize = 8192

// nativeCacheEntry caches one translated block for direct-mapped dispatch. gen
// ties it to the nativeBlocks generation, so an invalidation makes every stale
// entry miss without the invalidation path walking the array.
type nativeCacheEntry struct {
	pc    uint32
	gen   uint64
	block *nativeBlock
}

// nativeCodePageWords covers the whole 32-bit guest address space at 4 KiB
// granularity: 2^20 pages, one bit each.
const nativeCodePageWords = (1 << 20) / 64

// markCodePages records that [address, address+size) now holds translated code
// and reports whether any page became code for the first time.
func (b *Backend) markCodePages(address, size uint32) bool {
	if b.nativeCodePages == nil || size == 0 {
		return false
	}
	changed := false
	last := (uint64(address) + uint64(size) - 1) >> tlbPageBits
	for page := uint64(address) >> tlbPageBits; page <= last; page++ {
		mask := uint64(1) << (page & 63)
		word := &b.nativeCodePages[page>>6]
		if *word&mask == 0 {
			*word |= mask
			changed = true
		}
	}
	return changed
}

// hasCodePages reports whether any page overlapping [address, address+size)
// holds translated code. This is what separates a genuine self-modifying write
// from the guest blitter writing pixels into the same executable image.
func (b *Backend) hasCodePages(address, size uint32) bool {
	if b.nativeCodePages == nil {
		return true // no bitmap: fall back to invalidating
	}
	if size == 0 {
		return false
	}
	last := (uint64(address) + uint64(size) - 1) >> tlbPageBits
	for page := uint64(address) >> tlbPageBits; page <= last; page++ {
		if b.nativeCodePages[page>>6]&(1<<(page&63)) != 0 {
			return true
		}
	}
	return false
}

// nativeInvalidate drops every translated block and rewinds the code arena so
// the memory is reused. It runs on a guest write into executable memory
// (self-modifying / freshly loaded code) and on a remap, mirroring the pure-Go
// JIT's clear(b.jitBlocks). It never runs while a block executes: a native block
// can only store to a page the software TLB's write half holds, and that half
// never holds a page overlapping translated code, so any store that can reach
// here is on the interpreter path with no block on the host stack.
func (b *Backend) nativeInvalidate() {
	if b.nativeBlocks == nil {
		return
	}
	clear(b.nativeBlocks)
	clear(b.nativeARMBlocks)
	for _, slot := range b.nativeLinks {
		slot.Store(0)
	}
	clear(b.nativeSlow)
	b.nativeGen++
	clear(b.nativeCodePages)
	b.nativeCodeLo, b.nativeCodeHi = ^uint32(0), 0
	if b.nativeArena != nil {
		b.nativeArena.off = 0
	}
}

func (b *Backend) nativeLinkSlot(mode cpu.Mode, pc uint32) uintptr {
	key := nativeLinkKey{mode: mode, pc: pc}
	if slot := b.nativeLinks[key]; slot != nil {
		return uintptr(unsafe.Pointer(slot))
	}
	slot := new(atomic.Uintptr)
	b.nativeLinks[key] = slot
	return uintptr(unsafe.Pointer(slot))
}

func (b *Backend) publishNativeLink(mode cpu.Mode, pc uint32, gate uintptr) {
	key := nativeLinkKey{mode: mode, pc: pc}
	slot := b.nativeLinks[key]
	if slot == nil {
		slot = new(atomic.Uintptr)
		b.nativeLinks[key] = slot
	}
	slot.Store(gate)
}

func (b *Backend) clearNativeLink(mode cpu.Mode, pc uint32) {
	if slot := b.nativeLinks[nativeLinkKey{mode: mode, pc: pc}]; slot != nil {
		slot.Store(0)
	}
}

func rangesOverlap(start, end, address, size uint32) bool {
	return size != 0 && uint64(start) < uint64(address)+uint64(size) && uint64(end) > uint64(address)
}

func widthRangeOverlap(start, width, address, size uint32) bool {
	return size != 0 && uint64(start) < uint64(address)+uint64(size) &&
		uint64(start)+uint64(width) > uint64(address)
}

// nativeInvalidateRange removes only blocks whose decoded guest bytes overlap
// the changed range. Emitted bytes remain in the bump arena until the next full
// flush; incoming direct links are disabled before the map entries disappear.
func (b *Backend) nativeInvalidateRange(address, size uint32) bool {
	changed := false
	for pc, block := range b.nativeBlocks {
		overlaps := block != nil && rangesOverlap(block.start, block.end, address, size)
		if block == nil {
			overlaps = widthRangeOverlap(pc, 2, address, size)
		}
		if overlaps {
			b.clearNativeLink(cpu.ModeThumb, pc)
			delete(b.nativeBlocks, pc)
			changed = true
		}
	}
	for pc, block := range b.nativeARMBlocks {
		overlaps := block != nil && rangesOverlap(block.start, block.end, address, size)
		if block == nil {
			overlaps = widthRangeOverlap(pc, 4, address, size)
		}
		if overlaps {
			b.clearNativeLink(cpu.ModeARM, pc)
			delete(b.nativeARMBlocks, pc)
			changed = true
		}
	}
	if changed {
		b.nativeGen++
	}
	return changed
}

func (b *Backend) noteNativeBail(mode cpu.Mode, pc, address uint32) {
	key := nativeLinkKey{mode: mode, pc: pc}
	state := b.nativeSlow[key]
	resident := b.tlbHit(address, cpu.PermissionRead) ||
		b.tlbHit(address, cpu.PermissionWrite)
	// The first miss on a directly backed page is just TLB warm-up. A second
	// bail at the same now-resident address is persistent (normally a crossing
	// check or a deliberately non-writable code page) and counts toward the
	// interpreter-boundary threshold. MMIO never becomes resident and counts
	// immediately.
	if resident && (!state.resident || state.address != address) {
		state.count = 0
	} else if state.count < nativeSlowThreshold {
		state.count++
	}
	state.address, state.resident = address, resident
	b.nativeSlow[key] = state
	if state.count == nativeSlowThreshold {
		width := uint32(2)
		if mode == cpu.ModeARM {
			width = 4
		}
		b.nativeInvalidateRange(pc, width)
	}
}

func (b *Backend) nativeSlowAt(mode cpu.Mode, pc uint32) bool {
	return b.nativeSlow[nativeLinkKey{mode: mode, pc: pc}].count >= nativeSlowThreshold
}

// nativeCloseArena releases the executable mapping. It is called from Close.
func (b *Backend) nativeCloseArena() {
	if b.nativeArena != nil && b.nativeArena.release != nil {
		b.nativeArena.release()
	}
	b.nativeArena = nil
}
