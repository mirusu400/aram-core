# ARAM Core

[![ci](https://github.com/mirusu400/aram-core/actions/workflows/ci.yml/badge.svg)](https://github.com/mirusu400/aram-core/actions/workflows/ci.yml)

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
hash-keyed OEM services. It deliberately faults on unknown instructions.

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
- bounded diagnostic snapshots containing CPU registers, the last execution
  result, guest log output, and runtime trace tails without source bytes,
  guest memory, framebuffers, persistence, or media payloads.

Windows, menus, native file dialogs, touch overlays, and product navigation
live in [`aram-frontend`](https://github.com/mirusu400/aram-frontend).
The ecosystem roadmap and release integration live in
[`aram-emu`](https://github.com/mirusu400/aram-emu).

## Portability baseline

The default module is pure Go and must build on Windows, Linux, macOS, and
Android/arm64. Optional CPU backends that require C libraries belong behind
build tags and stay optional for loaders, profiles, and state formats.

```powershell
go test ./...
go vet ./...
```

Every push and pull request tests, vets, and compiles all core packages on
Windows x64, Linux x64, and macOS arm64. The workflow also publishes compressed
development artifacts containing `aram-debug`, `aram-skvm-inspect`, and
`aram-skvm-run`; artifacts are retained for 14 days. Android/arm64 remains a
separate pure-Go compile gate.

A successful push to `main` moves the `nightly` tag to that commit and updates
the rolling Nightly prerelease with the exact Windows, Linux, and macOS
archives from the workflow plus `SHA256SUMS.txt`. The release is only updated
after every native build, the Android compile gate, and the input policy pass.
Publishing any GitHub Release other than `nightly` builds the tagged source and
attaches the same archives for the Stable channel.

The standard-facing design basis is recorded in
[`docs/wipi-1.2.1-foundation.md`](docs/wipi-1.2.1-foundation.md).
The Magic Hole-first, multi-platform system-mode plan is recorded in
[`docs/system-firmware-roadmap.md`](docs/system-firmware-roadmap.md).
Measured implementation status and the current original-firmware trace
boundary are recorded in
[`docs/system-firmware-progress.md`](docs/system-firmware-progress.md).
