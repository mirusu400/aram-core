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
| original QCSBL execution | `56,069` instructions before current probe boundary |
| current PC / access boundary | `0x000831A0` / write `0x00000001` to `0x8000540C` |

The current diagnostic handoff places reconstructed QCSBL bytes at their
profiled load address and supplies the `0x78000000..0x78010000` PBL-IRAM
compatibility window required by QCSBL's `0x7800F000` stack literal. A bounded
MMIO exploration device records reads at offsets `0x0A40`, `0x551C`, and
`0x0274`, returning an explicit candidate zero, then rejects the first write
at offset `0x540C`. Writes are not silently discarded.

As a negative diagnostic, allowing the observed writes without implementing
their device semantics reaches QCSBL's reset/error loop at `0x0008015C`; it
does not advance the boot milestone. This shows that the direct QCSBL entry is
missing an earlier boot-state contract or takes a hardware-error branch, so
the exploratory MMIO values must not be promoted into a device model.

This establishes `boot-stage-entry` only. The trace starts at an original
secondary-boot entry as a diagnostic isolator; it does not yet execute from
reset/PBL, place the decoded WBIN in reconstructed flash, reach AMSS, display
a frame, or consume keypad input. The bounded probe values cannot support a
higher milestone.

The memory dump is not read by either private gate and is not a runtime input.

## Next measured boundary

The next implementation target is a bounded progressive flash view that places
the decoded WBIN at the MIBIB `0:AMSS` origin and exposes the original DAT and
FNT payloads at their partition offsets, including erased gaps and a separate
copy-on-write overlay. That view will be connected to the modeled Qualcomm
flash controller and the reset/PBL-HLE handoff before QCSBL is rerun.

The Qualcomm-family register block at physical `0x80000000` remains the first
observed platform MMIO dependency. The bring-up loop must replace each probe
value with an evidenced register/device contract before claiming the
corresponding boot milestone. CP15 translation-table walking, abort behavior,
and MMU-backed virtual accesses remain required before MMU enable can succeed.
