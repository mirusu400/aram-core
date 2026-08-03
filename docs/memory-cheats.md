# In-game memory cheats

The `cheat` package provides host-side memory search, guarded writes, reusable
title-keyed codes, and per-frame value freezing without extending the public
`core.Machine` interface. `application.AttachCheats` supplies the application
memory map and returns a `core.Machine`-compatible wrapper.

Always use the returned wrapper for machine operations after attaching cheats.
It serializes guest execution with searches and writes. Calling the original
machine directly after attachment bypasses that guarantee.

```go
wrapped, err := application.AttachCheats(machine, cheat.Options{})
if err != nil {
    return err
}
engine := wrapped.Cheats()
```

The application adapter supplies the loaded application's SHA-256 and default
regions for writable image data, the WIPI heap, and manual access to text and
stack memory. Executable memory and the stack are not searched unless selected
explicitly.

Code sections are writable so a hash-keyed patch can retarget a branch, which
is how a title-specific check is neutralized. They stay unscannable, so an
unknown-value scan never walks executable bytes. The portable interpreter
fetches straight from mapped memory and keeps no decoded-instruction cache, so
a code patch takes effect on the next fetch.

## Find an address

Start with a known value, change it in game, and filter the previous results:

```go
initial := cheat.U32(100)
matches, err := engine.Scan(cheat.ScanRequest{
    Type:       cheat.TypeUint32,
    Comparison: cheat.CompareEqual,
    Value:      &initial,
})
if err != nil {
    return err
}

// Advance the wrapped machine until the visible value decreases.
matches, err = engine.NextScan(cheat.NextScanRequest{
    Comparison: cheat.CompareDecreased,
})
```

`CompareUnknown` seeds a scan without a known value. Unknown scans can produce
large result sets and return `ErrTooManyResults`; `MaxResults` and
`MaxScanBytes` in `cheat.Options` bound their host memory and read cost.

Supported scalar representations are signed and unsigned 8-, 16-, 32-, and
64-bit integers plus 32- and 64-bit floating point. Little-endian values are
the default.

## Write or freeze a value

An interactive write can include expected bytes or an expected typed value:

```go
expected := cheat.U32(3)
if err := engine.Write(address, cheat.U32(99), &expected); err != nil {
    return err
}
```

Reusable codes are bound to the loaded application SHA-256. If `Expected` is
omitted, the engine captures the current bytes when the code is added and
checks them again when enabling it.

```go
value, _ := cheat.U32(99).Encode(cheat.EndianLittle)
_, err = engine.AddCode(cheat.Code{
    ID:               "lives",
    Description:      "Keep lives at 99",
    Address:          address,
    Value:            value,
    Freeze:           true,
    RestoreOnDisable: true,
})
if err != nil {
    return err
}
if err := engine.EnableCode("lives"); err != nil {
    return err
}
```

Freeze codes are reapplied after every successful `Start`, `Resume`, and
`StepFrame`. All enabled codes are reapplied after `Reset` and `LoadState`.
Cheat definitions remain host-side state and are not embedded in guest save
states.
