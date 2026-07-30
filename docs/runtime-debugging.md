# Headless runtime debugging

`aram-debug` is an agent-friendly debugger for the public `core.Machine`
contract. It has no window-system dependency and does not reach into KTF,
Raptor, EADS, or CPU implementation internals. Runtime work can therefore
continue independently in `application/`, `loader/`, and `cpu/`.

Build it:

```powershell
go build -o build/aram-debug.exe ./cmd/aram-debug
```

Proprietary applications and generated screenshots belong outside the tracked
source tree (for example under the ignored `games/` and `build/` directories).

## Lua scenarios

Run a trusted Lua file against an application:

```powershell
.\build\aram-debug.exe lua .\games\title.kwx .\debug-scenarios\smoke.lua
```

The repository includes a minimal starting point at
[`examples/debug-smoke.lua`](../examples/debug-smoke.lua). A longer KTF input
probe, validated against the `aram-test` corpus, is available at
[`examples/debug-ktf-input.lua`](../examples/debug-ktf-input.lua).

Example scenario:

```lua
aram.start()
aram.step(30)
aram.tap("ok", 2)
aram.step(10)

local screen = aram.screenshot("build/debug/after-ok.png")
print("screen", screen.rgba_sha256, screen.non_black_pixels)

local cpu = aram.cpu()
print("pc", string.format("0x%08x", cpu.registers.pc))

local runtime = aram.runtime()
print("WIPI calls", runtime.wipi.api_calls, runtime.wipi.last_api)

local center = aram.pixel(120, 160)
print("center", center.r, center.g, center.b, center.a)
aram.assert_screen(screen.rgba_sha256)
```

Lua functions:

- `aram.start()`, `aram.step(count)`, `aram.reset()`, `aram.stop()`
- `aram.key_down(control)`, `aram.key_up(control)`,
  `aram.tap(control, hold_frames)`
- `aram.status()`, `aram.cpu()`, `aram.runtime()`, `aram.screen()`,
  `aram.pixel(x, y)`
- `aram.screenshot(path)`, `aram.assert_screen(rgba_sha256)`
- `aram.save_state(path)`, `aram.load_state(path)`
- `aram.log(...)` and `print(...)`

`tap` advances `hold_frames + 1` frames. The extra frame delivers the release
edge to runtimes that consume one queued input event per frame. Coordinates
use the framebuffer's native, zero-based coordinate system.

Standard control names are `up`, `down`, `left`, `right`, `ok`, `soft-left`,
`soft-right`, `menu`, `back`, `send`, `end`, `star`, `hash`, and `num0`
through `num9`. Runtime-specific aliases accepted by `application` also work.

Every action and `print` call produces one JSON object on stdout. Diagnostics
and Lua failures go to stderr, so an agent can parse successful runs without
scraping prose.

## NDJSON control protocol

For generated or interactive command sequences, `serve` accepts one JSON
object per input line and emits exactly one response line:

```powershell
$commands = @(
  '{"id":1,"command":"start"}'
  '{"id":2,"command":"step","count":30}'
  '{"id":3,"command":"tap","control":"ok","hold_frames":2}'
  '{"id":4,"command":"screenshot","path":"build/debug/after-ok.png"}'
  '{"id":5,"command":"pixel","x":120,"y":160}'
  '{"id":6,"command":"quit"}'
)
$commands | .\build\aram-debug.exe serve .\games\title.kwx
```

Supported commands are `start`, `step`, `key_down`, `key_up`, `tap`, `reset`,
`stop`, `status`, `cpu`, `runtime`, `screen`, `pixel`, `screenshot`, `save_state`,
`load_state`, and `quit`. A failed command returns `"ok":false` but leaves the
protocol session alive. `status` includes registers and the most recent CPU
stop result. Its `runtime` field includes image geometry, WIPI calls, presents,
last/unimplemented selectors, API coverage, and EADS lifecycle events when the
selected machine exposes those public diagnostics.

KTF save-state support is still limited by the underlying runtime:
`save_state`/`load_state` report the runtime error in-band instead of hiding it.

## Screen identity

Screen reports contain geometry, visible/non-black pixel counts, and
`rgba_sha256`. The hash covers little-endian width and height followed by
row-major, normalized RGBA bytes. It is independent of PNG encoder details and
is suitable for deterministic regression assertions. PNG files remain
available for visual inspection with an image viewer or an agent image tool.

Common flags let the caller select the profile, run budget, framebuffer size,
virtual frame duration, and whole-command timeout:

```text
--profile ID
--run-budget N
--width N --height N
--frame-duration 16ms
--timeout 2m
```

## Planned GDB integration

A native ARM/Thumb GDB remote stub is planned but not implemented yet. The
non-overlapping CPU-wrapper architecture, RSP packet scope, monitor commands,
implementation order, and acceptance tests are specified in
[`gdb-stub-design.md`](gdb-stub-design.md).

## SKVM debugging

SK Telecom SK-VM packages use a separate Java-bytecode runtime and debugger
entry point so they do not overlap WIPI implementation work. Package
inspection, lifecycle boot, instruction tracing, numeric key injection, PNG
capture, library APIs, private-corpus regression, and current limitations are
documented in [`skvm-runtime.md`](skvm-runtime.md).
