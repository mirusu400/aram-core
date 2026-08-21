# System firmware implementation progress

This file records measured system-mode behavior. It does not promote a parser,
diagnostic entry point, reference-dump observation, or budget-limited trace into
a complete cold-boot claim.

## Implemented foundation

- `firmwareset` hashes bounded random-access inputs and emits a path-free
  manifest. Core never needs a host filename to retain firmware identity.
- `loader/samsung` identifies WBT, WBIN, DAT, and FNT roles from wrapper
  contents, selects and validates MIBIB metadata, reconstructs the original
  OEMSBL/QCSBL images, decodes the progressive WBIN, and assembles an immutable
  logical NAND image. Source ordering and filenames are not part of matching.
- `system.COWFlash` keeps erase-block writes separate from immutable firmware,
  enforces NAND's 1-to-0 programming rule, supports factory reset, and binds
  save states to the normalized flash identity.
- `system.QualcommNAND` implements the evidenced 2 KiB-page controller path,
  including identification, configuration, ready signaling, command/address
  latches, and the 512-byte data window used by the original boot stages.
- The named `qualcomm.pbl-hle.nand2k-v1` boundary supplies only the unavailable
  mask-ROM service table and register contract consumed by QCSBL. Geometry is
  derived from the assembled image; OEMSBL and AMSS are not host-preloaded.
- The interpreter implements ARMv5 short-descriptor translation for sections,
  coarse/fine tables, large/small/tiny pages, domains and AP permissions,
  FCSE, CP15 fault state, deterministic software TLB invalidation, and
  MMU-aware public memory access. Translation/domain/permission faults enter
  architectural prefetch/data-abort vectors; external physical-bus aborts are
  still reported as host execution faults.
- ARM block transfers implement privileged `^` behavior for user-register-bank
  access and `LDM ... pc^` exception return with CPSR restoration.
- The interpreter accepts level-sensitive IRQ/FIQ inputs, applies mask and
  priority checks at instruction boundaries, enters low or high ARM vectors,
  delivers system-mode SWI and MMU prefetch/data aborts, and supports
  `MOVS/SUBS pc,...` exception return. External physical-bus abort delivery
  and reset/undefined exception entry remain incomplete.
- Board profiles describe RAM/IRAM/high-vector windows and exact-width
  read-only board registers, fixed-width external latches, boot-control
  halfword/word layouts, SBI instances, and board-specific clock/sleep
  controllers. `SparseWordRegisters` provides a reusable, stateful device for
  explicitly evidenced register layouts without turning reserved MMIO gaps
  into read-zero/write-drop space.
- Qualcomm compatibility devices now cover the NAND/PBL controls, MPMC register
  set, clock/reset latches, sparse clock-regime apertures, evidenced IRQ setup
  words, a stable-pair timetick compatibility counter, and timetick-match
  synchronization status. The
  separate INTCTL model implements two 32-source sticky status banks,
  IRQ/FIQ enables, source deassert-then-clear acknowledgement, and CPU output
  lines at the family-reference `CHIP_BASE+0x0900` window.
  Unknown addresses and widths still fault.
- CPU-attributed MMIO observation records the guest ARM/Thumb instruction PC,
  physical address, width, direction, value, region, and error without changing
  device behavior. It is optional and inactive during normal execution.
- `ClockedRunner` advances reusable devices in bounded instruction-retirement
  quanta while preserving ARM/Thumb mode across slices. The Qualcomm timetick
  can now convert a configured deterministic instruction rate into sleep-clock
  ticks, synchronize match writes, pulse a profile-selected INTCTL source, and
  serialize its fractional phase. A synthetic machine executes and
  acknowledges the resulting timer IRQ end to end.
- The generic 16-bit parallel-panel transport records original command/data
  writes. Panel-controller commands, a pixel surface, scanout, and host input
  are not implemented yet.
- All added devices and CPU-visible MMU state participate in deterministic
  reset/save/load behavior.

## Private SCH-W830 DL21 evidence gate

When `ARAM_REFERENCE_REPO` is configured, the private gate currently proves:

| Check | Measured result |
|---|---|
| filename-independent complete-set recognition | pass |
| exact four-piece build-profile match | `samsung.sch-w830.dl21` |
| selected MIBIB generation / partitions | `2` / `10` |
| OEMSBL and QCSBL logical images | exact profile hash matches |
| QCSBL entry | `0x00080028` |
| decoded WBIN logical image | `0x015A0000` bytes / exact profile hash match |
| WBIN progressive ELF | 11 program headers / logical end `0x040CCAF4` |
| normalized flash geometry | `0x097C0000` bytes / four attributed source regions |
| normalized WBIN / DAT / FNT starts | `0x002A0000` / `0x01C00000` / `0x04F00000` |
| PBL-HLE service table | 2 KiB NAND geometry at `0x78001000`; `R7=0xA1B2C3D4`, `R8=0x78001000` |
| original QCSBL callback boundary | `1,195,629` instructions / `0x000A07D8` |
| original post-callback firmware execution | `562,000,000` additional instructions, budget stop, no fault |
| enabled interrupt masks after warmup | IRQ `00000030/08980000`; FIQ `00000000/00000058` |
| panel transport writes / terminal values | 852 commands, 196,441 data / command `0x2C`, data `0xFFFF` |
| watchdog service writes | 24,367 |
| diagnostic HLE invocations | none |

The reset path places only reconstructed QCSBL bytes at the profiled load
address, provides the bounded PBL IRAM/service-table contract, and starts the
original QCSBL. QCSBL reads MIBIB and OEMSBL through the modeled NAND device.
The original firmware then initializes two sleep controllers, local SBI/SSBI
paths, three differently shaped controller instances at `0x80004000` through
`0x8000423C`, the panel, MMU, clocks, timer/control banks, and IRQ/FIQ masks. It
performs privileged exception return into transformed runtime code, services
the watchdog, reads the timetick, and continues runtime work without an MMIO
or CPU fault during the measured budget. An attributed trace identifies the
stable-pair reads at Thumb PCs `0x01701DCC`/`0x01701DD0` and the
match-write/synchronization loop at `0x017D2942`..`0x017D295C`. The reference
register contract identifies `0x800054C4` as a 32-bit free-running timetick
match value and `0x800054C0` bit 0 as its synchronization status. CPU IRQ/FIQ
entry and INTCTL line delivery are implemented, but the SCH-W830 timer source
number and clock cadence are not yet evidenced and wired. Enabled interrupt
masks alone do not prove interrupt-driven OS progress.

Forward execution also falsified the earlier hypothesis that `0x00107FFC` was
a required SIM-secure module. It is an OEM fatal/assert diagnostic reached
after hardware initialization failure. Traps guard that address and the flash
initialization failure handler; neither is invoked in the measured run.

The private gate reads the four firmware pieces only. The phone memory dump is
not opened, copied into guest memory, or included in save state. It has been
used manually as a reference oracle to identify analogous functions and board
register layouts; those observations are converted into explicit, tested
device contracts before they enter the emulator.

## Current limit and next measured boundary

This is not a complete phone boot yet. The gate has not demonstrated a decoded
LCD frame, home screen, keypad input, modem/SIM behavior, audio, persistent
user storage, or application launch. The firmware emits a substantial LCD
command/data stream and reaches a clean 10-million-instruction post-warmup
budget, but instruction count and transport traffic alone are not evidence of
a correct UI frame.

The next targets are:

1. Evidence the SCH-W830 timetick interrupt source and deterministic
   instruction-rate conversion, put both in its platform profile, and run the
   private gate through `ClockedRunner`. The generic scheduler, rational
   timetick advancement, match pulse, and synthetic IRQ/ACK path are complete.
2. Deliver external physical-bus and undefined-instruction exceptions without
   hiding unsupported interpreter or MMIO implementation boundaries.
3. Decode the SCH-W830 panel-controller command stream into a pixel surface and
   identify any MDP/framebuffer handoff used after the boot splash.
4. Add profile-declared keypad/GPIO input only after the firmware reaches its
   evidenced polling or interrupt path.

Other Samsung builds should reuse the CPU, bus, flash, NAND, timer, sparse
register, and panel primitives while selecting different board-profile facts.
No Magic-Hole address or reset value is treated as universal Qualcomm state.
