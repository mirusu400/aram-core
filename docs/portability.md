# Portability

## Required targets

| Target | Baseline | CPU strategy |
|---|---|---|
| Windows | amd64, arm64 later | Unicorn first; portable interpreter fallback |
| Linux | amd64, arm64 | Unicorn first; portable interpreter fallback |
| macOS | amd64, arm64 | Unicorn first; portable interpreter fallback |
| Android | arm64 | interpreter or explicitly packaged native backend |
| iOS | arm64, later | interpreter; JIT cannot be assumed |
| WebAssembly | exploratory | interpreter only |

The core contract does not promise that every optional backend exists on every
host. It promises that format inspection, profiles, deterministic state, and a
portable execution path are not coupled to one desktop C library.

## Mobile constraints

Mobile lifecycle, document picking, permissions, and UI belong to
`aram-frontend`. Core accepts streams, byte sources, or frontend-owned handles;
it does not assume that an Android content URI is a normal filesystem path.

Save states include the backend identity and version. Loading a state produced
by another backend requires an explicit compatibility check rather than
silently accepting incompatible CPU context bytes.
