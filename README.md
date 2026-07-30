# ARAM Core

Headless, frontend-independent emulation core for **ARAM (Archived Runtime for
ARM Mobiles)**.

This repository owns:

- machine lifecycle, framebuffer, audio, input, and state contracts;
- replaceable ARM/Thumb CPU backend contracts;
- safe WIPI container and firmware-image loaders;
- carrier, manufacturer, device, and title profiles;
- deterministic time, replay, save-state, patch, and debugger boundaries.

The current application-mode milestone includes a bounds-checked EADS loader,
a pure-Go ARMv5TE/Thumb interpreter, and a native application machine that maps
text, zeroed BSS, a guarded execution stack, deterministic guest heaps, and
hash-keyed OEM services. It deliberately faults on unknown instructions. For
the authorized Magic Hole DAT, the machine executes the recovered
`0x1100`, `0x1101`, `0x0504`, `0x0505`, `0x0505` lifecycle, dispatches the
observed EADS services, and renders the first visible `MinigameQVGAOEM` frame.
The private integration oracle verifies exact instruction/API-call counts and
RGBA SHA-256
`0ae34e616ac40a0dab1e35d907acfef63fb47bd2b065875f17631f0bbeb915a7`.

The usable library baseline now includes:

- bounded `io.ReaderAt` inspection with SHA-256 and ordered ABHS/EADS markers;
- unified DAT record inspection plus validated ABHS relocation and EADS
  text/BSS loading;
- composable WIPI, carrier, manufacturer, device, and hash-keyed title
  profiles, including WIPI physical/virtual key values and framebuffer format;
- validated frontend sources, input events, audio chunks, and immutable
  framebuffer snapshots;
- versioned CPU backend identity, bounds/permission checked guest memory, and
  portable CPU context serialization;
- checksummed application save states containing source identity, CPU context,
  text/BSS/stack bytes, framebuffer contents, queued input, deterministic
  title-runtime counters, allocator metadata, and service-owned heap bytes.

It does not own windows, menus, native file dialogs, touch overlays, or product
navigation. Those live in
[`aram-frontend`](https://github.com/mirusu400/aram-frontend).
The ecosystem roadmap and release integration live in
[`aram-emu`](https://github.com/mirusu400/aram-emu).

## Portability baseline

The default module is pure Go and must build on Windows, Linux, macOS, and
Android/arm64. Optional CPU backends that require C libraries belong behind
build tags and cannot become mandatory for loaders, profiles, or state formats.

```powershell
go test ./...
go vet ./...
```

Set `ARAM_REFERENCE_REPO` to a local `anycall_magichole` checkout to enable the
private real-DAT integration tests. They validate all six ABHS loads plus the
hash-keyed EADS lifecycle, first-frame pixel oracle, reset, save-state restore,
and deterministic next-frame replay. No proprietary input is part of this
repository.

The standard-facing design basis is recorded in
[`docs/wipi-1.2.1-foundation.md`](docs/wipi-1.2.1-foundation.md).
