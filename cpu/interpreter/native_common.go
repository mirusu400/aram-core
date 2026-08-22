package interpreter

// This file holds the portable, build-tag-free pieces of the optional native
// JIT backend (see native_windows_amd64.go / native_android_arm64.go for the
// per-host machine-code emitters). It compiles on every target so the shared
// Backend struct, Run dispatch, and SMC/lifecycle hooks reference the same
// types everywhere; the emitters themselves are behind //go:build tags and a
// stub keeps non-native builds (Android arm64 cross-check aside) clean.
//
// The native JIT is ARAM's third CPU execution strategy behind the same
// cpu.Backend as the tree-walking interpreter (the accuracy oracle) and the
// pure-Go closure JIT. It translates a straight run of Thumb instructions into
// host machine code once, caches it, and re-runs it, falling back to the
// interpreter one instruction at a time for memory access, ARM, and anything it
// does not translate ??so it is always correct and validated bit-for-bit
// against the interpreter by cpu/conformance. Its value is speed on hosts where
// emitting native code beats Go dispatch; iOS (JIT forbidden) never compiles an
// emitter and always falls back to the precise interpreter.

const (
	nativeMaxBlock     = 256              // guest instructions per translated block
	nativeStatusNorm   = 0                // block completed; resume dispatch at regs[PC]
	nativeStatusBKPT   = 1                // block hit BKPT; stop with StopBreakpoint
	nativeStatusBudget = 2                // remaining budget < next block; interpret the tail
	nativeStatusBail   = 3                // software-TLB miss; interpret this one instruction
	nativeArenaSize    = uintptr(8 << 20) // executable arena bytes before a flush
)

// nativeBailStatus packs the block status a software-TLB miss returns: the low
// byte is nativeStatusBail and the rest is retired, the number of instructions
// the block had already retired when it bailed. The budget gate at the top of a
// block subtracts the WHOLE block's instruction count up front, so the Run loop
// needs retired to give back the part that never ran.
func nativeBailStatus(retired int) uint32 {
	return uint32(nativeStatusBail) | uint32(retired)<<8
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
	absolute bool   // the address is exactly offset (PC-relative literal load)
}

// emitter appends host machine code for one translated Thumb instruction at a
// time. Each method hides the host's scratch-register choreography; the decoder
// (emitThumb, native_jit.go) only extracts operand fields and calls these. A
// block is a leaf host function that takes &regs[0], mutates the 17-word
// register file in place (regs[16] is CPSR with eager N/Z/C/V), and returns a
// status (see nativeStatus*). Register/ALU classes update flags to match the
// interpreter exactly; memory, ARM, and unhandled ops are never offered here
// (emitThumb bails, ending the block), so a native block never faults mid-run.
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

	// memory translates one single load/store inline through the software TLB
	// (native_tlb.go): address computation, a direct-mapped page probe, a
	// page-crossing check, then the host access. On a miss the emitted code
	// leaves PC at pc and returns nativeBailStatus(retired), so the Run loop
	// restores the budget the gate over-subtracted and hands exactly this
	// instruction to the interpreter - which installs the page, so the next
	// execution hits. Nothing else in the block is disturbed, so a bail is a
	// slow path, never a correctness fork.
	memory(a memAccess, pc uint32, retired int)

	// Terminators end a block. exit* set PC and return to the Go dispatcher;
	// selfLoop* jump back to the gate offset (staying in native code).
	selfLoopUncond(gateOff int)                          // B to own start: jump to gate
	selfLoopCond(cond uint8, gateOff int, nextPC uint32) // Bcc to own start: taken->gate, else exit at nextPC
	exitBranch(pc uint32)                                // set PC=pc, return NORM
	exitCondBranch(cond uint8, takenPC, nextPC uint32)   // external Bcc: PC = cond?taken:next, return NORM
	exitBkpt(nextPC uint32)                              // set PC=nextPC, return BKPT
	exitBranchLink(link, target uint32)                  // BL: LR=link, PC=target, return NORM

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

	// protectRW makes the whole arena writable (W^X: called before emitting a
	// block), protectRX makes it executable and flushes the i-cache (called
	// after), and release returns the mapping to the OS.
	protectRW func()
	protectRX func()
	release   func()
}

// nativeBlock is one translated straight-line Thumb run: entry is the host
// address to call (via the per-host callBlock), and count is the number of
// guest instructions it retires. A native block always terminates at the first
// control-flow instruction (or right before an untranslatable one), so it
// retires exactly count instructions on every invocation ??the invariant the
// Run loop relies on to account retired instructions without the block
// reporting them.
type nativeBlock struct {
	start uint32
	count int
	entry uintptr
}

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

// markCodePages records that [address, address+size) now holds translated code.
func (b *Backend) markCodePages(address, size uint32) {
	if b.nativeCodePages == nil || size == 0 {
		return
	}
	last := (uint64(address) + uint64(size) - 1) >> tlbPageBits
	for page := uint64(address) >> tlbPageBits; page <= last; page++ {
		b.nativeCodePages[page>>6] |= 1 << (page & 63)
	}
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
	b.nativeGen++
	clear(b.nativeCodePages)
	b.nativeCodeLo, b.nativeCodeHi = ^uint32(0), 0
	if b.nativeArena != nil {
		b.nativeArena.off = 0
	}
}

// nativeCloseArena releases the executable mapping. It is called from Close.
func (b *Backend) nativeCloseArena() {
	if b.nativeArena != nil && b.nativeArena.release != nil {
		b.nativeArena.release()
	}
	b.nativeArena = nil
}
