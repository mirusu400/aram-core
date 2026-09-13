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

## GVM acceptance and remaining boundary

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
The new kernel has 20 supported opcode values with an explicitly configured
address space. Further numeric operations, reserved state, events and graphics
are still required before any GVM game-startup claim.
