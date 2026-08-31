# Development Unicorn comparison backend

This nested Go module adapts a locally installed Unicorn 2.x shared library to
ARAM's application-mode `cpu.Backend`. It exists only for deterministic
differential testing against `cpu/interpreter`; the root module does not import
it, its dependencies do not enter the root `go.mod`, and it is never selected
by product builds.

The adapter uses a small cgo shim to load the library at runtime without a
build-time Unicorn dependency. Set `ARAM_UNICORN_LIBRARY` to an absolute
`.dll`, `.so`, or `.dylib` path, or place a conventional Unicorn library name
on the platform loader's search path. A missing or incompatible library, a
disabled cgo toolchain, or an unsupported platform returns
`unicornbackend.ErrUnavailable`; tests report a skip instead of making the
default build fail.

On Windows, calls are serialized through a native worker thread. Unicorn 2.1
uses handled access violations to commit its translation buffer lazily, while
Go owns a process exception handler for Go-managed threads; the worker lets
Unicorn receive and handle those native demand-paging faults normally.

```powershell
cd dev/unicornbackend
$env:CGO_ENABLED = "1"
$env:ARAM_UNICORN_LIBRARY = "C:\path\to\unicorn.dll"
go test ./...
```

The backend deliberately runs at one guest instruction per Unicorn call. It is
a semantic comparison tool, not a production-speed integration. It supports
private, page-aligned application mappings and ARM/Thumb integer execution; it
does not attach a whole-system bus, expose MMIO callbacks, or claim firmware
support.

See [UNICORN-LICENSE-NOTICE.md](UNICORN-LICENSE-NOTICE.md) before installing or
redistributing the native library.
