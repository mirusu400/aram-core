# Generic firmware system-mode roadmap

## Objective

ARAM system mode will execute user-supplied feature-phone firmware as a whole
machine. The first vertical target is a reproducible Samsung SCH-W830 Magic
Hole boot, but the architecture must admit other firmware families without
copying the SCH-W830 machine or scattering model-name checks through CPU,
memory, and device code.

The target is a framework with shared execution and device contracts plus
explicit platform, board, and firmware-build definitions. It is not one
universal hardware model that pretends unrelated Samsung phones are the same
machine.

Application mode remains a separate product mode. It is the faster path for
running individual WIPI applications with host services. System mode runs the
original boot chain, operating system, application manager, and built-in
runtime against modeled hardware.

## First product claim

The first supported system profile is:

- manufacturer: Samsung;
- board family: SCH-W830 Magic Hole;
- firmware build: DL21, identified by exact piece hashes;
- CPU/platform family: selected only after evidence pins the required
  Qualcomm variant and architectural features;
- boot input: the original WBT, WBIN, DAT, and FNT pieces supplied by the user;
- memory dump: optional reference evidence, never a required boot input.

"Magic Hole boots" means all of the following are reproducible:

1. the firmware set is identified, validated, and normalized without trusting
   filenames;
2. reset starts from a documented CPU and board state;
3. original boot and AMSS code executes through named boot milestones;
4. a guest-produced boot or idle UI is visible through the system framebuffer;
5. host keypad input reaches the guest and changes the UI;
6. reset produces the same milestone trace and frame evidence;
7. missing hardware and HLE boundaries are listed explicitly;
8. no memory dump bytes, patched firmware, or hidden title launcher are needed.

This claim does not imply working cellular service, RF, camera, Bluetooth, or
arbitrary application compatibility.

## Evidence and input policy

System mode keeps three kinds of material distinct:

| Material | Role | Product runtime dependency |
|---|---|---|
| firmware pieces | authoritative bytes executed by the system machine | required, user supplied |
| memory dump | address-map, vector, structure, and checkpoint oracle | never required |
| reverse-engineering notes and exporters | reviewed facts used to create profiles and tests | never required |

The memory dump may establish expected vectors, loaded addresses, table
layouts, runtime strings, and privacy-safe checkpoint hashes. A successful
test must not restore the dump into RAM and call that a firmware boot. Dump
comparisons run only when the user opts in through the private reference gate.

No firmware, dump, key, extracted asset, archive member, absolute private path,
or derived bulk image is committed. Checked-in fixtures are synthetic. Private
reports may retain hashes, model/profile identifiers, milestones, normalized
fault classes, and bounded scalar traces.

## Architectural boundaries

```text
aram-frontend
    presentation, input, settings, file/document handles
          |
aram-emu system adapter
    firmware-set selection, product commands, packaging, reports
          |
aram-core system machine
    lifecycle, deterministic state, boot coordination
          |
    +-----+--------------------+------------------+
    |                          |                  |
CPU and MMU              physical bus       device scheduler
    |                          |                  |
ARM execution       RAM / ROM / MMIO       clocks / IRQ / DMA
                               |
                  platform and board definitions
                               |
                  normalized firmware / flash image
```

`anycall_magichole` remains an evidence and reference repository. It does not
become a product dependency and does not supply tracked product code.

### Reusable system machine

The system machine owns:

- reset, start, pause, resume, stop, frame step, and deterministic time;
- CPU, physical address space, devices, and interrupt coordination;
- framebuffer, audio, input, persistent storage, and save-state contracts;
- structured boot milestones, execution faults, and bounded traces;
- source, platform, board, and firmware-build identities.

Application and system machines may share CPU and guest-neutral services, but
they do not share one bootstrap or force hardware MMIO through WIPI HLE.

### CPU system contract

The existing application CPU contract remains usable by application mode.
System mode adds capability-based contracts instead of requiring every backend
to emulate privileged hardware unconditionally.

Required capabilities include:

- ARM and Thumb execution for the architecture selected by the platform;
- privileged processor modes and banked registers;
- CPSR and SPSR behavior;
- reset, undefined-instruction, SVC/SWI, prefetch/data-abort, IRQ, and FIQ
  exception entry and return;
- CP15 control, fault, translation-table, domain, and cache-maintenance state;
- MMU address translation, access permissions, faults, and deterministic TLB
  invalidation;
- physical memory accesses routed through a bus that can dispatch MMIO;
- explicit interrupt lines and deterministic instruction accounting;
- complete serializable CPU and MMU state.

Cache data does not need a cycle-accurate host representation initially, but
guest-visible maintenance, coherency, and fault behavior must be correct.
Unknown coprocessor operations fail with the exact instruction and PC rather
than silently succeeding.

### Physical bus and MMIO

The bus is independent of one phone model. It owns:

- non-overlapping physical regions with explicit width and alignment rules;
- RAM, ROM, flash windows, aliases, and device-backed MMIO;
- little-endian typed reads and writes;
- unmapped, read-only, alignment, and device fault reporting;
- trace filters and bounded first-access diagnostics;
- state-component registration and deterministic restore ordering.

Device handlers receive typed accesses. They do not receive application-mode
guest pointers or frontend objects.

### Device model contract

A reusable device has:

- an identity and state schema version;
- reset and optional power-domain behavior;
- bounded MMIO read/write operations;
- deterministic advancement against virtual time or device clocks;
- interrupt output lines and optional DMA requests;
- validated save and restore;
- trace events that do not change behavior.

Initial device families are interrupt controllers, timers, clock/reset control,
GPIO/keypad, UART, NAND/NOR, DMA seams, display scanout, audio output, watchdog,
and a safe offline modem boundary.

### Platform, board, and firmware-build definitions

Differences are layered:

| Layer | Examples of owned facts |
|---|---|
| CPU architecture | instruction set, privileged features, MMU form |
| SoC platform | interrupt controller, timers, clocks, DMA, peripheral blocks |
| board | RAM size, flash wiring, panel, keypad matrix, GPIO routing |
| carrier/device profile | locale, feature flags, safe modem/SIM behavior |
| firmware build | piece hashes, partition layout, boot-stage graph, quirks |

A platform definition constructs reusable devices and maps their registers. A
board definition wires devices and supplies geometry. A firmware-build profile
identifies bytes and boot expectations. None of these layers may select a
behavior merely because an input directory or archive filename contains a
model name.

Model-specific code is allowed only when evidence shows truly model-specific
semantics. It must live behind a named board/device quirk with a source and a
test. `if model == "SCH-W830"` branches in the interpreter, bus, or generic
devices are prohibited.

## Firmware ingestion

### Firmware sets

Many phones require several cooperating files. Product integration therefore
passes a validated firmware set, not a concatenated temporary file or a
directory path assumed to be readable on every host.

A firmware set records:

- one or more seekable, user-authorized source handles;
- per-piece size and SHA-256;
- detected container family and role candidates;
- optional user-selected board/profile when detection is ambiguous;
- no persistent absolute host paths in state or reports.

### Detection and normalization

Format detection uses bounded header and structural checks. It produces a
normalized description rather than immediately constructing a machine:

```text
FirmwareSet
  -> detected package family
  -> validated pieces and roles
  -> normalized flash regions / boot images / resources
  -> platform and board candidates
  -> explicit unsupported or ambiguous result
```

Family loaders own proprietary container structure. The normalized result uses
generic physical addresses, permissions, flash geometry, image identities,
entry candidates, and boot-stage metadata.

The initial loader handles the SCH-W830 WBT/WBIN/DAT/FNT set. Later loaders may
handle raw JTAG images, S3/CTS, CLA/TFI, CRP, Broadcom BOOTFILES packages,
Bada-style image sets, or other evidenced families. Recognizing a container
does not claim the corresponding SoC is implemented.

### Flash safety

- Source files are never modified in place.
- Decoding has explicit input, output, piece, segment, and allocation limits.
- Authentication is modeled as guest-observed state; it is not bypassed by
  patching source bytes.
- Writable flash is copy-on-write and persisted separately from the input.
- A factory reset clears only the integration-owned writable overlay.
- Save states record firmware hashes and reject a different build.

## Boot strategy

Boot stages are a profile-defined graph. The implementation must not assume an
OEMSBL/QCSBL order until evidence and execution establish it for the selected
build.

The first profile may use a documented HLE boundary for missing on-chip primary
boot ROM. That boundary initializes only state the unavailable ROM is proven to
own and then transfers control to original firmware code. It is named in every
trace and compatibility record.

Bring-up progresses forward from reset and uses later entry points only as
diagnostic isolators:

1. reconstructed flash and boot images;
2. reset/PBL-HLE handoff;
3. first original secondary-boot instruction;
4. partition lookup and progressive-image validation;
5. AMSS segment placement and handoff;
6. exception vectors, RTOS initialization, and scheduler alive;
7. display initialization and first guest frame;
8. idle UI and keypad interaction;
9. application-manager launch of a built-in title.

Starting at AMSS or a memory-dump vector is useful for isolating CPU and device
failures. It does not satisfy an earlier boot milestone.

## Execution and device modeling policy

System bring-up is trace-driven:

1. run to the first unsupported instruction, exception mismatch, or MMIO
   access;
2. reduce it to the smallest synthetic reproduction;
3. identify the owning architecture, platform, board, or firmware layer;
4. add a focused synthetic test;
5. implement evidenced semantics or an explicitly named compatibility model;
6. rerun from reset and compare the full milestone trace;
7. run all application-mode and portability gates.

Unknown MMIO reads do not default to zero indefinitely. Exploration may use a
profile-scoped probe policy that records bounded reads and candidate values,
but a boot milestone requires a real model or an explicit documented HLE
contract. Writes are never discarded silently.

Real cellular networks remain disconnected by default. The modem boundary may
report absent service, a deterministic virtual SIM state, and recorded request
metadata without transmitting over RF or a carrier network.

## Milestones

### Phase 0: evidence manifest and reproducible probes

Deliverables:

- exact private-reference piece hashes and roles recorded process-locally;
- checked-in synthetic lookalike fixtures for each structural rule;
- a privacy-safe firmware manifest schema;
- a headless probe result schema with named boot milestones;
- a baseline report that distinguishes recognized, mapped, entered, alive,
  visible, interactive, and application-running states.

Exit gate:

- the SCH-W830 set is identified without filenames;
- malformed and partial sets fail with piece and offset information;
- reference reports contain no paths or bytes.

### Phase 1: generic system contracts

Deliverables:

- system machine lifecycle separate from `application.Machine` internals;
- physical bus, MMIO region, device, interrupt-line, and state-component
  contracts;
- platform, board, and firmware-build registries;
- capability negotiation for system CPU features;
- synthetic RAM/ROM/MMIO machine tests.

Exit gate:

- a synthetic board boots a tiny ARM fixture through reset, timer IRQ, MMIO
  framebuffer, keypad input, save/restore, and deterministic replay;
- application-mode tests and CPU benchmarks do not regress unexpectedly.

### Phase 2: SCH-W830 flash reconstruction

Deliverables:

- bounded WBT block and MIBIB parsing;
- normalized partition geometry;
- reconstructed original boot images with pinned identities;
- bounded WBIN package decoding into the progressive ARM image;
- read-only DAT and FNT flash regions;
- copy-on-write flash overlay.

Exit gate:

- the normalized flash map and boot-image hashes match the private evidence;
- every output byte is attributable to a source range and transform;
- truncation, overlap, overflow, and invalid metadata tests pass.

### Phase 3: privileged ARM and MMU baseline

Deliverables:

- privileged modes, banked registers, SPSR, and exception entry/return;
- required CP15 state and translation-table walking;
- instruction and data abort reporting;
- bus-backed physical fetch/load/store paths;
- deterministic IRQ/FIQ injection;
- system CPU save states.

Exit gate:

- synthetic architecture fixtures pass;
- the first original SCH-W830 boot stage runs until an identified platform
  MMIO dependency rather than an interpreter limitation;
- application-mode CPU tests remain green.

### Phase 4: Qualcomm platform bring-up

Deliverables:

- evidence-pinned SoC identity or explicitly bounded family model;
- interrupt controller, timer, clock/reset, watchdog, GPIO, UART, and flash
  controller minimums;
- reset/PBL-HLE handoff state;
- structured first-access MMIO and exception traces.

Exit gate:

- original secondary-boot code reaches its partition/progressive-image path;
- repeated reset produces identical PC, exception, MMIO, and flash traces;
- no unexplained default-success device access is required.

### Phase 5: AMSS entry

Deliverables:

- progressive segment authentication model and placement;
- AMSS handoff registers and memory contract;
- required early AMSS MMU and exception behavior;
- scheduler/idle-loop liveness detection;
- checkpoint comparison against firmware facts and optional dump evidence.

Exit gate:

- original boot code transfers control to original AMSS code;
- AMSS reaches a reproducible scheduler-alive milestone;
- loaded segment addresses and bounded checkpoint hashes are explained.

### Phase 6: visible and interactive phone UI

Deliverables:

- display controller/scanout and panel profile;
- deterministic frame presentation and frame hashes;
- keypad matrix/GPIO routing and host control map;
- timer and interrupt cadence sufficient for UI tasks;
- backlight and vibration as guest-visible headless state.

Exit gate:

- reset reaches a guest-produced boot or idle frame;
- a scripted key press changes the guest-produced frame;
- replay and save/restore reproduce the same frame and milestone sequence.

### Phase 7: storage, audio, and built-in applications

Deliverables:

- writable filesystem/flash overlay behavior;
- audio-device boundary and PCM drain where codecs are supported;
- safe offline phone/SIM/network state;
- firmware application-manager launch observation;
- system-mode compatibility probe for the built-in MinigameQVGAOEM image.

Exit gate:

- the firmware launches a built-in application through its ordinary UI and
  manager path;
- the title reaches a known frame milestone comparable with application mode;
- input, restart, and persistent data work without modifying firmware inputs.

### Phase 8: prove same-platform reuse

Select a second complete, user-authorized firmware build from the same proven
SoC/platform family. Selection favors complete boot pieces, structural
documentation, a strong identity, and a useful difference in board wiring.

Exit gate:

- the second build reaches at least AMSS entry using the same CPU, bus, and
  platform device implementations;
- differences reside in loader data, board/profile data, or evidenced quirks;
- no SCH-W830 branch is added to generic CPU, bus, or device code.

### Phase 9: prove cross-platform architecture

Choose a structurally different feature-phone family, not Android merely
because an archive is available. A Broadcom BOOTFILES-style package is a
candidate only after its boot images, CPU, and board can be identified.

Exit gate:

- a second platform supplies its own family loader and SoC devices while
  reusing firmware-set, machine, bus, state, trace, and product contracts;
- unsupported peripherals and milestones are reported precisely;
- the first platform does not regress.

## Corpus classification

The private archive is a discovery source, not a product dependency. A local
classifier should inventory archive metadata and bounded member headers without
extracting or retaining proprietary payloads. It groups candidates by:

- package/member format;
- complete versus partial or component-only contents;
- raw/JTAG versus downloader package;
- CPU/SoC evidence;
- boot-stage availability;
- operating-system family;
- model and region labels treated as hints, not trusted identity.

The initial product scope excludes Android, Bada, Windows Mobile, and Symbian
unless they are deliberately accepted as separate platform projects. "Samsung
firmware" is not itself a compatibility family.

Corpus discovery, normalization, privacy-safe result caching, and compatibility
deltas belong to `aram-test`. Loader and execution behavior belong to
`aram-core`. Product commands and adapters belong to `aram-emu`.

## Verification ladder

Every system change runs the narrow owning test and then the workspace gate.
The system-specific ladder is:

1. synthetic loader and malformed-input tests;
2. synthetic CPU, exception, MMU, bus, and device tests;
3. synthetic reset-to-frame board test;
4. private SCH-W830 firmware probe from reset;
5. optional memory-dump checkpoint comparison;
6. second same-platform firmware probe when configured;
7. all application-mode core tests and vet;
8. `aram-test all` compatibility and delta gate;
9. Windows product builds and Android/arm64 pure-Go core build.

Private reference configuration is an explicit opt-in. If configured, missing
pieces or unexpected skips fail the gate. Hosted CI uses synthetic fixtures
unless authorized inputs are provided securely.

## Compatibility status vocabulary

System-mode reports use milestones distinct from application-mode levels:

| Status | Meaning |
|---|---|
| `firmware-recognized` | complete or partial family identified |
| `flash-mapped` | normalized physical flash layout validated |
| `boot-stage-entry` | named original boot-stage instruction executed |
| `amss-entry` | original AMSS handoff executed |
| `scheduler-alive` | reproducible OS task scheduling or idle loop observed |
| `ui-visible` | guest-produced frame presented |
| `interactive` | guest consumes input and changes state/frame |
| `app-launched` | firmware manager launches a built-in application |
| `cold-boot` | documented reset/PBL boundary reaches the claimed milestone |

No higher status is inferred from a lower one. A memory-dump entry is reported
as `snapshot-entry`, never as `cold-boot`.

## Risk register and pivots

### Missing primary boot ROM

Risk: the on-chip PBL and exact silicon reset state may be unavailable.

Response: define a minimal, evidence-backed PBL-HLE boundary. Continue running
original secondary boot and all later firmware. Report the boundary permanently.

### Ambiguous SoC identity

Risk: firmware strings may contain code for several related Qualcomm parts and
do not alone identify the board silicon.

Response: combine instruction requirements, MMIO addresses, boot code, service
manuals where legally available, and cross-build evidence. Keep the platform
identifier bounded to a family until proven.

### MMIO explosion

Risk: early boot touches many undocumented registers.

Response: record unique accesses and causal branches, implement the smallest
device semantics that satisfy an observed contract, and forbid permanent
read-zero/write-drop fallbacks.

### Interpreter performance

Risk: full firmware executes much more code than application mode.

Response: preserve the portable interpreter as the correctness baseline; add
safe block execution and caching without changing architectural results;
optionally use a native backend behind build tags for development comparison.

### Firmware-family overreach

Risk: the private archive contains unrelated CPUs, operating systems, and
smartphones, making a universal-machine claim meaningless.

Response: support named platform families and boards. Unknown inputs remain
recognized or unsupported with evidence. Add a second platform only after the
first platform's contracts are stable enough to test reuse.

### Application-mode regressions

Risk: privileged CPU and bus work slows or changes existing WIPI execution.

Response: use optional capability interfaces, keep the application fast path,
and require the existing core, corpus, frontend, and integration gates for
every system change.

## Immediate engineering queue

1. Define the privacy-safe firmware-set and probe result schemas.
2. Add synthetic multi-piece firmware fixtures and family-detection tests.
3. Define system machine, bus, MMIO, device, interrupt, and profile contracts.
4. Build a synthetic reset-to-MMIO-frame board before using proprietary bytes.
5. Port reviewed SCH-W830 WBT/MIBIB/WBIN facts into typed profile and loader
   data with private-reference checks.
6. Add privileged ARM modes, exceptions, and the minimum evidenced CP15/MMU
   behavior one focused test at a time.
7. Run original boot code to the first platform-owned MMIO boundary.
8. Implement Qualcomm devices in dependency order until AMSS handoff.
9. Reach `scheduler-alive`, then add display and keypad for `interactive`.
10. Launch the built-in minigame through the firmware's ordinary application
    manager and compare its frame evidence with application mode.
11. Select a second same-platform firmware using the private catalog and prove
    that support is profile-driven.
12. Integrate the system factory through `aram-emu` only after the headless
    `ui-visible` gate is reproducible.

The roadmap advances only through measured milestones. A successful parser is
not a boot, AMSS entry is not a visible phone, and one built-in title is not a
generic Samsung compatibility claim.
