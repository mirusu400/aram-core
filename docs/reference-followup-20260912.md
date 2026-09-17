# Reference research follow-up: entry mapping and GameCanvas presentation

Date: 2026-09-12. Baseline core `f6ff973`, test runner `db986e1`.
This continues [the first research pass](reference-emulators-20260912.md).
Only independent synthetic code and normalized facts are tracked. Supplied
executables/installers are not run, expiry logic is not modified, and no
proprietary payload, saved database, class or asset is copied into the product.

## GVM: conditional buffer-relative primary entry

All native addresses below refer only to the x86 PE with SHA-256
`c2c170b3c47cd99dc55fdadad31dd1927473b4362868205c5eb49eff96c60719`,
preferred image base `0x400000`. They are not runtime or SGS offsets.

Define B as the pointer in global `0x527798`, and H as the pointer in
`0x5276b8`. A direct writer at `0x40b0a4` assigns B = `0x4e69a0`.
Initializer `0x41dfd0` aliases B into H at `0x41dfde`. Conditional on that
initializer returning nonzero, wrapper `0x41df90` reads E = LE16(H + `0x1c`)
at `0x41dfbb` and calls dispatcher `0x41e3b0` at `0x41dfc0`.

- E = 0 returns before dispatcher-local PC/index updates or opcode fetch.
  This does not assert the surrounding initializer/wrapper has no effects.
- E != 0 resets the two stack indices to -1 and selects PC = B + E.
- Only the low 16 argument bits matter. The observed addition does not use a
  title terminator or ARAM's `Header.BodyOffset`.
- A separate wrapper `0x41e420` reads H + `0x1e`; its event meaning is unknown.

Reader `0x409a70` opens a binary stream, requests a rewind to zero, and at
`0x409bf2` requests 131072 bytes into B. Bounded call-chain inspection reaches
host ReadFile and SetFilePointer imports. Under successful rewind/read, B
corresponds to file offset zero **at that read boundary**. The immediate caller
does not establish rewind success or complete short-read handling.

**Do not yet implement an unconditional SGS entry = file[0x1c] rule.** The
selected buffer's storage/reload and event path from that ingress to the
initializer have not been connected end-to-end. Content transformations,
prefix handling, full initializer conditions and other pointer writers are
not excluded. The newly established contract is conditional local dataflow,
not a complete SGS loader, opcode interpreter, or resource decoder.

The independent hash-gated checker passed 49 structured instruction assertions
in 13 contiguous decode windows, pointer/mode/IAT ownership, and a final input
hash recheck. In-memory changed-byte and truncated-input integrity tests were
rejected before PE parsing. The coordinator reran the checker successfully.
No reference runtime execution or GVM compatibility milestone is claimed.

## GameCanvas.flushGraphics: documented presentation contract

The same verified SDK GameCanvas API document from the first pass specifies:

- Both overloads do nothing when the canvas is not shown.
- Only the intersection of the requested rectangle and canvas bounds is flushed.
- Width or height less than one flushes no pixels, without an exception.
- Flushing does not change the off-screen buffer.

These are standard MIDP documentation contracts, not Motorola-only native
behavior. The document also describes busy-system handling; this work does
not establish handset scheduling or asynchronous busy behavior.

Before the fix, independent public VM guest-bytecode tests observed hidden
whole/partial flushes changing a sentinel pixel (R171 to R55), and negative
width throwing IllegalArgumentException. The frozen ordinary product probe
also fails the new authored MIDlet at instruction 67 with that exception,
no valid frame and zero presentations. Its binary SHA-256 is
`03708875b76782e0ff91206dc331a7cf1d6d535932b3f48426fd237d0daee83b`.
The synthetic JAR SHA-256 is
`b4ed1ce010c87f75570531c1c5d498fb6482dd82ea230f9a41f4e4e1cae367d6`.

### Owning implementation and requirement checks

Both overloads now return without presenting a hidden canvas or a rectangle
with nonpositive dimensions. Coordinates are widened before intersection with
both backing-buffer and screen bounds, then narrowed only for a valid region.
The presentation copy temporarily uses an opaque copy state with full screen
clip and zero translation/transparency, restoring the exact prior state even
when the copy fails. GameCanvas.paint is not changed.

| Requirement | Observed check and result |
|---|---|
| Hidden full/partial flush has no effect | Independent guest bytecode calls inherited methods through public VM.InvokeStatic; sentinel framebuffer preserved after the fix |
| Empty/negative and extreme rectangles | Zero/negative extents no longer throw; min/max coordinates and extents clip safely, including a passing 386 build/test |
| Correct visible intersection | Exact FrameRGBA comparisons cover partial, offscreen and bottom-right regions across J2ME, SKT and LGT |
| Presentation ignores drawing state | Public guest tests cover clip/translation; 24 supplemental runtime-state cases cover retained raster, opacity and transparency, with exact state restoration |
| State/replay preservation | Byte-identical save/replay checks pass; existing LGT paint/flush alpha pixel expectations are unchanged after adding the documented shown-canvas precondition |
| Ordinary MIDlet integration | The identical authored JAR changes from IllegalArgumentException at instruction 67 to ok_frame at instruction 102 with a valid first frame |
| Existing compatibility | Original 19 synthetic payloads and expectations remain locked; same-scope comparison retains all previous 20 synthetic/reference rows and 495 private-scope rows unchanged |

The public VM matrix contains 72 cases. Supplemental runtime-state injection is
service-boundary evidence, not a claim that every such state is reachable from
MIDP bytecode. The ordinary product probe is rebuilt through cmd/aram-probe,
without overlays, special launchers or test-only product switches. Its SHA-256:
`6759b7e1b31bcfadea211eeeaa695788aef00c668e824ac1e32ccbd3a93637e1`.
The recurring runner independently constructs the expected 240x320 RGBA image:
base #123456 with clipped #abcdef corner rectangles. Expected and observed SHA:
`fd975e5f91f4486244f6e35dbb2d7faecc1b014bcf27c58a30169290c1eb1e4b`.
This fixture has no input-response behavior and does not establish playability.

The ordinary Windows cmd/aram product was also launched with this JAR as a
positional argument under isolated application settings. After the ordinary
welcome dialog's Later action, PID-scoped capture confirmed the visible
240x320 output and both corner rectangles: 1300 changed pixels, zero geometry
mismatches. This is GUI geometry evidence, not equality of TFT-filtered screen
pixels to the raw RGBA hash, and not native File/Open-dialog acceptance.
The bounded smoke stopped its own process, verified no lingering tracked
process, transactionally restored only its protocol-registration change, and
independently confirmed the complete typed registry snapshot and existing
installation unchanged. Its temporary captures and settings were removed.

### Ordered workspace validation

After source freeze, core, frontend and integration tests/vet/diff checks passed,
as did 98 runner unit tests and the 20-case synthetic gate. Windows product,
probe and frontend builds, pure-Go Android/arm64, Linux/amd64 and Darwin/arm64
core builds, and the official Android binder passed. The new fixture contributes
one new ok_frame row in each equal-scope comparison, with no regressions.

The configured private all gate still exits 1: the same six required KTF/Raptor
reference tests find no valid matching packages in the supplied legacy corpus.
This is an unresolved acceptance input prerequisite, not a passing gate or an
expected status to weaken. Triage remains led by 358 BREW execution-unsupported,
80 external-savedata-unverified and nine GVM execution-unsupported inputs.
No new commercial compatibility or complete workspace acceptance is claimed.
