# Codex project guide

## Scope

`aram-core` is the headless emulation library. It must never import a windowing,
GUI, native-dialog, Android Activity, or iOS UIKit package.

The sibling repositories are:

- `aram-frontend`: cross-platform presentation, input, and product shell;
- `aram-emu`: ecosystem roadmap, integration, packaging, and release policy;
- `anycall_magichole`: reverse-engineering evidence and executable reference.

## Portability contract

- The default build is pure Go.
- Windows, Linux, macOS, and Android/arm64 builds are required.
- CPU implementations are replaceable through `cpu.Backend`.
- Unicorn or another C backend must be optional and isolated behind build tags.
- A portable interpreter backend remains the fallback for platforms where JIT
  or native libraries are unavailable.
- Core tests do not require a display, audio device, Android SDK, or network.
- Guest and save-state formats use fixed-width types and explicit byte order.

## Architecture rules

- `core` owns machine lifecycle and deterministic state contracts.
- `cpu` owns backend-neutral execution contracts.
- `loader` treats every byte as untrusted and returns offset-bearing errors.
- `profile` separates WIPI standard, carrier, manufacturer, device, and title
  behavior.
- Frontends issue commands through exported contracts; they never mutate guest
  memory directly.
- Virtual time, seeded randomness, storage, input, and network responses are
  serializable machine state.
- Game-specific patches are hash-keyed data with expected-original checks.
- Avoid per-instruction callbacks outside debugger or trace modes.

## Evidence and data

- Do not commit firmware, games, memory dumps, device fonts, IDA databases, or
  extracted proprietary assets.
- Use synthetic test fixtures.
- Private integration tests may use `ARAM_REFERENCE_REPO` or `ARAM_TEST_DATA`.
- A title-specific success is not a carrier-wide or WIPI-wide compatibility
  claim.

## Verification

```powershell
gofmt -w .
go test ./...
go vet ./...
$env:GOOS="android"
$env:GOARCH="arm64"
go build ./...
```

Native CI covers Windows, Linux, and macOS. Android is compile-checked because
core code must not depend on desktop services.
