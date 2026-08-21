# System firmware implementation progress

This file records measured system-mode behavior. It does not promote a parser,
diagnostic entry point, or probe-assisted trace into a cold-boot claim.

## Implemented foundation

- `firmwareset` hashes bounded random-access inputs and emits a path-free
  manifest. Core never needs a host filename to retain firmware identity.
- `loader/samsung` identifies WBT, WBIN, DAT, and FNT roles from the common
  wrapper magic and role tokens, independently of source order and filename.
- The Samsung loader scans WBT erase blocks for MIBIB copies, selects the
  newest valid generation, parses bounded partition entries, and cross-checks
  the WBIN footer against the WBT payload end and the RSRC, FONT, and final
  partition boundaries.
- The exact SCH-W830 DL21 four-piece hash set selects a data-driven build
  profile. That profile reconstructs original OEMSBL and QCSBL images by
  validating and stripping their per-block headers, then checks the complete
  logical image hashes.
- The WBIN decoder validates its signed-wrapper transform fields, implements
  the public RFC 4269 SEED primitive, applies Samsung's per-block key feedback,
  and parses the resulting progressive ARM ELF32 image. It does not consult a
  memory dump or retain a private key table.
- The normalized flash view places decoded WBIN bytes at the MIBIB `0:AMSS`
  origin and streams WBT, DAT, and FNT payload bytes from their authenticated
  input handles. Gaps read as erased `0xFF`; every populated range records its
  source hash, source offset, transform, and output hash.
- `system.COWFlash` keeps erase-block writes separate from the immutable input,
  enforces NAND's 1-to-0 programming rule, supports factory reset, and binds
  deterministic save states to the normalized flash identity.
- `system.QualcommNAND` exposes the bounded address, read-command, status, and
  512-byte data-window behavior used by the original QCSBL. Four commands read
  one 2 KiB NAND page from the copy-on-write flash view; unsupported commands
  and registers fail instead of succeeding implicitly.
- The named `qualcomm.pbl-hle.nand2k-v1` handoff supplies only the PBL-owned
  register magic and service table consumed by QCSBL. Its flash geometry is
  derived from the assembled image, and the unavailable mask ROM remains an
  explicit HLE boundary.
- The early Qualcomm boot-control models expose the board-supplied hardware
  revision and NAND-interface mode, watchdog service, and bounded primary and
  secondary clock/reset latches. Every access outside those compatibility
  contracts faults.
- `system.Bus` provides non-overlapping RAM, ROM, and typed MMIO regions with
  permissions, alignment and boundary faults, deterministic reset, and
  component state serialization.
- The portable interpreter can attach that bus without changing its
  application-mode private-memory path. Its advertised system capabilities
  currently cover the physical bus, ARM privileged modes with banked
  registers, and bounded CP15 control state. It explicitly does not advertise
  MMU, architectural exceptions, or interrupt lines yet.

## Private SCH-W830 DL21 evidence gate

When `ARAM_REFERENCE_REPO` is configured, the private gate currently proves:

| Check | Measured result |
|---|---|
| filename-independent complete-set recognition | pass |
| exact four-piece build-profile match | `samsung.sch-w830.dl21` |
| selected MIBIB generation / partitions | `2` / `10` |
| OEMSBL logical image | exact profile hash match |
| QCSBL logical image and entry | exact profile hash / `0x00080028` |
| decoded WBIN logical image | `0x015A0000` bytes / exact profile hash match |
| WBIN progressive ELF | 11 program headers / logical end `0x040CCAF4` |
| normalized flash geometry | `0x097C0000` bytes / four attributed source regions |
| normalized WBIN / DAT / FNT starts | `0x002A0000` / `0x01C00000` / `0x04F00000` |
| PBL-HLE service table | 2 KiB NAND geometry at `0x78001000`; `R7=0xA1B2C3D4`, `R8=0x78001000` |
| original QCSBL to original OEMSBL | `1,195,629` instructions / entry `0x000A07D8` |
| OEMSBL execution after handoff | `5,402,441` additional instructions |
| secondary clock terminal selector / value | `0x36` / `0x00000004` |
| current PC / dependency boundary | `0x00107FFC` / missing secure-module entry |
| `0:SIM_SECURE` input contents | `0x00080000` bytes, all zero |
| watchdog services before boundary | `338` |

The reset/PBL-HLE path places only reconstructed QCSBL bytes at their profiled
load address, supplies the `0x78000000..0x78010000` PBL IRAM window and service
table, and starts the original QCSBL at `0x00080028`. QCSBL reads the selected
MIBIB and OEMSBL partition through the modeled NAND device, copies OEMSBL into
RAM, and invokes original OEMSBL code at `0x000A07D8`.

The handoff table entries are the QCSBL-consumed page size, pages per erase
block, erase-block count, bad-block compatibility value, NAND2K selector, and
terminator. No host pointers, filenames, dump bytes, or preloaded OEMSBL bytes
participate in this path. The early boot-control compatibility registers are
explicit and stateful; they are not a general read-zero/write-drop probe.

After the first OEMSBL clock boundary, the bounded secondary clock model
accepts only offsets `0x400`, `0x404`, `0x430`, and `0x434`. The original path
then reaches `0x00107FFC`, while that address contains no executable input
bytes and the normalized `0:SIM_SECURE` partition is entirely zero. An
optional companion memory snapshot contains security-test code at that address
and is used only to identify the missing module class; none of its bytes are
loaded by the emulator or retained in the product.

This establishes `boot-stage-entry` only. The trace starts at an original
secondary-boot entry after the named mask-ROM HLE; it does not execute the
missing mask ROM or SIM-secure module, reach AMSS, display a frame, or consume
keypad input. The WBIN is present in reconstructed flash but has not yet been
loaded by the original boot chain. The current compatibility model cannot
support a higher milestone.

The memory dump is not read by either private gate and is not a runtime input.

## Next measured boundary

The next implementation target is a named HLE contract for the absent
SIM-secure module, beginning with the call ABI at `0x00107FFC`. It must model
only evidenced return values and state changes; copying code from a memory
snapshot into runtime RAM is explicitly disallowed. The boot is rerun from the
same PBL-HLE boundary after every added contract so each dependency is causal
and reproducible.

Further OEMSBL and AMSS progress is expected to require interrupt, timer,
clock, GPIO, and related platform blocks. CP15 translation-table walking,
abort behavior, and MMU-backed virtual accesses remain required before MMU
enable can succeed. No access beyond the current boundary will be treated as
successful until its owner and minimum semantics are evidenced.
