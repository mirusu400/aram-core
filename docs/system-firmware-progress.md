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
| original QCSBL execution | `56,069` instructions before current probe boundary |
| current PC / access boundary | `0x000831A0` / write `0x00000001` to `0x8000540C` |

The current diagnostic handoff places reconstructed QCSBL bytes at their
profiled load address and supplies the `0x78000000..0x78010000` PBL-IRAM
compatibility window required by QCSBL's `0x7800F000` stack literal. A bounded
MMIO exploration device records reads at offsets `0x0A40`, `0x551C`, and
`0x0274`, returning an explicit candidate zero, then rejects the first write
at offset `0x540C`. Writes are not silently discarded.

This establishes `boot-stage-entry` only. The trace starts at an original
secondary-boot entry as a diagnostic isolator; it does not yet execute from
reset/PBL, decode and place WBIN, reach AMSS, display a frame, or consume
keypad input. The bounded probe values cannot support a higher milestone.

The memory dump is not read by either private gate and is not a runtime input.

## Next measured boundary

The next implementation target is the Qualcomm-family register block reached
at physical `0x80000000`, beginning with the observed hardware-identification
reads and the write to `0x8000540C`. The bring-up loop must replace each probe
value with an evidenced register/device contract before claiming the
corresponding boot milestone. CP15 translation-table walking, abort behavior,
and MMU-backed virtual accesses remain required before MMU enable can succeed.
