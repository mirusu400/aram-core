# GVM and BREW follow-up, 2026-09-13

This continues [the bounded GVM foundation](gvm-execution-subset.md). It is not
an all-platform game-startup completion claim. Only independently authored code,
synthetic tests and normalized interoperability facts are tracked. Supplied
reference executables are inspected statically, not executed or modified.

## GVM scalar operations

The reference remains SHA-256
`c2c170b3c47cd99dc55fdadad31dd1927473b4362868205c5eb49eff96c60719`.
An independent hash-gated pass confirms the numeric dispatch associations and
critical instructions for `0x15`, `0x1f` and `0x4d`.

- `0x1f` consumes two signed 16-bit values `a, b`, stores canonical `a >= b`
  as zero or one in the lower slot, and decrements the top. It has no operands.
- `0x15` sign-extends operands to 32 bits before signed division, truncates
  toward zero and retains the low 16 result bits. Thus `-32768 / -1` produces
  `0x8000`, not a 16-bit division-overflow exception.
- The reference zero-divisor path calls its error helper, reloads and decrements
  the stack top, and does not write a quotient. Its helper also performs host
  cleanup and selects a native sentinel. A sticky transactional `ErrDivideByZero`
  that preserves the stack is explicit emulator safety policy, not a claim of
  matching that unmodeled cleanup path.

## GVM address regions

The file region starts at the header's symbol-data offset, not at buffer zero
or the minimum aliased symbol offset. It ends after processing all symbols,
including file bytes copied into initialized mutable symbols. The RAM region
starts after native descriptor tables and ends after symbol allocation, before
mutable media allocation. Portable region-relative RAM starts at zero and must
retain those cumulative symbol offsets.

Opcode `0x4d` encodes a symbol's region-relative byte offset shifted right by
one into a 16-bit word address. File addresses additionally set bit `0x4000`.
The resolver tests that bit, clears only it for file addresses, interprets the
remaining index as signed 16-bit and checks it against the selected region's
word limit. Truncation and tag collisions must not be silently corrected.
Odd byte offsets retain the reference's shift behavior, not an invented aligned
pointer rule. Safe reads additionally validate the entire two-byte span.

The explicit address-space constructor must clone the program and RAM once,
retain overlapping views, validate every span before binding, and keep old
constructors distinguishable from a configured address model. Resolving an
encoded address returns a value, never a raw host pointer. These contracts do
not decode media or initialize reserved runtime symbols.

## BREW prior boundary rechecked

The supplied Melange APK SHA-256 is
`aa391bd9099ebb50bd7f8f41c53bc0616e568e250698e3b342f49d8a12044437`.
Its conditional reread path allocates an eight-byte loader-owned prefix and
invokes the payload at MOD file offset zero. The checked caller passes shell,
helper/context and module-output arguments, then uses the module vtable for
AddRef, CreateInstance and Release. This is not evidence that all MOD variants
share memory or relocation rules.

For selected MOD SHA-256
`06cd2a2a72ac04700fdcae138824043d97144bca7c92eb2cd9415556f31b4ed2`,
the 2,720-byte entry saves incoming r9 and then replaces it with the shell
argument before computational use. Its first external call is shell vtable
slot `+8` with ID `0x01001003` and a stack-local output pointer.

### Newly identified FileMgr and delegated entry

The hash-qualified shell vtable at `0x177be4` maps slot `+8` to `0x247245`,
which reaches factory `0x242294` and class lookup `0x241c1c`. The class table
at `0x177e88` maps `0x01001003` to constructor `0x22f453`, producing vtable
`0x1773dc`. A diagnostic for the same ID independently names FileMgr. Its
OpenFile and GetInfo paths reach OEMFS_Open and OEMFS_GetAttributes. This is
an identified interface chain, not an observed successful service call.
The constructor has conditional status paths, and the selected MOD does not
check the first factory result before dereferencing its output.

The selected 2,720-byte MOD is a companion-loading, transformation and entry
delegation stub. It reads a companion, processes it into allocated storage,
conditionally calls an entry inside that output and interposes a Release
lifetime wrapper. It is not evidence of a self-contained game executable.
Exactly one matching companion exists in the same archive: 42,432 bytes,
SHA-256 `b7c3f959e988e3af3eaa369d9a00eab221de340931b6fb7532993f469e02eaa0`.
The companion was only hashed and inspected for membership, not transformed,
executed or copied into tracked artifacts. Its name and header bytes are not
retained here. The exact transformation specification and output ABI remain
unverified, so implementing fabricated successful startup would be incorrect.

### Optional cache callback narrowed for this build

Actual mode-3/mode-4 callsites `0x23784a` and `0x23785a` use provider global
`0x2b3720`, obtained through class `0x01030430`. Relocation resolution joins
OEMMemCache_New to vtable `0x2a5144`, slot `+12`, and OEMMemCache_ArchClean.
The complete architecture callback returns zero without cache work in this
specific supplied build. A null provider skips these calls and callers ignore
their results. This resolves the previously unknown cache callback path, not
general MOD relocation, BSS, allocation, decoded-entry or handset semantics.

The coordinator reran a hash-qualified checker with 103 instruction checkpoints,
22 host constants and three evaluated PLT relocations. It also rechecked the
MOD identity, same-archive companion and unchanged archive/APK hashes. These
are static interoperability checks, not execution tests. BREW product behavior
remains recognition-only.

## Address-space checkpoint acceptance and remaining boundary

| Requirement | Observed check |
|---|---|
| Scalar signedness, rounding and failure state | Public Step/Run tests pass for `15` and `1f`, signed extremes, zero divisors, underflow, sticky faults and budgets. Tests failed before implementation. |
| Explicit address identity and ownership | Public constructor/`4d`/ReadWord tests pass for nonzero file bases, shared RAM and program aliases, odd offsets, truncation, tag collisions, invalid bounds, caller mutation isolation and old constructors remaining unconfigured. |
| Decoder allocation and integration | Public decoder/address-space tests pass for file consumption including copied initializers, cumulative zero/initialized RAM, shared Data views and media exclusion. |
| In-place original-buffer progress | Eight v2 SGS buffers decode and one v1 remains unsupported. Seven of eight v2 inputs execute more instructions. Selected SHA `c4f6ade5193dec699654fc2cb488af4cd131152fe14fa27c384c1d36ec47656e` advances from 8 to 15 successful instructions and then rejects `0x3c` at offset 456. This uses descriptor initialization only, not reserved runtime state, services or the product Machine path. |
| Ordinary product regression | Ordered core, runner, frontend and integration tests/vet pass. Runner has 98 passing tests, synthetic gate 20/20. Same-scope deltas retain 21 and 496 unchanged rows. BREW and GVM still report recognized-only through the ordinary product. |
| Portability | Windows product/probe/frontend, core Android arm64/Linux amd64/Darwin arm64, and official Android binder pass. Focused Windows 386 tests pass. |
| Configured private gate | The private command exits 1 with the same six missing KTF/Raptor reference prerequisites. Unchanged reports do not turn this failed prerequisite gate into a pass. |

The independent frozen review found no semantic blockers. Three 15-second
fuzz runs passed for address-space execution, bounded execution and image
decoding. These are additional robustness checks, not handset equivalence.
At this address-space checkpoint the kernel had 20 supported opcode values
with an explicitly configured address space. Further numeric operations,
reserved state, events and graphics
are still required before any GVM game-startup claim.

## Subsequent signed-immediate branch checkpoint

Opcode `0x3c` adds one supported opcode value. For the same reference hash,
the verified four-byte encoding is opcode, signed8 comparison immediate and
unsigned BE16 whole-buffer target. It pops once on both paths and branches
only when signed16 top is strictly less than the immediate. It does not touch
symbols, resolve tagged addresses, call host helpers or alter the saved-PC stack.
The kernel's eager operand-span check and transactional failure policy differ
explicitly from native lazy target reads and publication of the pop before a
taken target read. See [kernel policy](../gvm/README.md).

Tests failed with unsupported `0x3c` before implementation and then passed.
The owning matrix checks signed extremes/equality, both paths, full stack,
big-endian absolute targets, unused invalid targets, eager truncation, exact
sticky faults, memory/return preservation and instruction budgets.

The same public decoder/address-space kernel was rebuilt before and after the
change and run against the same nine in-place original SGS identities. One v1
remains unsupported. The selected v2 SHA above advances from 15 to **43**
successful instructions and now rejects `0x0a` at offset **669**. The other
seven v2 observations are unchanged, with no new bounded safety faults. This
still uses descriptor initialization only, not the product Machine path or
reserved runtime initialization. Neither a frame nor a game-startup milestone
is established by this progress.

The subsequent frozen-source ordered gate passed core, runner, frontend and
integration tests/vet, synthetic 20/20, Windows product builds, core portability
builds and the official Android binder. Same-scope product deltas again show
21 and 496 unchanged rows. The configured private command still exits 1 for
the same six missing KTF/Raptor prerequisites. Its triage still reports 358
BREW and nine GVM inputs as execution unsupported. No product-level milestone
was promoted to conceal this missing integration.

| Branch requirement | Concrete observation |
|---|---|
| Signed comparison, byte order and absolute target | Owning Step/Run matrix and nonzero-entry target tests pass; the unchanged-hash original progresses 15 to 43 instructions through public kernel APIs. |
| No state mutation on rejected input | Truncation, underflow and taken-target tests preserve operands, memory and saved PCs, and repeated calls retain exact sticky error/PC. |
| Untaken target and budget behavior | Unused invalid targets fall through; end-of-buffer faults on the next fetch; zero/one budgets and resume tests pass. |
| Existing product boundaries | Ordinary synthetic gate and same-scope reports remain unchanged; BREW/GVM product startup is still unsupported. |
| Portability and collateral regression | Full public ordered gate, Windows builds, official Android binder and focused 386 tests pass; private prerequisite failure remains explicit. |

## Subsequent stack-to-symbol store checkpoint

Opcode `0x0a` is a two-byte direct symbol store/pop, not an indexed load.
The same hash-qualified reference reads only a u8 symbol index and its pointer,
stores the raw top16 value as LE16, then pops once. Its signed top>=64 guard
rejects depth65 despite popping; depth64 is accepted. It reads no descriptor
type or element count. The independent checker passed 43 instruction anchors,
three contiguous fingerprinted windows and all 65,536 LE16 value/alias cases.
Those checks are static/specification evidence, not reference execution.

Configured address-space stores use full global file/RAM word bounds and can
cross descriptor boundaries, including empty descriptor views with valid
backing. Legacy independent symbol slices require two bytes in their own span.
Full-word bounds and underflow are explicit host safety policy. Native code
only checks starting-pointer membership and publishes operand PC before its
terminal cleanup/error path. Kernel faults instead remain sticky and preserve
memory and stacks at opcode+1. No cleanup service is fabricated.

The owning tests first failed with unsupported `0x0a`, then passed using actual
`05/06` push programs through public constructors, Step and Run. They cover
depths0/1/64/65, index255, odd/large spans, exact LE bytes, overlap and global
boundaries, future-fetch/self-operand aliases, caller/snapshot isolation,
budgets and call/return preservation. Focused and full GVM tests, vet and
Windows 386 tests/vet pass.

The same nine original identities were exercised through the public decoder
and descriptor-only kernel before and after the store change. Three v2 inputs
advanced, the other five remained unchanged, and v1 stayed unsupported:

| SHA-256 prefix | Successful instructions before/after | Next explicit boundary |
|---|---:|---|
| `47e6e53e` | 3 / 13 | `0x35` at143 |
| `aac8b2c5` | 4 / 6 | `0x51` at3819 |
| `c4f6ade5` | 43 / 45 | `0x3e` at673 |

No new bounded safety fault appeared. This advances a partial kernel only.
Reserved runtime initialization, services, rendering and ordinary Machine
integration are still required before a GVM game-startup claim.

| Store requirement | Observed check |
|---|---|
| Raw LE16 store/pop and unusual upper guard | Public push/store programs pass at depths1/64, reject65, and verify u8 indices through255 and preserved adjacent bytes. |
| Shared region ownership and fetch coherence | File/RAM alias tests, empty/one-byte descriptor views, odd starts, cross-descriptor stores and self-operand/future-fetch programs pass. Global last-byte writes reject safely; legacy short slices remain rejected. |
| Error order, isolation and replayable stepping | Missing operand, overflow, index, span and underflow precedence tests pass with sticky exact PC/error and unchanged failed state. Snapshot/caller isolation, budgets and actual call/store/return programs pass. |
| Original decoder/kernel integration | Same-hash before/after execution advances three originals as shown above, without new safety faults. It is not ordinary Machine execution. |
| Ordinary product and build acceptance | Frozen core/runner/frontend/integration tests/vet, synthetic20/20, Windows builds, core portability and official Android binder pass. Product deltas21/496 remain unchanged, including recognized-only BREW/GVM. |

The configured private command still exits1 with the same six missing
KTF/Raptor prerequisites. The independent frozen review found no blockers by
inspection. These results do not make the full private gate green or establish
GVM/BREW game startup.

## Subsequent less-than-or-equal branch checkpoint

The same hash-qualified reference independently establishes `0x3e` as a
four-byte signed16-top <= signed8-immediate branch. Equality takes the BE16
whole-buffer target; both paths pop once. The complete 22-instruction handler
has no helper calls, guest-memory stores or saved-PC accesses. Its native JG
selects fallthrough. The coordinator independently reran the checker: 28
instruction anchors, two complete windows, all 16,777,216 signed comparison
pairs, 65,536 targets and identity-negative checks pass. These are static
specification checks, not execution of the supplied reference.

The implementation shares the existing `3c` handler but preserves its strict
comparison. Eager three-byte validation, underflow, taken-only target bounds
and atomic commit remain explicit host policy, not native failure ordering.
Tests first failed with unsupported `3e`, then passed. The new external-package
tests establish operands and nested calls using public constructors and guest
bytecode rather than private VM-state injection.

The same nine in-place original identities were compared before and after the
change through public decoder/address-space APIs. Selected SHA
`c4f6ade5193dec699654fc2cb488af4cd131152fe14fa27c384c1d36ec47656e`
advances from **45 to 52** successful instructions and then rejects `0x3d` at
**702**. The other seven v2 inputs are unchanged and the v1 input remains
unsupported. No new bounded safety fault appears. This remains descriptor-only
execution, without reserved initialization, services, rendering or an ordinary
Machine launch. The configured kernel now supports 23 opcode values, not games.

| Less-or-equal requirement | Concrete observation |
|---|---|
| Signed predicate and equality, one pop and prefix | `TestBranchLessEqualSignedMatrix` passes at depths1/65 with signed extremes and equality. Existing `TestBranchImmediate` tests also pass, preserving strict `3c`. |
| Whole-buffer BE16 targets | `TestBranchLessEqualAbsoluteTargets` passes from nonzero entry for targets0/0x1234/0x8001/0xffff, followed by actual target fetch. |
| Bounded faults and stepping | `TestBranchLessEqualHostFaultOrder`, `UnusedTargetsEndFetch` and `Budget` pass for eager truncation, underflow, taken/unused targets, exact sticky PC/state, end fetch and resume. |
| Guest memory and saved returns | `TestBranchLessEqualMemoryAndCallReturnPreserved` passes with file/RAM readback and two nested bytecode returns on both branch paths. |
| Original decoder/kernel integration | Same-hash before/after execution progresses45 to52 as above; no frame/startup milestone is inferred. |
| Ordinary product and portability | Frozen ordered core/runner/frontend/integration tests/vet and synthetic20/20 pass. Windows builds, core Android/Linux/Darwin, official Android binder and focused386 tests pass. Product deltas retain21/496 unchanged rows. |

The configured private command still exits1 for the same six missing
KTF/Raptor prerequisites. Triage still contains358 BREW and9 GVM execution
unsupported results. Independent frozen review found no blocking findings by
inspection. Neither green public checks nor unchanged product reports close
the outstanding all-platform game-startup goal.

## Subsequent greater-than-or-equal branch checkpoint

Opcode `3d` is independently verified against the same GVM reference hash:
its four-byte form pops once and branches on signed16(top) >= signed8(immediate).
Equality takes the unsigned BE16 whole-buffer target. Native JL selects
fallthrough, with no helper calls or guest-memory/saved-PC stores. The complete
handler and 28 anchors, all signed comparison pairs and all BE16 values pass an
independent checker rerun. Eager operands, underflow, taken-target bounds and
transactional faults remain host safety policy. Existing `3c <` and `3e <=`
behavior is preserved by the shared handler.

The new public-bytecode tests first failed with unsupported `3d`, then passed.
The unchanged-hash selected original above progresses **52 to 60** successful
instructions through public decoder/kernel APIs, then rejects `03` at **717**.
The other seven v2 inputs are unchanged; one v1 remains unsupported. No new
bounded safety fault appears. This is 24 supported opcode values in a partial
kernel, not ordinary Machine execution or a game-start milestone.

| Greater-or-equal requirement | Observed check |
|---|---|
| Signed extremes/equality, one pop and prefix | `TestBranchGreaterEqualSignedMatrix` passes at depths1/65; all existing branch tests pass. |
| Absolute BE16 addressing | `TestBranchGreaterEqualAbsoluteTargets` passes at nonzero entry with zero, asymmetric, high-bit and final-byte targets, including subsequent fetch. |
| Failure order and stepping | `HostFaultOrder`, `UnusedTargetsEndFetch` and `Budget` tests pass for truncation, underflow, taken/unused bounds, sticky exact PC/state, end fetch and resume. |
| Memory and saved-PC preservation | `MemoryAndCallReturnPreserved` checks file/RAM readback and actual nested bytecode returns on both paths. |
| Original integration | Same nine original identities compared before/after; selected52 to60, other observations unchanged. Reserved state/services/rendering are absent. |
| Product regression and portability | Frozen ordered core/runner/frontend/integration tests/vet, synthetic20/20, Windows builds, core Android/Linux/Darwin and official Android binder pass. Focused386 branch tests pass. Product deltas21/496 unchanged. |

Independent frozen review found no blocking issues by inspection. The private
command still exits1 for the same six missing KTF/Raptor prerequisites; ordinary
product triage still reports358 BREW and9 GVM execution-unsupported inputs.

## GVM service51: identified dependency, still unsupported

The same reference maps `51` to a complete zero-inline-operand handler. It
consumes one tagged reference and writes four LE16 words through the resolved
destination. It is not a no-op or a proven fixed response. Two output words
depend on three fields returned through a virtual service call. The concrete
provider, field provenance, initialization and lifetime remain unresolved.
Neither screen dimensions nor palette/color meanings are inferred from numeric
thresholds. The native resolver checks a starting word index, not the complete
eight-byte write extent; a future host must impose bounded transactional policy.

The coordinator reran seven fingerprinted windows, 28 instruction anchors,
393,989 synthetic classification/shift/reference cases and integrity negatives.
These establish a static interface dependency, not service execution. Opcode51
remains explicitly unsupported until the provider contract is established.

## BREW companion framing narrowed, decoder not selected

For the previously hash-qualified 2,720-byte MOD and 42,432-byte companion, the
caller requests a 128-byte outer header followed by a 16-byte inner header.
Its requested input span is [144,42429), leaving three bytes unrequested. It
requests 143,316 output-allocation bytes for 143,308 produced bytes and a
10,456-byte workspace. These are conditional requested I/O/allocation sizes,
not observed successful reads or decoding.

Range/probability constants, model count and literal-state transitions strongly
fingerprint an LZMA-family transform, but do not establish exact standard stream
compatibility. The 16-byte framing differs from a conventional 13-byte candidate
header and must not be silently repaired. The input-length argument is not an
established decoder bound. No payload was transformed or executed. The checked
success path forwards the original entry arguments to the first produced byte
after an eight-byte loader-owned prefix; decoded ABI, BSS, relocations, services
and lifecycle remain unresolved.

An independent coordinator rerun passes 49 instruction checkpoints, exact
MOD/companion identities, unchanged archive checks and eight synthetic span
policy cases. This is static format evidence, not a decoder test or load/start
claim. No proprietary decoder, payload or asset is copied into the product.


## Indexed-load continuation: opcode03

The independently hash-qualified 62-instruction handler loads raw LE16 from
a symbol element selected by two unsigned byte operands. Its descriptor count
is the byte at offset1, not count plus one, and it does not inspect type.
The checker passed 44 instruction anchors, three complete windows, 67,346
synthetic guard cases, 65,536 LE16 cases and four integrity negatives.
No supplied reference executable was run. Transitive error-helper effects
remain unresolved and are not emulated.

The kernel now supports25 configured opcode values. Host policy requires
two operands before capacity, symbol index, exact even descriptor shape
(up to510 bytes), element bounds and a complete-word read. Failures retain
the established sticky transactional policy. Public constructor/bytecode
tests cover raw16 values, unsigned indexes, capacity, failure ordering,
legacy and configured bindings, aliases, memory, returns and budgets.

Rebuilt public decoder/kernel observations on the same nine original SGS
identities advance the selected c4f6ade5 identity from60 to65 instructions,
then stop at unsupported4f at727. Other seven v2 cases are unchanged and
the v1 case remains unsupported. This is descriptor-only kernel progress,
not reserved-state initialization, a frame, or ordinary product startup.

The frozen public ordered gate, Windows product builds, core Android/Linux/
macOS builds and official Android binder passed. Same-scope deltas retain
21 synthetic and496 private rows unchanged. Configured private-all exits1
with the same six missing KTF/Raptor package prerequisites, so full workspace
acceptance remains failed. No expectations or product milestones were weakened.


## Direct mutable-RAM store continuation: opcode4f

Independent hash-gated analysis verifies the entire nine-instruction handler:
no inline operands, branches, helpers or services; signed16 index below raw16
value selects mutable-region base+2*index, then two stack words are popped.
Three fingerprint windows,65,536 signed-index cases,65,536 payload cases,
boundary/order checks and four integrity negatives passed in a coordinator
rerun. Region-base provenance is bounded initialization-prefix evidence, not
complete loader validation or reference execution.

Only NewWithAddressSpace enables4f; legacy constructors remain unsupported.
Host checks configuration, underflow, then nonnegative complete-word global
RAM bounds before cached write/pop. Positive4000 is a direct RAM word index,
not file tagging. Popped-slot clearing is host hygiene. External public tests
cover0/1/2/65 depth, negative and high positive indexes, odd/end bounds,
legacy precedence, exact sticky state, no-inline fetch, budgets, descriptor
crossings/aliases, independent input snapshots and return preservation.
Tests-first red and focused/full/386 tests plus vet passed. Independent
frozen review found no blockers by inspection.

The same nine original public decoder/kernel observations advance selected
c4f6ade5 from65 to129 successful instructions, next unsupported3a at835.
Other seven v2 cases and the unsupported v1 remain unchanged. The configured
subset now covers26 opcode values. No reserved initialization, service, frame
or ordinary product startup is implied.

Frozen public ordered tests, Windows product builds, core portability and
official Android binder passed. Official deltas show21synthetic and496private
rows unchanged. Private-all alone exits1 with the same six missing KTF/Raptor
prerequisites; full configured workspace acceptance is not green.
