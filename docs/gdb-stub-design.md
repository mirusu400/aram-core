# GDB remote stub design

> Status: design and implementation guide only. The GDB stub described here is
> not implemented yet. The existing Lua and NDJSON debugger remains the usable
> interface documented in [`runtime-debugging.md`](runtime-debugging.md).

## Goal

Expose the native ARM/Thumb guest observed by `application.Machine` through the
GDB Remote Serial Protocol (RSP), while preserving the following boundaries:

- no GDB transport or protocol code in `application`, `loader`, or `cpu`;
- no changes to KTF, Raptor, EADS, or WIPI runtime dispatch;
- no mandatory CGO or native debugger library;
- no per-instruction overhead outside an explicitly enabled debug session;
- Lua/NDJSON remains responsible for handset input, framebuffer inspection,
  and WIPI/Java diagnostics.

GDB covers native registers, memory, breakpoints, continue, interrupt, and
single-step. It does not make host-modeled KTF Java objects into Java-level GDB
frames.

## Non-overlapping architecture

```text
GDB client
    |
    | RSP over loopback TCP
    v
debugkit/gdbstub.Server
    |
    +-- run control ------> debugkit Session ------> core.Machine
    |
    +-- CPU inspection --> debugkit/targetcpu.Backend
                                  |
                                  v
                          ordinary cpu.Backend
```

The new code should be limited to:

```text
debugkit/gdbstub/       RSP packet codec, server, and target contract
debugkit/targetcpu/     opt-in cpu.Backend decorator
cmd/aram-debug/         additive `gdb` subcommand and wiring
docs/                   protocol and operator documentation
```

`application.Factory.NewCPU` is the injection point. The CLI creates the
ordinary portable interpreter, wraps it with `targetcpu.Backend`, and passes
the wrapper to the existing factory. This avoids exposing the private
`application.Machine.cpu` field or adding memory mutation to `core.Machine`.

No edit to `application/ktf_runtime.go`, `application/machine.go`,
`cpu/backend.go`, or a loader should be necessary for the initial
implementation.

## Proposed debug target contract

The RSP server should depend on a small interface rather than a concrete
application machine:

```go
type Target interface {
    Registers(context.Context) ([]uint32, error)
    ReadRegister(context.Context, uint32) (uint32, error)
    WriteRegister(context.Context, uint32, uint32) error
    ReadMemory(context.Context, uint32, []byte) error
    WriteMemory(context.Context, uint32, []byte) error

    Continue(context.Context, *uint32) (Stop, error)
    Step(context.Context, *uint32) (Stop, error)
    Interrupt() error

    AddBreakpoint(uint32) error
    RemoveBreakpoint(uint32) error
}
```

The exact interface may be split into CPU inspection and machine-driving
interfaces during implementation. RSP parsing must not depend on
`application` types.

All mutation is legal only while the guest is stopped. Exactly one component
owns execution at a time:

- GDB owns run control during `continue` or `step`;
- Lua/NDJSON may inspect side-band status only while GDB reports the target
  stopped;
- disconnect or detach cancels active execution and removes session
  breakpoints.

## CPU decorator behavior

`targetcpu.Backend` implements the existing `cpu.Backend` interface and
delegates ordinary operations to its wrapped backend. In normal mode it should
add only a cheap disabled check around `Run`.

When GDB debugging is enabled, it additionally tracks:

- normalized execution breakpoints;
- a pending single-step request;
- asynchronous interrupt state;
- the most recent GDB-visible stop;
- whether guest execution is currently inside `Run`.

### Continue

The machine driver repeatedly calls `StepFrame`. Ordinary
`cpu.StopBudget` results are scheduling boundaries, not GDB stops. Continue
ends only for:

- a requested GDB breakpoint;
- single-step completion;
- Ctrl-C/interrupt;
- guest fault;
- normal guest exit;
- context cancellation or connection loss.

The decorator may run the wrapped backend in bounded chunks while debugging so
it can check breakpoints and interrupts. It must not force one-instruction
chunks during ordinary non-debug execution.

### Single-step

Single-step executes one native guest ARM/Thumb instruction and returns a
GDB-visible `SIGTRAP`. Host trampoline dispatch may occur immediately after
that stop when execution resumes.

KTF task scheduling and native/host transitions make this the highest-risk
part of the implementation. Tests must prove that stepping does not lose the
current KTF task context or misclassify an ordinary host trampoline.

### Breakpoints

Prefer logical execution breakpoints in the decorator. Do not initially patch
guest text with ARM/Thumb `BKPT` instructions:

- existing runtimes already use breakpoint stops for host trampolines;
- guest code may be mapped read/execute;
- patched bytes complicate save state and detach;
- a title may contain legitimate breakpoint instructions.

Normalize Thumb code addresses with `address &^ 1` for comparison, while
preserving execution mode through CPSR T (`cpu.StatusThumb`). GDB-visible PC
and breakpoint reporting must use one documented convention consistently.

## Initial RSP surface

The server should support GDB negotiation without pretending unsupported
features exist.

### Connection and discovery

- `qSupported`
- `QStartNoAckMode`
- `?`
- `qAttached`
- `qXfer:features:read:target.xml`
- `qfThreadInfo` / `qsThreadInfo`
- `H`
- `vCont?`
- empty replies for unsupported optional queries

The initial target exposes one synthetic thread, ID `1`. KTF host tasks must
not be advertised as GDB threads until their saved native contexts have a
stable public model.

### Registers and memory

- `g` / `G`
- `p` / `P`
- `m` / `M`

ARM register order:

```text
r0..r12, sp, lr, pc, cpsr
```

Each register is a 32-bit little-endian value. `target.xml` should declare
`arm` and the `org.gnu.gdb.arm.core` feature. Do not advertise floating-point
or vector registers that the backend does not model.

### Run control

- `c` / `s`, with optional address
- `vCont;c` / `vCont;s`
- raw Ctrl-C byte (`0x03`)
- `Z0` / `z0` for software execution breakpoints implemented logically
- `D` for detach
- `k` for kill/stop

Watchpoints (`Z2` through `Z4`) and hardware breakpoint variants should return
an empty unsupported response in the first version.

### Stop replies

Suggested mappings:

| Emulator condition | RSP reply |
|---|---|
| logical breakpoint or completed step | `T05` (`SIGTRAP`) |
| user interrupt | `T02` (`SIGINT`) |
| invalid guest memory access | `T0b` (`SIGSEGV`) |
| unsupported instruction | `T04` (`SIGILL`) |
| clean guest exit | `W00` |

Include `thread:1;`, `pc`, and `reason` fields where GDB accepts them. Preserve
the underlying Go error in the debugger's structured log even when RSP can
only represent a signal.

## Monitor commands

RSP `qRcmd` can bridge the existing debugger features into the same GDB
session. Proposed commands:

```text
monitor status
monitor runtime
monitor screen
monitor screenshot build/debug/gdb-frame.png
monitor key down ok
monitor key up ok
monitor key tap ok 2
monitor step-frame 10
```

Responses use GDB console-output `O` packets. Paths must follow the same local
artifact policy as the Lua/NDJSON debugger. Run-control commands are rejected
unless the target is stopped.

## CLI shape

Proposed command:

```powershell
.\build\aram-debug.exe gdb `
  --listen 127.0.0.1:2159 `
  --wait `
  .\games\title.zip
```

Manual client session:

```gdb
(gdb) set architecture arm
(gdb) target remote 127.0.0.1:2159
(gdb) info registers
(gdb) x/16wx $sp
(gdb) break *0x00103b34
(gdb) continue
(gdb) stepi
(gdb) monitor screenshot build/debug/gdb-frame.png
```

Bind to `127.0.0.1` by default. RSP has no authentication or encryption; a
non-loopback listener must require an explicit unsafe/remote flag and print a
warning to stderr.

## Implementation order

1. Implement and fuzz the RSP packet codec independently of the emulator.
2. Define the generic target contract and a deterministic fake target.
3. Implement read-only attach, register, memory, and target XML support.
4. Implement `targetcpu.Backend` interrupt and logical breakpoints.
5. Add continue and single-step through an exclusive machine driver.
6. Add `qRcmd` monitor integration with the existing debug session.
7. Add the `aram-debug gdb` CLI and loopback-only defaults.
8. Validate ARM and Thumb synthetic programs.
9. Run a private KTF corpus smoke test without committing input or screenshots.

## Verification

Unit and fuzz coverage:

- checksum, escaping, acknowledgements, no-ack mode, partial packets;
- malformed hex, oversized packets, cancellation, and disconnect;
- exact ARM register encoding order and little-endian values;
- memory boundary and permission failures;
- Thumb address normalization;
- breakpoint add/remove/idempotence;
- interrupt during a long `Run`;
- detach cleanup and restoration of normal backend performance;
- no execution/mutation while another controller owns the target.

CI integration must use a small in-process RSP test client so ordinary tests do
not require GDB to be installed. An optional manual or private job may run
`gdb-multiarch` or `arm-none-eabi-gdb`.

Private KTF acceptance:

1. load a user-authorized package from `ARAM_TEST_DATA`;
2. attach before guest execution;
3. read registers and mapped memory;
4. stop at a normalized Thumb breakpoint;
5. single-step without corrupting the current KTF task;
6. issue a handset key through `monitor`;
7. capture and visually inspect the framebuffer;
8. detach and continue through the ordinary runtime path.

No package bytes, extracted assets, absolute private paths, or screenshots are
committed.

## Completion criteria

The stub is complete only when:

- stock GDB connects without negotiation errors;
- register and memory reads match direct backend reads;
- register and writable-memory changes round-trip;
- continue, Ctrl-C, step, breakpoint, detach, and reconnect are deterministic;
- ARM/Thumb PC and CPSR behavior is covered by tests;
- a real KTF package survives attach, breakpoint, input, screenshot, detach,
  and continued execution;
- non-debug execution shows no material per-instruction overhead;
- `go test ./...`, `go vet ./...`, and Android/arm64 build still pass.
