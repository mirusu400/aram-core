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
	nativeArenaSize    = uintptr(8 << 20) // executable arena bytes before a flush
)

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

	// Terminators end a block. exit* set PC and return to the Go dispatcher;
	// selfLoop* jump back to the gate offset (staying in native code).
	selfLoopUncond(gateOff int)                          // B to own start: jump to gate
	selfLoopCond(cond uint8, gateOff int, nextPC uint32) // Bcc to own start: taken->gate, else exit at nextPC
	exitBranch(pc uint32)                                // set PC=pc, return NORM
	exitCondBranch(cond uint8, takenPC, nextPC uint32)   // external Bcc: PC = cond?taken:next, return NORM
	exitBkpt(nextPC uint32)                              // set PC=nextPC, return BKPT

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

// nativeInvalidate drops every translated block and rewinds the code arena so
// the memory is reused. It runs on a guest write into executable memory
// (self-modifying / freshly loaded code) and on a remap, mirroring the pure-Go
// JIT's clear(b.jitBlocks). It never runs while a block executes: v1 native
// blocks never write guest memory (stores fall back to the interpreter), so a
// store that triggers this is always on the interpreter path with no block on
// the host stack.
func (b *Backend) nativeInvalidate() {
	if b.nativeBlocks == nil {
		return
	}
	clear(b.nativeBlocks)
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
