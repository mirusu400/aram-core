# CPU backends: precise oracle + optional fast core

ARAM runs guest ARM/Thumb code through a `cpu.Backend`. The architecture is a
two-tier split: one **precise** core that defines correct behavior, and any
number of optional **fast** cores that must reproduce it exactly. This document
specifies the seam, how a fast backend is validated and selected, and the
per-target policy. See also [portability.md](portability.md).

## The seam

`cpu.Backend` (`cpu/backend.go`) is the whole contract: `Map`, `ReadMemory`,
`WriteMemory`, `Read/WriteRegister`, `Run(ctx, pc, mode, budget) Result`,
`Stop`, `SaveContext`, `RestoreContext`, `Close`. It is Unicorn/dynarmic-shaped
(map memory, read/write registers, run on an instruction budget), so a native
recompiler satisfies it without new interface surface.

The machine never constructs a CPU directly; `Factory.NewCPU CPUFactory`
(`application/machine.go`) is the single injection point. Every runtime
(KTF/Raptor/WIPI/minigame) takes a `cpu.Backend` — nothing type-asserts to the
interpreter.

## Precise core = the oracle

The portable interpreter (`cpu/interpreter`, identity `portable-interpreter`) is
the accuracy reference. It is pure-Go, deterministic, and the only backend
guaranteed on every target, so it is the **default and the fallback** and the
permanent differential-testing oracle. It is never deleted once a fast core
lands (the higan/ares principle: keep a first-class accuracy core).

There is **no cycle model** in ARAM: the virtual clock advances by a fixed frame
quantum, and guest time (`TickMS`) is sampled from it, never from cycles or
retired instructions. So "precise" here means **instruction-semantics fidelity +
exact accounting granularity**, not cycle accuracy:

- bit-exact N/Z/C/V flags, shifter carry (incl. RRX and shift-by-≥32), ARMv5
  interworking, LDM/STM base-in-list quirks, PC-read offsets, MUL leaving C/V;
- exactly one retired instruction counted per guest instruction;
- the exact stop-PC and `StopBudget` cutoff, and precise faults on undefined
  encodings (Thumb-2 32-bit is unsupported in *both* cores — a shared gap, not a
  precise-vs-fast trade).

## Fast core = a black box between sync points

A fast backend (a recompiler, or a native core such as Unicorn/dynarmic behind
build tags) may do anything internally — block translation and chaining, lazy
flags, register allocation, coarser cancellation polling — **as long as it is
indistinguishable from the interpreter at every sync point** (each `Run`
return: a BKPT host-call, `StopBudget`, `StopFault`, or `Stop`/cancel). At a
sync point these must match exactly: all 17 registers incl. CPSR, `mode`, every
mapped memory byte, `Result.{Reason, PC, Instructions}`, and — once the context
format is shared — the `SaveContext` bytes. `Result.Instructions` is
load-bearing (frame pacing and runaway detection are denominated in retired
instructions), so optimizations must never drop instructions from the count, and
a block must clamp to the remaining budget rather than overshoot.

## Differential testing (`cpu/conformance`)

`cpu/conformance` is the honesty mechanism, modeled on dynarmic's diff-against-
Unicorn harness and Dolphin/RPCS3 desync detection:

- `Execute(newBackend, program)` runs a program + initial state on a backend and
  captures a `Snapshot` (17 registers, a scratch-memory window, the `Result`).
- `Diff(oracle, subject)` reports the first architectural divergence, or "".
- `Corpus` is a curated Tier-1 set targeting the corners a fast core most easily
  gets wrong (carry/overflow survival, ADC chains, shift carry, MUL C/V,
  conditional branches, memory transfers, ARM/Thumb).

Tiers, cheapest first:

1. **Instruction-level** — run `Corpus` (and random streams) on
   `interpreter.New` vs the fast backend; assert identical snapshots.
2. **Corpus lockstep** — run whole titles from `ARAM_TEST_DATA` on both cores
   frame-by-frame and diff architectural state at every sync point; the first
   divergence localizes the bug to one block.
3. **Observable output** — end-of-frame framebuffer hash + host-call trace.

Any divergence is a hard failure and pins that title (by image SHA-256) to
`precise` until fixed.

## Selection

Backends are chosen by name through a process-global registry
(`application/cpu_select.go`):

- `precise` / `portable` / empty → the interpreter (always available).
- `RegisterCPUBackend(name, factory)` — a native/cgo core registers itself from
  a **build-tagged** file, so the pure-Go core never imports it. On a target
  where that file is not compiled, nothing registers the name.
- `ResolveCPUBackend(name)` returns the factory or `ErrBackendUnavailable`.
- `NewFactory` consults the `ARAM_CPU` environment override and **falls back to
  the precise interpreter** when it is unset or names a backend absent from this
  build — the product stays runnable on every target.

Default is `precise` everywhere. A fast core is opt-in until it passes the
differential gate broadly, then may become the default for a specific
desktop profile only (iOS/WASM/Android stay on the interpreter).

## Adding a fast backend (drop-in contract)

1. Implement `cpu.Backend` in its own package. A native core (Unicorn,
   dynarmic) lives behind `//go:build cgo && <tag>` with a pure-Go stub for
   other builds; a pure-Go recompiler is always compiled.
2. Honor the host-call ABI: terminate `Run` exactly at a BKPT and report
   `PC == trap+2`, materialize all registers/memory at every return, resume from
   an arbitrary LR, and count retired instructions like the interpreter.
3. Register it: `application.RegisterCPUBackend("fast", newFastCPU)` from an
   `init` in the build-tagged file.
4. Validate: pass all three conformance tiers against the interpreter oracle
   before shipping it as selectable; keep it non-default until the corpus is
   clean.

Native JIT that emits host machine code is **out of scope** for the shipped
targets: it breaks the pure-Go `CGO_ENABLED=0` Android build, iOS forbids JIT,
and Go has no runtime code emitter. The order-of-magnitude path is a cgo
recompiler on desktop only; mobile speed comes from interpreter-level work
(straight-line batching, lazy flags, and a decoded-block cache), which stays
pure-Go and benefits every target.
