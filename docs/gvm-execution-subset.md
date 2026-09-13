# GVM execution subset and remaining startup boundary

Date: 2026-09-12. This is implementation progress toward game startup, **not
completed GVM game execution**. The ordinary product still reports recognition
without execution until the runtime, media and event contracts are implemented.
No proprietary payload, interpreter body, palette table or game asset is tracked.

The [2026-09-13 follow-up](gvm-brew-followup-20260913.md) adds explicit
file/RAM address spaces, shared symbol RAM and operations `15`, `1f`, `4d`.
The checkpoint and observations below describe the earlier 17-opcode revision.

## Reference qualification

Static contracts refer only to the supplied GVM2X PE SHA-256
`c2c170b3c47cd99dc55fdadad31dd1927473b4362868205c5eb49eff96c60719`.
The reference executable was not run or modified. Original synthetic tests and
normalized observations are separate from native bytes. This follows
[the entry investigation](reference-followup-20260912.md).

The selected-slot path writes and reads through a symmetric bytewise storage
transform. The two operations cancel for all 256 byte values. Successful normal
storage/reload and event delivery preserve the original buffer from offset zero.
A tail jump missed in the earlier call-only scan connects the completed reload
to the primary-entry wrapper. This establishes a conditional static chain, not
proof that OS timer delivery, short reads or storage failures behave correctly.
Compressed/prefixed variants remain outside this implementation.

## Decoded storage layout

`loader/gnex.DecodeExecutionImage` is deliberately separate from recognition and
factory selection. It accepts only an unprefixed, observed normal v2 layout
(version byte 2, byte2=12, byte5=1), bounded to 128 KiB. The fields below are
little-endian uint16 **offsets**, not counts:

| Field | Meaning |
|---|---|
| 0x1c | Primary entry relative to the complete buffer, zero means absent callback |
| 0x2c / 0x2e | Symbol descriptor start / symbol data start |
| 0x30 / 0x32 | Media descriptor start / media data start |

Each descriptor is four bytes. Symbol count is the first offset difference /4,
media count is the second /4. A symbol descriptor uses byte0 for allocation,
byte1 for word count, and byte2 for initialization when allocated. Zero byte0
aliases file storage and consumes 2*count bytes. Allocated symbols consume file
bytes only when initialized, otherwise they start zero-filled. Byte3 is not given
an invented meaning. Media descriptors use byte0 for allocation, byte1 as an
opaque tag and LE16 at+2 as byte length. All media consume file bytes.

The observed runtime accounts six bytes per symbol and eight per media record,
plus allocated contents, within 16 KiB RAM. The Go decoder reproduces storage
ownership rather than native addresses. It preserves original descriptor Type
bytes, while the reference runtime normalizes nonzero allocation types to1.
BufferOffset explicitly distinguishes buffer aliases from independent RAM.

Ordering, alignment, file bounds and region-overlap rejection are additional
emulator safety policy. They are not claims of safe native validation. Input
ownership, alias sharing, initialization, zero-fill, malformed spans and RAM
limits have independently authored tests and a bounded fuzz smoke.

Reserved symbols, dimensions, mode flags and a fixed runtime string are not
initialized by this decoder. Tags are not decoded image/audio formats. A
successful decode is neither a fully initialized VM nor a game-start milestone.

## Independent bounded instruction kernel

The new pure-Go `gvm` package executes caller-selected buffer-relative code.
`NewAt` retains the entire buffer so absolute branch offsets are not accidentally
rebased to a sliced entry. `NewWithSymbols` binds validated program aliases or
independent RAM without losing shared storage. Constructors and public snapshot
accessors do not expose caller-owned state to mutation by another owner.

The dispatcher consumes one opcode byte before handler processing. Verified
immediates are signed8 (05) and **big-endian16** (06), despite little-endian
header/storage fields. Arithmetic12/13/14 wraps to16 bits. Opcode00 is NOP,
not stop. Branches41/42/43 and call44 use big-endian16 buffer-relative targets.
Conditional branches consume their condition on either valid path. Symbol04
loads the first little-endian16 slot. Symbol36 stores a signed8 immediate into
that slot; indexed symbol31 does the same for a validated element. The verified
96/97 adapters pop one operand and call a native function whose body returns
without another effect in this exact reference build. This is a hash-qualified
observed effect, not a blanket success stub for unknown host APIs.

The native pre-increment guards permit65 operand slots and17 return-address
slots starting with top=-1. Bounds/underflow faults and deterministic budgets
are explicit emulator safety decisions. Faults are sticky and never count as
successful halts. Kernel failure state is intentionally bounded/transactional
rather than reproducing native pointer corruption or an out-of-buffer sentinel.
Guest ff ends a dispatch after its byte is consumed. Unimplemented opcodes
return an opcode-and-offset error, never a fabricated successful result.

The public API is not a Machine implementation. It has no renderer, input
service, media decoder or save-state format. Handler45 supports a nonempty
return stack, restoring its validated buffer-relative return address. Empty45,
handler46 and native error-sentinel details remain explicitly unsupported rather
than silently mapped onto ordinary guest ff.

## Remaining acceptance work

Real startup requires reserved-symbol initialization, enough numeric and symbol
operations for the selected game, callback/event scheduling and evidenced
media/palette/presentation contracts. In particular, a verified screen-fill
adapter does not justify inventing RGB values from an undecoded remap table.
The ordinary product remains recognized-only for GVM, and BREW bootstrap and
services also remain incomplete. Synthetic dispatch and lower-level original
buffer checks cannot replace an actual game screen and input response.

## Frozen checkpoint acceptance (2026-09-12)

| Requirement | Check and observed result |
|---|---|
| Selected descriptor layout and ownership | `loader/gnex` synthetic alias/copy/zero-fill, malformed offsets, RAM bounds and fuzz tests pass. Eight authorized v2 buffers decode, one v1 is explicitly unsupported. |
| Numeric execution, symbol mutation and failures | `go test ./gvm -count=1 -cover` passes with 100% statement coverage. Unknown instructions remain typed failures. Fuzz and 386 tests pass. |
| Decoder/kernel boundary | Public decoder plus `NewWithSymbols` tests pass for aliased and allocated symbols, call/return and resumable budgets without mutating caller buffers. |
| Real-buffer progress, not game startup | Descriptor-only execution of SHA c4f6ade5193dec699654fc2cb488af4cd131152fe14fa27c384c1d36ec47656e completes eight instructions, then rejects opcode 0x1f at offset 415. Reserved runtime initialization, services and frames are absent. |
| Ordinary product compatibility | Ordered core, runner, frontend and integration tests/vet pass. Synthetic gate passes 20/20. Same-scope deltas are 21 unchanged synthetic/reference rows and 496 unchanged private-inclusive rows. Ordinary GVM remains recognized-only. |
| Configured private gate | Exits 1 because six existing KTF/Raptor reference prerequisites cannot find valid packages in the supplied legacy corpus. This is not a passing full private gate. |
| Platform builds | Windows product/probe/frontend and pure-Go core Android arm64, Linux amd64 and Darwin arm64 builds pass. Mobile binder is tracked separately. |

These checks validate a partial foundation. They do not satisfy the requested
J2ME/BREW/GVM all-platform game-startup milestone or establish reference-screen
equivalence. No original payload bytes are tracked.
