# SKVM portable runtime and debugging

`aram-core/skvm` is a headless, pure-Go interpreter for SK Telecom SK-VM
packages. It is intentionally separate from the WIPI-C and KTF runtime work:

- `loader/skvm` owns the SKT `.msd`/`.mod`/`.wmr`/wrapped-JAR container;
- `skvm` owns JVM class parsing, bytecode execution, heap objects, arrays,
  Java/ME host methods, SKT extensions, input, and an RGBA framebuffer;
- `cmd/aram-skvm-inspect` reports package/class/API structure without running
  bytecode;
- `cmd/aram-skvm-run` boots and debugs one package without a GUI.

No package in this implementation imports `application`, `wipi`, `loader/ktf`,
or `loader/raptor`. That boundary lets WIPI work proceed independently.

## Current compatibility boundary

The private `dubigame-202403/SKT` corpus currently contains 20 outer packages.
All 20 pass the following smoke path:

1. validate the outer ZIP and basename-matched SKVM quartet;
2. remove the observed 32-byte SKT JAR wrapper;
3. parse every JVM class file;
4. initialize classes and construct the descriptor main class;
5. invoke either `startApp()V` or the SKTP 1.1
   `startApp([Ljava/lang/String;)V` variant;
6. invoke `showNotify` and `paint` when a MIDP `Displayable` is current.

This is a lifecycle smoke result, not a gameplay-compatibility claim. Some
titles need a real cooperative Java thread scheduler before their game loop
advances beyond the initial state.

Observed packages use JVM class version 45.3 and depend on a mixture of:

- `java.lang`, `java.io`, and `java.util`;
- `javax.microedition.lcdui`, RMS, MIDlet, and IO APIs;
- `com.skt.m` carrier APIs;
- XCE and KWIS compatibility APIs shipped on some SKT devices.

The implementation basis is the authorized local corpus plus primary public
evidence that SKVM is an interpreted Java/ME runtime:

- [JCP SK Telecom platform summary](https://jcp.org/aboutJava/communityprocess/elections/2011-nominees-me.html)
- [Korean VM patent background describing SK-VM](https://patents.google.com/patent/KR20070035211A/en)
- [Oracle JVM specification](https://docs.oracle.com/javase/specs/)

## Inspect a package

```powershell
go run ./cmd/aram-skvm-inspect -- "C:\path\game.zip"
```

The JSON report includes the descriptor, wrapper size, class versions,
methods, resources, and deduplicated field/method references. Inspection does
not execute guest code.

## Boot, trace, inject keys, and capture the screen

```powershell
go run ./cmd/aram-skvm-run `
  -instructions 2000000 `
  -trace 64 `
  -keys "press:53,release:53,press:50,release:50" `
  -screenshot ".\skvm-frame.png" `
  "C:\path\game.zip"
```

Key events accept signed decimal or Go-style integer literals. `53` is the
usual Java ME key code for the `5` key. The runtime calls the current
displayable's `keyPressed(I)V` or `keyReleased(I)V`, then repaints it.

The command emits JSON containing:

- the main class and current object references;
- the executed-bytecode count;
- an RGBA framebuffer SHA-256;
- the number of non-transparent pixels;
- a trailing instruction trace with resolved constant-pool targets;
- any structured loader, class, opcode, host-method, or instruction-limit
  error.

The PNG writer is optional. It never requires a window, display server, or
native image library.

## Library use

```go
pkg, err := skloader.Inspect(packageBytes)
if err != nil {
    return err
}

classes := make(map[string][]byte, len(pkg.Classes))
for name, class := range pkg.Classes {
    classes[name] = class.Data
}

machine, err := skvm.New(classes)
if err != nil {
    return err
}
machine.InstructionLimit = 2_000_000
machine.SetResources(pkg.Resources)
machine.SetProperties(pkg.Descriptor.Raw)

_, err = machine.Start(ctx, pkg.Descriptor.MainClass)
if err != nil {
    return err
}
if machine.CurrentDisplay() != 0 {
    _ = machine.ShowCurrent(ctx)
    _ = machine.PaintCurrent(ctx)
    _ = machine.KeyEvent(ctx, 53, true)
    _ = machine.KeyEvent(ctx, 53, false)
}
rgba := machine.FrameRGBA()
```

Use `SetTraceHook` for instruction-level diagnostics. A hook receives the
class, method, descriptor, PC, opcode, call depth, and a resolved
constant-pool target when applicable. Use `RegisterNative` to override or add
a title/device API without modifying the interpreter.

## Private corpus regression

```powershell
$env:ARAM_TEST_DATA = (Resolve-Path "..\aram-test").Path
go test ./skvm -run '^TestReferenceSKVMLifecycleSmoke$' -count=1 -v
Remove-Item Env:ARAM_TEST_DATA
```

The test reads private packages only from the environment-selected sibling
repository. It does not copy firmware, games, extracted classes, or resources
into `aram-core`.

## Implemented VM surface

The bytecode engine currently covers the instruction families exercised by
the corpus, including:

- constants, local loads/stores, fields, static fields, and arrays;
- integer/long arithmetic, shifts, comparisons, and conversions;
- branches, `tableswitch`, `lookupswitch`, legacy `jsr`/`ret`, and wide
  operands;
- object and multidimensional-array allocation;
- virtual, special, static, and interface invocation;
- exception tables, `athrow`, checks/casts, and monitor instructions;
- category-1/category-2 stack manipulation used by the old Java compiler.

The host layer includes deterministic subsets of Java core classes, MIDP
display/graphics/image/font, RMS, SKT audio/device/vibration/Graphics2D, XCE,
and KWIS APIs. RMS and file data are currently in-memory runtime state.

## Known limitations

- `Thread.start`, timers, and `Display.callSerially` do not yet preserve and
  interleave Java continuations. They are deterministic compatibility stubs.
- Audio, vibration, backlight, browser launch, and networking have no external
  side effects.
- BMP, PNG, GIF, JPEG, and SKVM LBMP images decode through the bounded shared
  asset service. Other proprietary LBM and MMF assets remain resources only.
- Text drawing uses a deterministic placeholder glyph, not a device font.
- RMS/XCE/KWIS storage is not yet serialized as a save-state component.
- Unknown device properties return a deterministic compatibility fallback;
  device profiles should eventually replace that fallback.
- The interpreter is not yet wired into the shared `application.Factory`.
  That integration should happen only after the WIPI owner and SKVM owner
  agree on format-probing order.
- The existing GDB design targets native ARM runtimes. SKVM instruction
  debugging currently uses `SetTraceHook`; a Java-aware remote debugger would
  be a separate protocol adapter.

## Safety rules

SKVM packages are untrusted input. The loader rejects unsafe ZIP paths,
case-colliding members, symlinks, ambiguous application quartets, oversized
members, expanded-size overflow, malformed wrapper offsets, invalid classes,
and missing main classes. Execution is bounded by an instruction limit and a
caller context.
