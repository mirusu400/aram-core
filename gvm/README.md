# Bounded GVM bytecode kernel

This independent pure-Go interpreter **subset is not game startup**, an SGS
loader, resource decoder, or factory adapter. Tests use only independently
authored synthetic literals. No proprietary bytes are included.

## Evidence boundary

Research worker bird (`session_bird_1789223070871_ca64fbf46e7c08d9`) supplied
normalized static contracts on 2026-09-12 for the GVM2X build with SHA-256
`c2c170b3c47cd99dc55fdadad31dd1927473b4362868205c5eb49eff96c60719`.
These facts are build-specific, not dynamic handset conformance. See
[CODEX](../CODEX.md), [GNEX research](../docs/gnex-format.md),
[reference evidence](../docs/reference-emulators-20260912.md), and
[conditional entry mapping](../docs/reference-followup-20260912.md).
Bird confirmed direct verifier assertions for `31` (111 total), both `96/97`
wrappers and empty callee (95 total), and nonempty `45` (113 total). These are
research-verifier totals, not package-test counts or game-success measurements.
The subsequent direct `04` checks passed in a 156-assertion verifier run.

Let B be the beginning of the whole caller buffer, P the position immediately
after the fetched opcode, S the operand stack, and R the saved-PC stack.
Every fetched opcode advances PC before its handler.

| Opcode | Normalized successful behavior |
| --- | --- |
| `00` | No operation, PC=P |
| `03` | u8 symbol, u8 element; push unchanged LE16 at symbol+2*element, PC=P+2 |
| `04` | u8 symbol, push its first rawLE16 slot, PC=P+1 |
| `05` | Push signed i8 extended to raw16, PC=P+1 |
| `06` | Push BE16, PC=P+2 |
| `09` | u8 symbol, u8 element; store raw top16 as LE16 at symbol+2*element, pop once, PC=P+2; depth65 rejected |
| `0a` | u8 symbol; store raw top16 as LE16 at symbol start, pop once, PC=P+1; depth65 rejected |
| `0e` | Decrement top16 modulo65536 without popping or operands, PC=P; depth65 accepted |
| `12` | Replace a=S[top-1], b=S[top] with low16(a+b) |
| `13` | Replace a,b with low16(a-b) |
| `14` | Replace a,b with low16(a*b) |
| `15` | Signed32 division of signextended16 a/b, truncating toward zero, retain low16 |
| `1f` | Signed16 a>=b, replacing a,b with canonical zero or one |
| `31` | u8 symbol, u8 element, signed i8 immediate; store signextended LE16 at symbol+2*element, PC=P+3 |
| `36` | u8 symbol, signed i8 immediate; store signextended LE16 at symbol start, PC=P+2 |
| `3a` | u8 symbol, signed i8 delta; add delta modulo65536 to its first LE16 word, stacks unchanged, PC=P+2 |
| `3c` | Pop16; if signed16(top)<signed8(P), jump B+BE16(P+1), otherwise P+3 |
| `3d` | Pop16; if signed16(top)>=signed8(P), jump B+BE16(P+1), otherwise P+3; equality branches |
| `3e` | Pop16; if signed16(top)<=signed8(P), jump B+BE16(P+1), otherwise P+3; equality branches |
| `41` | PC=B+BE16(P) |
| `42` | Pop16, jump B+BE16(P) if nonzero, otherwise P+2 |
| `43` | Pop16, jump B+BE16(P) if zero, otherwise P+2 |
| `44` | Save P+2 on R, jump B+BE16(P) |
| `45` | Nonempty R only: restore top saved PC exactly and pop it |
| `4c` | Configured address model only: u8 symbol, u8 element; push encoded address of symbol+2*element, PC=P+2 |
| `4d` | Configured address model only: u8 symbol, push its encoded region-relative word address, PC=P+1 |
| `4f` | Configured address model only: store raw top16 as LE16 at RAM+2*signed16(next-to-top), pop two, PC=P |
| `51` | Explicit service constructor only: write four device-query words through a tagged reference and pop once |
| `96`, `97` | Pop16 argument, call a ret-only native callee in this build, PC=P |
| `b4` | Configured address model only: scalar operation on a tagged word array, pop four, PC=P |
| `b9` | Explicit service constructor only: write local hour/minute/second/millisecond from one virtual-clock sample and pop once |
| `ff` | Exit current dispatch, PC=P |

Immediate stores `31/36` and returns do not alter S; `0a` pops its stored value.
The signed operand top starts at -1, and
the observed pre-push top>=0x40 check allows **65** values, not64. Saved-PC top
also starts at -1, and its pre-call equality check top==0x10 allows **17** saved
PCs, not16. Internal lengths are equivalent on reachable states. A defensive
>= capacity check additionally prevents writes if an invariant is broken.

Empty `45` and all `46` remain typed unsupported. The native static `ff`
sentinel is not a guest-buffer byte. No sentinel address, diagnostic-code
meaning, or synthetic stop lifecycle is invented. All other unimplemented
opcodes, including `f3..fe`, produce `*UnsupportedOpcodeError{Opcode, Offset}`,
never a guessed NOP. `96/97` are not guessed service stubs: direct assertions
establish their ret-only callee for this hash. No graphics or other-build
service semantics are inferred.

## Explicit host memory bindings

- `New(program)` copies the buffer and starts at zero. Empty input faults on
  first fetch. `NewAt(program, entry uint32)` validates an in-bounds entry and
  copies the **whole** buffer. Targets remain B-relative, not entry-relative.
  Neither constructor parses SGS or applies its special entry-zero wrapper.
- `NewWithSymbols(program, entry, []SymbolRegion)` adds explicit host bindings.
  `SymbolRegion` has `ProgramOffset *uint32`, `Length uint32`, `Initial []byte`.
  Nonnil offset selects a mutable program span and requires nil Initial.
  Nil offset selects independent RAM and requires Length==len(Initial).
- All spans are checked using widened arithmetic before VM storage allocation.
  Program is copied **once**, then aliased symbol views bind into that copy.
  Overlapping views share stores, including changes to future instruction
  fetches. Each RAM binding receives an independent copy, even if inputs alias.
- `Program()`, `Symbol(index uint8)` and `Stack()` return independent copies.
  They are inspection methods, not a serialized VM save-state schema.
- No native pointers, SGS record parser, reserved runtime symbols, read-only
  policy, services, or materialization rules are inferred by this package.

## Configured file/RAM address spaces

`NewWithAddressSpace` takes explicit `FileStart`, `FileLength`, a coherent
`RAM` slice and `AddressSymbol` bindings. `AddressFile` and `AddressRAM` offsets
are relative to their region, not the whole program or a guessed minimum alias.
Program and RAM are each copied once, preserving overlapping symbol views.
Old constructors do not silently acquire this model and keep `4c/4d` unsupported.

`4c` uses the same region-relative encoding as `4d`, but selects a word by
an unsigned element operand without reading its payload. The host requires
both operands before capacity, symbol, shape and element checks. Bindings use
the same explicit even-length, at-most510-byte count representation as `03/31`.
A full selected word must fit the matched region. These bounds and sticky
transactional failures are host safety policy, not native error-helper parity.
Odd byte-offset rounding, low16 truncation and file-tag collisions remain
unchanged. Public constructor tests cover these rules and legacy rejection.

An in-place nine-input descriptor-only comparison after this addition advanced
the selected SHA-256 `c4f6ade5193dec699654fc2cb488af4cd131152fe14fa27c384c1d36ec47656e`
from293 to318 completed instructions, then reached unsupported `09` at906.
The other eight observations were unchanged. This does not apply reserved
initialization or prove a product frame, startup, or input response.

`4d` shifts the bound byte offset right by one, keeps low16 and sets `0x4000`
for file storage. Odd offsets, truncation and tag collisions are not corrected.
`ReadWord` selects the tagged region, interprets its word index as signed16,
checks the global region limit and full two-byte span, then returns LE16.
It can read across descriptor boundaries within that region. It is a scalar
inspection API, not a host pointer or a guest service. See the
[2026-09-13 contract follow-up](../docs/gvm-brew-followup-20260913.md).

`4f` has no inline operands, helpers or services in its independently verified
nine-instruction handler. It uses a direct signed16 RAM word index below the
raw top16 value, not `ReadWord` tagging: positive `0x4000` selects RAM word16384,
never file storage. Only the configured constructor supplies that global RAM;
legacy constructors retain typed unsupported behavior. Host checks configured
state first, then depth>=2 and a nonnegative full-word RAM span before any write
or pop. Failures are sticky and transactional except opcode fetch. Clearing
popped backing slots is host hygiene, not native behavior. Native unchecked
pointer faults/aliases and complete loader lifecycle are not reproduced.

Division by zero produces sticky `ErrDivideByZero` without changing operands.
This differs deliberately from the native error-helper cleanup/pop path.
The widened division makes `-32768 / -1` wrap to `0x8000` without a host trap.

## Explicit device and civil-clock services

`NewWithAddressSpaceAndServices` is an opt-in constructor, not a factory or
startup adapter. Existing constructors retain typed unsupported `51/b9`.
Nil service config deliberately provides neither service, with precise unavailable
errors after stack/address validation. Device query and clock are independent.
No configuration is detected from a title, host registry, environment or clock.

`DeviceQueryProfile` is copied into private state. Width/Height1..256 are an
explicit portable safety range, not a claimed native lower-bound check.
AudioType retains all32 bits for the verified mask transform and does not enable
audio playback. The four-word query record is computed from these explicit
values, never hardcoded from one reference configuration.

Clock configuration requires a borrowed shared `runtime.Clock` and explicit
`FixedOffsetNoDST` policy together. The owner serializes execution and clock
updates. A query takes one snapshot, does not advance time or consume RNG, and
requires UTC and local milliseconds in0..2147483647999. Epoch0 is selectable
through validated Clock.Restore; NewClock(0) otherwise normalizes its own default.
Fixed offset conversion is emulator policy, not Windows timezone/DST parity.

Both operations require one stack value and a complete eight-byte writable
tagged-arena span before provider/conversion checks. All four results are
prepared before any write/pop. Errors remain sticky and transactional after
opcode fetch. File/code aliases are preserved, but native host-global aliases
are never exposed. Service configuration is immutable apart from the explicitly
borrowed clock. This adds no complete VM snapshot, reset, scheduler, presentation,
or game-start contract.

## Reference store checks versus emulator safety

`0e` changes only the existing top word. The hash-qualified native three-
instruction handler has no stack guard; the host adds sticky underflow rejection
before mutation. There is no upper guard or pop, and the maximum supported stack
depth65 remains valid. Program/RAM and saved return addresses are unchanged.

`09` uses unsigned symbol and element operands and the same exact even-span
count representation as `03/31`. Host validation is both operand bytes, upper
stack capacity, symbol, shape, element, then lower-stack availability. A valid
element must fit the descriptor itself, not an unrestricted global fallback.
Operands and value are cached before aliasing writes. Success stores raw LE16
and pops once. Failures remain sticky and transactional after opcode fetch,
unlike the reference's partial PC publication and host diagnostic cleanup.

`b4` consumes `[destination reference, scalar, signed count, selector]`, deepest
first. Selectors0..11 are assign, add, subtract, multiply, signed divide,
signed remainder, AND, OR, assign NOT scalar, XOR, arithmetic right shift and
left shift. Results retain low16 and shifts use scalar's low5 bits. Positive
counts visit ascending words. Nonpositive counts write nothing, but zero
division/remainder scalars still fault. Both use `ErrDivideByZero`, a host
policy rather than the reference's distinct diagnostic numbers.

Only explicit configured arenas support `b4`. Host checks configuration,
four stack values, a complete starting word, selector0..11, divisor and the
whole positive-count span before mutation. The starting-word check applies
even to nonpositive counts. `ErrInvalidArraySelector` rejects all unverified
selectors. Tagged spans may cross descriptor boundaries but not their global
region. Cached arguments preserve code aliases without exposing native VM
globals. This is deterministic array processing, not a clock, audio or graphics
service, and proves no presentation or startup milestone.

`0a` is a direct symbol store, not an indexed load. The same hash-qualified
reference checks signed top>=64 after consuming its u8 index, despite popping
on success. Thus depth64 succeeds and depth65 fails. It reads only the symbol
pointer, not descriptor type or element count. No even-length or 510-byte
shape restriction is inferred. Configured address-space stores validate a full
two-byte span in the selected global file/RAM region, allowing writes across
descriptor boundaries and from empty descriptor views with valid backing.
Legacy independent bindings instead require their own slice to contain two
bytes. Neither mode permits a word past the backing region's end.

Host check order is operand availability, upper-stack guard, symbol index,
destination full span, then lower-stack underflow. All failures are sticky and
transactional except opcode fetch. This differs from native operand-PC
publication and its terminal helper/host-cleanup path. Successful writes retain
shared aliases and can change future instruction fetches. No raw native pointer
or cleanup behavior is introduced.

`03` is independently hash-verified across its complete 62-instruction handler.
It consumes two unsigned bytes, checks capacity, symbol index, the element count
stored at descriptor byte offset1, and starting-address membership, then pushes
raw LE16. It does not read the descriptor type byte. The count is not incremented.
The host eagerly requires both operands, then checks capacity and symbol index.
As with `31`, bindings must have even length at most510 and count exactly len/2;
invalid shape and element errors remain distinct. This representational policy
and complete-word containment are not native metadata or error-helper claims.
Both constructors retain transactional failures with PC=opcode+1. Native partial
PC/stack publication and transitive error-helper effects are not reproduced.

`04` fetches its u8 symbol index, then checks stack capacity before symbol
index and pointer range, and pushes the first rawLE16 slot. It has no native
wordcount or type guard. The host checks the complete two-byte region and
keeps transactional PC behavior on failure. Missing index precedes the
capacity check, unlike the immediate-only pushes `05/06`.

`36` consumes u8 symbol before validation, checks it against native count,
loads the pointer at offset2 of a six-byte record, checks that starting pointer
against either permitted native range, then reads signed i8 and stores LE16.
It does not prove full-write bounds, record type, or writability. The kernel
instead resolves an explicit binding, checks its full two-byte span, and then
checks immediate availability. Missing index is truncation.

`31` consumes u8 symbol and u8 element before validating symbol, then checks
element against the authoritative uint8 descriptor word count. It checks the
selected pointer's permitted native range before reading signed i8 and storing
LE16. Host bindings must exactly model the descriptor span: executing `31`
requires an even region length at most510, with count exactly len/2. Odd or
oversized shapes produce `ErrInvalidSymbolRegion`, never a modulo count.
Out-of-range elements produce `ErrInvalidElement`. This shape restriction is
host policy, not a claim that native code rejects odd buffers. `36` continues
to allow any bound region containing at least two bytes.

`3a` is a separately verified in-place LE16 add with signed8 delta and wrapping
arithmetic, not a branch or saturating operation. It has no stack effects or
capacity checks and reads no descriptor type/count. Host policy eagerly caches
both operands, validates symbol and full word, then writes and advances PC.
Configured bindings use their full selected global file/RAM region, permitting
empty/one-byte views with valid backing; legacy slices require two bytes.
No even-length or maximum-shape restriction is imposed. Cached delta and old
word preserve operand/code aliases. Native index-first consumption and opaque
error-helper host side effects are deliberately not reproduced by sticky faults.

All handler failures are transactional except the already-completed opcode
fetch: PC stays at opcode+1, with no operand, stack, or memory changes. This
**differs from native store failure paths**, which consumed index bytes before
validation and then selected a static sentinel. No native recovery is claimed.

## Bounded execution and fault policy

The hash-qualified `3c` handler was independently checked on 2026-09-13.
Its encoding is four bytes including the opcode. Equality falls through, and
both paths pop once without touching guest memory or the saved-PC stack.
It does not use tagged addresses or require a configured address space.
Native code publishes the pop before reading a taken target and skips target
reads when not taken. The kernel instead eagerly requires all three immediate
bytes and validates a taken target before committing the pop. These are
explicit safety-policy differences, not observed native error behavior.
This static contract does not establish handset fidelity or game startup.

`3e` was independently verified against the same hash, rather than inferred
from its neighboring opcode. Its four-byte encoding and single-pop behavior
match this branch form, but its signed comparison is **less than or equal**.
The native JG selects fallthrough, so equality takes the absolute BE16 target.
It has no helper calls or guest-memory/saved-PC stores. The same eager
three-byte validation, underflow and taken-only target checks are explicit
host policy; native code publishes its pop before lazy target reads. The
checker covers the complete handler, all signed16/signed8 comparison pairs,
and all BE16 target values. These checks are static specification evidence,
not reference execution or a product-startup milestone.

`3d` is separately hash-verified, not inferred from those neighboring branches.
Its signed predicate is **greater than or equal**: native JL selects
fallthrough, so equality takes the unsigned BE16 whole-buffer target. The
four-byte encoding, one pop on both paths, and lack of helpers, guest-memory
or saved-PC stores were checked across the complete handler. The same eager
operand/underflow/taken-target guards and atomic commit are host safety policy,
not the native early pop and lazy target reads. `3c` and `3e` retain their
separate predicates.

- `Step()` executes at most one instruction. `Run(uint64)` executes at most its
  instruction budget. `ErrBudget` is resumable, not sticky. Zero budget is
  inert: halted returns nil, faulted returns its fault, otherwise ErrBudget.
  Reaching `ff` on the last budget step succeeds.
- `Halted()` means guest `ff` ended this dispatch, not that a game started.
  Repeated calls after successful halt are inert. Faults are not successful halts.
- Bounds faults are offset-bearing and sticky. Subsequent Step/Run returns the
  same fault without mutation. A failed opcode fetch leaves PC unchanged.
- Unlike native unguarded pops, operand underflow faults safely. Truncated
  immediates fault rather than reading outside input. The zero-value VM behaves
  like New(nil). NewAt(nil,0) rejects the entry.
- Taken branch/call/return targets must be inside the buffer. Untaken targets
  are not range-checked. Instruction boundaries are not inferred: an operand
  byte may be a target. Invalid return targets do not pop R.
- Immediate-push/call capacity checks precede operand reads. Symbol-load `04`
  reads its index before capacity validation. Branches check truncation,
  then underflow, then taken target. Store ordering is documented above.
  These guards, transactional failures and sticky faults are emulator policy.
- Storage is copied input, copied independent RAM, binding views and fixed
  stacks. There are no clocks, random sources, host services or unbounded runs.
  VM is not safe for concurrent use.

## Focused validation

Tests exercise public constructors/Step/Run with synthetic literals: arithmetic,
byte order, signed extremes, both branch paths, nested returns, budgets,
65/17 capacities, exhaustive unsupported opcodes, faults and ownership. Symbol
tests cover index255, element254 of255, invalid shapes, self-overwriting operands,
overlapping views and future opcode mutation. A supplemental internal check
verifies saved PCs. Two bounded fuzz targets check repeatability and memory
aliases. Tests-first baselines were observed before each added contract.

```text
go test ./gvm -count=1 -cover
go vet ./gvm
go test ./gvm -run="^$" -fuzz=FuzzBoundedDeterminism -fuzztime=3s -parallel=1
go test ./gvm -run="^$" -fuzz=FuzzSymbolAliasDeterminism -fuzztime=3s -parallel=1
```

These are synthetic kernel checks, not SGS load, first-frame, input-response,
playability, game-startup, or full-workspace acceptance evidence.
