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
- `system.QualcommNAND` exposes the bounded address, read-command, status,
  controller configuration, identification, ready signaling, and 512-byte
  data-window behavior used by the original boot stages. Four commands read
  one 2 KiB NAND page from the copy-on-write flash view; unsupported commands
  and registers fail instead of succeeding implicitly. NAND identity remains
  board-profile data rather than a generic Qualcomm-controller constant.
- The named `qualcomm.pbl-hle.nand2k-v1` handoff supplies only the PBL-owned
  register magic and service table consumed by QCSBL. Its flash geometry is
  derived from the assembled image, and the unavailable mask ROM remains an
  explicit HLE boundary.
- The early Qualcomm boot-control models expose the board-supplied hardware
  revision, NAND-interface mode and ready line, EBI memory configuration,
  watchdog service, and bounded primary and secondary clock/reset latches.
  Every access outside those compatibility contracts faults.
- The generic 16-bit parallel-panel interface records only the command/data
  writes issued by firmware. The observed SCH-W830 panel sequence is therefore
  original guest execution, not a pre-rendered host UI.
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
| original QCSBL OEM callback boundary | `1,195,629` instructions / `0x000A07D8` |
| board EBI RAM configuration | `0x00005880` / 128 MiB |
| NAND controller config / read ID | `0xE8D408C0`, `0x0004745C`, `0x1D` / `0xECAA` |
| panel writes / terminal values | 57 commands, 110,114 data / command `0x29`, data `0xFFFF` |
| OEMSBL execution after callback boundary | `6,036,311` additional instructions |
| current instruction / result PC | MMU enable at `0x000BC2D8` / `0x000BC2DC` |
| `0:SIM_SECURE` input contents | `0x00080000` bytes, all zero |
| watchdog services before boundary | `721` |

The reset/PBL-HLE path places only reconstructed QCSBL bytes at their profiled
load address, supplies the `0x78000000..0x78010000` PBL IRAM window and service
table, and starts the original QCSBL at `0x00080028`. QCSBL reads the selected
MIBIB and OEMSBL partition through the modeled NAND device, copies OEMSBL into
RAM, and reaches the measured OEM callback boundary at `0x000A07D8`.

The handoff table entries are the QCSBL-consumed page size, pages per erase
block, erase-block count, bad-block compatibility value, NAND2K selector, and
terminator. No host pointers, filenames, dump bytes, or preloaded OEMSBL bytes
participate in this path. The early boot-control compatibility registers are
explicit and stateful; they are not a general read-zero/write-drop probe.

Forward execution falsified the earlier hypothesis that `0x00107FFC` was a
required missing SIM-secure entry. It is an OEM fatal/assert diagnostic reached
only after a hardware initialization failure. An execution trap now guards
that diagnostic and the flash-initialization failure handler; neither is
invoked after the NAND-ready handshake, controller identity/configuration,
clock status, EBI RAM size, and panel interface are modeled explicitly.

The original OEMSBL initializes the panel and then executes the CP15 control
write at `0x000BC2D8` that enables address translation. The interpreter stops
precisely after that instruction because MMU translation is not implemented.
This is the current causal dependency boundary; no synthetic secure-module
success return is used.

This establishes `boot-stage-entry` and original panel initialization only.
The trace starts at an original secondary-boot entry after the named mask-ROM
HLE; it does not execute the missing mask ROM, reach AMSS, present a complete
frame, or consume keypad input. The WBIN is present in reconstructed flash but
has not yet been shown to reach its AMSS entry point. The current compatibility
model cannot support a higher milestone.

The memory dump is not read by either private gate and is not a runtime input.

## Next measured boundary

The next implementation target is ARMv5 short-descriptor MMU translation:
first-level section and coarse-page-table walks, small/large page mappings,
domain and AP permission checks, CP15 fault state, and deterministic TLB
invalidation semantics. The boot is rerun from the same PBL-HLE boundary after
each implementation increment so every dependency remains causal and
reproducible.

Further OEMSBL and AMSS progress is expected to require architectural aborts,
interrupts, timers, clocks, GPIO, and related platform blocks. No access beyond
the current boundary will be treated as successful until its owner and minimum
semantics are evidenced.
