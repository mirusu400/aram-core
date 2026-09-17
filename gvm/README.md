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
| `0b` | Copy signed top index depth-1 to a private32-bit scalar, no stack effects or inline operands, PC=P |
| `0c` | Partial support: after observed `0b`, restore a valid non-growing depth from the saved index without changing backing words, PC=P |
| `0d` | Increment top16 modulo65536 without popping or operands, PC=P; depth65 accepted |
| `0e` | Decrement top16 modulo65536 without popping or operands, PC=P; depth65 accepted |
| `0f` | Duplicate raw top16, depth64 grows to65, empty/overflow fault under host policy, PC=P |
| `12` | Replace a=S[top-1], b=S[top] with low16(a+b) |
| `13` | Replace a,b with low16(a-b) |
| `14` | Replace a,b with low16(a*b) |
| `15` | Signed32 division of signextended16 a/b, truncating toward zero, retain low16 |
| `1d` | Signed16 a>b, replacing a,b with canonical zero or one; equality is false |
| `1f` | Signed16 a>=b, replacing a,b with canonical zero or one |
| `21` | Raw16 a==b, replacing two words with canonical zero or one, no inline operands, PC=P |
| `31` | u8 symbol, u8 element, signed i8 immediate; store signextended LE16 at symbol+2*element, PC=P+3 |
| `35` | u8 destination symbol, u8 source symbol; copy first rawLE16 word, no stack effects, PC=P+2 |
| `36` | u8 symbol, signed i8 immediate; store signextended LE16 at symbol start, PC=P+2 |
| `3a` | u8 symbol, signed i8 delta; add delta modulo65536 to its first LE16 word, stacks unchanged, PC=P+2 |
| `3c` | Pop16; if signed16(top)<signed8(P), jump B+BE16(P+1), otherwise P+3 |
| `3d` | Pop16; if signed16(top)>=signed8(P), jump B+BE16(P+1), otherwise P+3; equality branches |
| `3e` | Pop16; if signed16(top)<=signed8(P), jump B+BE16(P+1), otherwise P+3; equality branches |
| `3f` | Pop16; if raw16(top)==signextended8(P), jump B+BE16(P+1), otherwise P+3 |
| `40` | Pop16; if raw16(top)!=signextended8(P), jump B+BE16(P+1), otherwise P+3 |
| `41` | PC=B+BE16(P) |
| `42` | Pop16, jump B+BE16(P) if nonzero, otherwise P+2 |
| `43` | Pop16, jump B+BE16(P) if zero, otherwise P+2 |
| `44` | Save P+2 on R, jump B+BE16(P) |
| `45` | Nonempty R only: restore top saved PC exactly and pop it |
| `4c` | Configured address model only: u8 symbol, u8 element; push encoded address of symbol+2*element, PC=P+2 |
| `4d` | Configured address model only: u8 symbol, push its encoded region-relative word address, PC=P+1 |
| `4e` | Configured address model only: replace top tagged address with its rawLE16 word, depth unchanged, PC=P |
| `4f` | Configured address model only: store raw top16 as LE16 at RAM+2*signed16(next-to-top), pop two, PC=P |
| `51` | Explicit service constructor only: write four device-query words through a tagged reference and pop once |
| `96`, `97` | Pop16 argument, call a ret-only native callee in this build, PC=P |
| `9a` | Explicit service constructor only: forward signed16 interval and raw16 selector to a borrowed timer-request sink, then pop two, PC=P |
| `a1` | Explicit service constructor only: replace two signed16 bounds with an equal bound or a draw in the half-open signed range, pop once, PC=P |
| `b4` | Configured address model only: scalar operation on a tagged word array, pop four, PC=P |
| `b5` | Configured address model only: forward live-source operation on two tagged word arrays, pop four, PC=P |
| `b9` | Explicit service constructor only: write local hour/minute/second/millisecond from one virtual-clock sample and pop once |
| `ff` | Exit current dispatch, PC=P |

`40` compares against the sign-extended immediate before selecting its unsigned
BE16 whole-buffer target. Equal values fall through, unequal values branch.
The host shares the existing immediate-branch policy: require all three operand
bytes before underflow, validate only a taken target, then commit one pop and
PC update. Native code instead publishes the pop before conditional target reads
and retains backing. Existing host popped-slot zeroing remains intentional
hygiene, not reference backing parity; growing `0c` is still unsupported.
The kernel's `9a` support is deliberately request-only. It does not install a
shared timer, advance virtual time, deliver a callback or call `BeginDispatch`.
The sink adapter must preserve the authenticated interval-below-10 bypass and
define its own
atomic installation, serialization and delivery policy. No initialization,
presentation or game-start milestone follows from this branch.

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

## Explicit dispatch re-entry

`BeginDispatch(entry uint32) (started bool, err error)` prepares another
dispatch only after the preceding dispatch completed through `ff`. It does not
execute an instruction, advance time or deliver a timer event. A single owner
must serialize this API with stepping and borrowed service mutation.

Preconditions and validation are ordered:

1. A sticky execution fault is returned unchanged. This API never clears it.
2. A non-halted VM returns `ErrDispatchActive`, including a dispatch suspended
   by instruction-budget exhaustion. Resume that dispatch with `Run` instead.
3. Entry zero returns `false, nil` with all state unchanged, representing an
   absent callback. These preconditions still apply to zero.
4. A nonzero out-of-buffer entry returns a nonsticky `ErrInvalidTarget` without
   mutation. As with `NewAt`, this is an explicit uint32 byte offset, not an
   implicitly masked native16-bit argument or a parsed header field.
5. A valid nonzero entry sets PC, clears the completed flag and resets operand
   and return-stack depths. It returns `true, nil` without running the entry.

Code, RAM, symbol aliases, stack/return backing words and borrowed services are
preserved. The raw saved-top scalar is also preserved, but using it through
`0c` requires a fresh `0b` in the new dispatch. Invalidating that permission is
fail-closed host policy, **not** a claim that the native dispatcher resets its
distinct saved-top scalar. Zero/no-op and rejected requests preserve permission.
Existing constructors still start their initial dispatch normally, including
offset zero. They do not acquire implicit services or fabricated idle state.

This is not a Reset, SaveState, interrupted-dispatch restart or machine factory.
The authenticated timer wrapper performs guest-state writes and native helper
work before reading its entry. Calling `BeginDispatch` alone must not bypass
those prerequisites or count as successful timer delivery. See the
[timer integration boundary](../docs/gvm-timer-integration.md).

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

`21` compares full 16-bit words, not bytes or truthiness. Its verified native
handler has no underflow guard and retains the vacated backing word. The host
rejects depths zero and one before mutation, accepts depth65, and retains its
existing popped-slot clearing policy. That clearing is host hygiene, not native
backing parity. Saved-top metadata and return frames are unchanged, and growing
`0c` restores remain unsupported. This scalar operation establishes no startup
or presentation milestone.

Division by zero produces sticky `ErrDivideByZero` without changing operands.
This differs deliberately from the native error-helper cleanup/pop path.
The widened division makes `-32768 / -1` wrap to `0x8000` without a host trap.

## Explicit device, civil-clock and random services

`NewWithAddressSpaceAndServices` is an opt-in constructor, not a factory or
startup adapter. Existing constructors retain typed unsupported `51/b9/a1`.
Nil service config deliberately provides no providers, with precise unavailable
errors after operation-specific validation. Device query, clock and RNG are independent.
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
borrowed clock or random owner. This adds no complete VM snapshot, reset, scheduler, presentation,
or game-start contract.

### Explicit random range (`a1`)

`ServiceConfig.Random` and `RandomStream` select a borrowed shared
`runtime.Random` and a named stream. Both must be supplied together. Construction
validates the pair and name, but never creates, seeds, resets or draws from a
stream. Callers explicitly seed with `SetLCG214013Seed` or restore validated
state. No host clock, TLS context, title hash or implicit seed supplies state.
The exact reference initializes a newly allocated TLS context to one, but this
does not establish the seed at guest entry or the observed execution boundary.

The selected algorithm is `RNGLCG214013Output15`: uint32 state advances by
`state*214013+2531011` modulo `2^32`, then returns `(state>>16)&32767`.
Its named state and draw count participate in shared-runtime snapshots and
serialization. Existing algorithm encodings remain unchanged. Older readers
reject the new algorithm rather than gaining forward compatibility. Random
owners must serialize stepping, reseeding, restoring and other borrowers.
Replacing an entire service graph requires explicitly rebinding its borrowers.
This is not a complete GVM save/restore or native thread-sharing contract.

After the opt-in constructor check, `a1` requires two stack values before
checking providers. Equal raw words retain that value without any random access,
even with an empty service config. Unequal bounds use signed16 ordering and
exactly one draw, returning `lo + draw % (hi-lo)` with a signed32 width. The
upper bound is excluded. Width one still consumes a draw. Width65535 reaches
only -32768 through -1, so this is not a uniform inclusive-range API.

Missing providers produce `ErrRandomUnavailable`. Missing, mismatched or
exhausted streams preserve the shared owner and VM operands and retain their
runtime error identity through the sticky execution error. No stream is lazily
created by a draw. All VM checks precede the draw, with no fallible operation
between successful draw and result commit. Existing popped-slot zeroing is host
hygiene, unlike native backing retention. Native allocation failures and
unchecked writes have no established rollback parity. No startup or frame
milestone is implied by an explicitly seeded diagnostic run.

In a bounded nine-input public-kernel comparison, the selected SHA-256
`c4f6ade5193dec699654fc2cb488af4cd131152fe14fa27c384c1d36ec47656e`
remained at3621 instructions with legacy construction. Opt-in device/clock
configuration without RNG stopped at the same offset33603 with
`ErrRandomUnavailable`. Separately chosen per-input seeds0,1 and4294967295
each reached4611 instructions, then unsupported `9a` at116. Seed1 replay was
identical and the other eight observations were unchanged. Those seeds are
diagnostic choices, not recovered native state. No reserved initialization,
ordinary product launch or framebuffer was established.

The next boundary is an active timer request, not a safe no-op. See the
[timer integration evidence](../docs/gvm-timer-integration.md) for the
static registration relationships and partial callback route, remaining state,
and shared-runtime scheduling policies that must not be assumed equivalent.

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

`b5` consumes `[destination reference, source reference, signed count, selector]`.
Its independently verified selectors0..11 correspond to the same arithmetic and
bit operations as `b4`, but the right operand is the current live source word.
Forward overlap is observable: copying words `[1,2,3,4]` from0 to1 for three
elements produces `[1,1,1,1]`, not memmove output. Nonpositive counts skip all
element/divisor access, unlike `b4`'s scalar zero-divisor check.

Host validation is configured arena, depth4, destination starting word, source
starting word, selector, then complete positive destination/source spans. This
fail-first policy differs from the native unconditional pair of resolver calls.
Same-bank operations simulate in one bounded union candidate; different banks
use a candidate destination and unchanged source. Only successful destination
bytes are committed. An overlap-generated zero divisor faults atomically rather
than exposing the native partial prefix. No source snapshot or original-divisor
pre-scan substitutes for live alias semantics. Candidate storage is bounded by
the accessed intervals, not the size of an arbitrary host-provided arena.

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

`35` is independently verified as a three-byte first-word symbol copy, not an
array copy or indexed operation. Both unsigned symbol indices and the complete
source LE16 word are cached before the destination write. Identical, partial,
and instruction aliases retain that load-before-store behavior. Neither stack
changes, and depths0,64 and65 are valid without capacity or underflow checks.

Host validation eagerly requires both inline operands, then checks destination
index, destination full word, source index and source full word in that order.
Configured bindings use the selected global file/RAM spans independently; empty,
one-byte, odd and oversized descriptor views may succeed when backing is safe.
Legacy bindings each require two bytes. No descriptor count/type or len/2 rule
is inferred. Errors are sticky and atomic except opcode fetch, unlike the native
operand-PC publication and opaque diagnostic-helper effects.

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

`0f` duplicates the raw16 top word without interpreting it as an address. Capacity
is checked before reading the source: depth64 grows to65, while depth65 faults.
The host additionally rejects depth0 rather than reproducing the native unchecked
read below the operand stack. After validation the source is cached, copied into
the next slot, and depth grows once. Lower words, guest memory, savedTop/valid,
symbol bindings and saved returns remain unchanged. There are no inline operands,
configuration requirements or extra PC changes. Existing sticky fault, budget and
post-fetch PC rules apply; the native error helper is not emulated.

`0b` copies the current signed operand top index, `depth-1`, into one private
32-bit scalar. Empty depth records -1 and depth65 records64. Repetition overwrites
that scalar; no operand values, memory, saved returns, depth or post-fetch PC are
changed. It is not a NOP or a stack checkpoint. A separate private validity flag
marks that this host VM has observed a write. Its initially-unset state is host
policy because the reference scalar's initial value, reset and cross-dispatch
lifetime have not been established. No consumer, serialization, clone or reset
semantics are inferred. Internal tests verify the actual hidden write in addition
to public execution checks; unchanged public stack output alone would not prove it.

`0c` has deliberately partial support. Its independently verified native handler
assigns the saved32-bit scalar to the operand top index, without copying values,
clearing storage, consuming the marker or changing PC beyond opcode fetch. The
host supports this only after an observed `0b`, with the saved index in -1..64
and the resulting depth no greater than the current live depth. Bounds are checked
before adding one. Success changes only depth and preserves all backing words,
including newly hidden words, marker validity/value, returns, memory and symbols.
A post-save word mutation remains visible: this is not a historical snapshot.

Missing, invalid or growing markers retain sticky `UnsupportedOpcodeError` with
the instruction offset and post-fetch PC. These are unsupported host domains,
not native validity guards. The native operation can re-expose backing words,
but existing host pops clear discarded slots and full native backing/lifecycle
provenance is unresolved. No zero-fill, whole-stack snapshot, global pop-clearing
change or guessed initial marker is used to conceal that gap. A standalone `0c`
therefore remains unsupported, including in the exhaustive unknown-opcode test.
Repeated supported restores preserve the marker; a later `0b` overwrites it.
This restriction advances neither reset/save-state support nor product startup.

`4e` is supported only with an explicitly configured address space. It replaces
one raw16 stack address in place with the rawLE16 word resolved through `ReadWord`,
without changing depth, memory, saved returns or post-fetch PC. Bit14 selects the
file region; clearing that bit must leave a nonnegative signed16 word index.
The complete word must fit the chosen global region, including when a read crosses
symbol boundaries. No descriptor shape or service configuration is required.
Legacy constructors remain unsupported, checked before underflow. Configured
execution adds sticky atomic underflow/address faults; ordinary `ReadWord`
inspection errors remain nonsticky. These checks are host safety policy, not
native invalid-pointer or error-helper behavior. Depth65 and encoded address zero
are valid when their backing word exists. Loaded values are data, not recursively
resolved pointers. This operation does not provide image initialization or startup.

`0d` and `1d` were independently verified across their complete handlers, not
inferred from adjacent opcodes. Both are one-byte stack-only operations with no
inline data, symbol access, direct PC change or saved-return effects. `0d` changes
only the top raw16 value, wrapping ffff to0000. `1d` compares the next-to-top
signed16 value against the top signed16 value using strict greater-than, stores
canonical zero/one in the lower slot and pops once. Equality is false and operand
order matters. Depth65 is valid for both operations. The host adds sticky atomic
underflow faults at depths below1 or2 respectively; native invalid stack indices
were unchecked. Clearing `1d`'s popped backing slot is host hygiene only.

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

`3f` is independently hash-verified across its complete handler. It compares
raw16 top against the sign-extended immediate8 for equality, not unsigned8 or
an ordering predicate. Thus immediate80 matches ff80, not0080. Both paths pop
once; equality selects an unsigned BE16 whole-buffer target, while inequality
falls through four bytes from the opcode. Eager three-byte framing precedes
underflow, and only a taken target is range-checked before committing the pop.
Depth65 is valid. These sticky atomic guards and clearing the vacated stack slot
are host safety/hygiene, not native early-pop or unchecked-memory behavior.
SavedTop/valid, returns, guest memory and existing branch predicates are unchanged.
No configured address space, service, runtime initialization or startup is implied.

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
  stacks. Legacy constructors install no clocks or host services; the explicit
  service constructor uses the caller-owned deterministic clock described above.
  There are no unbounded runs. VM is not safe for concurrent use.

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
