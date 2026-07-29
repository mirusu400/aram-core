# ARAM Core

Headless, frontend-independent emulation core for **ARAM — Archived Runtime for
ARM Mobiles**.

This repository owns:

- machine lifecycle, framebuffer, audio, input, and state contracts;
- replaceable ARM/Thumb CPU backend contracts;
- safe WIPI container and firmware-image loaders;
- carrier, manufacturer, device, and title profiles;
- deterministic time, replay, save-state, patch, and debugger boundaries.

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
private real-DAT integration test. No proprietary input is part of this
repository.
