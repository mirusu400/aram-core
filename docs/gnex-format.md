# SKT GNEX (SinjiSoft GVM) compatibility

GNEX is SK Telecom's distribution family for SinjiSoft GVM applications. It is
not SK-VM Java and it is not WIPI-C. A distribution normally contains a binary
`.mod` or `.inf` descriptor and a basename-matched `.sgs` program image.

This document describes the current implementation. Historical opcode guesses
and superseded “not implemented” notes have been removed; the tests and the
qualified corpus are the source of truth for supported behavior.

## Compatibility snapshot

The repository has two execution paths:

| SGS generation | Runtime | Qualified local corpus | Product status |
| --- | --- | ---: | --- |
| versions 1 and 2 | [`gvm`](../gvm) | nine exact hashes | operational display, input, timer, and persistent-data paths |
| version 4 | [`gnex32`](../gnex32) | one exact hash, `바이러스무삭제` | operational display, input, timers, services, audio, save state, and restart persistence |

Product routing is deliberately hash-qualified. A structurally valid but
unregistered version-4 image is inspected and preflighted, then reported as an
unqualified compatibility target rather than executed under the Virus device
profile. The registry is in
[`application/gnex32_machine.go`](../application/gnex32_machine.go).

The qualified Virus package SHA-256 is
`bf9cd39b1ae14ba2a5f005d66fde5390e53cb400bd36940d4b09dcbfe9f03883`.
It is the only version-4 layout in the available ten-package corpus. No second
layout is accepted without structural corpus evidence.

## Archive and metadata decoding

[`loader/gnex`](../loader/gnex) recognizes:

- ZIP distributions with one basename-matched `.sgs` plus `.mod` or `.inf`;
- a standalone SGS at offset zero; and
- the observed 32-byte-prefixed version-4 SGS.

Recognition is bounded by per-member, aggregate expansion, entry-count, path,
duplicate-name, and exact-size checks. A file extension alone is not identity.
Multiple application candidates are rejected.

The stable descriptor fields are exposed by
[`metadata.go`](../loader/gnex/metadata.go):

- `DecodeMODDescriptor` finds the self-describing timestamp, MIME type,
  payload type, and version strings. Observed records place this block at more
  than one offset.
- `DecodeINFDescriptor` validates the marker, 16-byte digest, revision, flags,
  and ten-digit application key.
- `DecodeRESIcon` validates the little-endian `0xDEADFACE` launcher-icon
  container and returns its detached raster payload.
- `DecodeWMRBundle` validates `0xFACEDEAD`, the producer's size convention, and
  bounded kind/size/data records. The Virus bundle has a kind-0 BMP and a
  kind-1 ABM resource.

`.res` and `.wmr` are launcher/distribution resources, not SGS media-table
entries, so the game runtime does not inject them into media memory.

Legacy header bytes 6–7 are a decimal application/provider identifier, not a
title checksum. They are exposed as `Header.ApplicationID`. The deprecated
`TitleChecksum` field is retained as a source-compatible alias and is tested to
match. Known IDs include 34064 for `.hack//sign`, 34043 for `놈`, and 34298 for
`마시마로격투 맞고`.

## Version-4 image layout

[`DecodeGNEX32Image`](../loader/gnex/gnex32.go) accepts the one qualified
layout:

- a 32-byte prefix whose first word is 32 and whose remaining bytes are zero;
- a version-4 header beginning at prefix offset 32;
- code beginning at header-relative offset `0x80`;
- ordered symbol-descriptor, symbol-data, media-descriptor, and media-data
  spans; and
- an exact total-length field and exact descriptor-data coverage.

Code addresses are stored as a high 16-bit word followed by a low 16-bit word,
with the combined address relative to the 32-byte prefix. The decoded runtime
entry adds that prefix; code offsets exposed by the VM are relative to the code
span.

The eleven callback slots are represented by `GNEX32Callbacks` in this order:

1. `EVENT_START`
2. `EVENT_END`
3. `EVENT_TIMEOUT`
4. `EVENT_KEYPRESS`
5. `EVENT_RECEIVE`
6. `EVENT_RSRECEIVE`
7. `EVENT_SMSRECEIVE`
8. `EVENT_RESULT`
9. `EVENT_NETWORK`
10. `EVENT_RSEVENT`
11. an unnamed extension slot

A zero address means that the callback is absent. The unnamed slot remains
diagnostic-only because the available SDK event reference does not name it.

### Symbol storage

The Virus image has 479 symbols:

| Flags | Count | Runtime contract |
| ---: | ---: | --- |
| `0x1` | 327 | writable per-run storage; all qualified descriptors are zero-filled |
| `0x100` | 152 | writable per-run storage initialized from descriptor data; 146 are nonzero |

Both classes use detached storage, so guest writes never mutate the decoded
image. The `0x100` class is not read-only: the title writes such symbols during
normal input handling. Unknown flags and non-word-aligned initializer shapes
are rejected. See
[`gnex32_memory.go`](../loader/gnex/gnex32_memory.go) and its
[`tests`](../loader/gnex/gnex32_memory_test.go).

### Media storage

The Virus image has 2,233 media records:

| Flags | Count | Class | Ordinary byte write/resize |
| ---: | ---: | --- | --- |
| `0` | 538 | read-only data, commonly terminated text | no |
| `0x1` | 89 | writable data | yes |
| `0x100` | 1,575 | read-only asset | no |
| `0x101` | 1 | writable asset | yes |
| `0x200` | 30 | read-only audio | no |

The low bit is the ordinary writable-storage bit for the two supported mutable
classes. Palette mutation is a separate SDK operation: it may create a per-run
palette override for a read-only image without making the underlying resource
generally writable. Runtime copies, size limits, atomic failure, snapshot, and
restore behavior are implemented in
[`gnex32_media.go`](../loader/gnex/gnex32_media.go).

## VM and service contracts

The 16-bit instruction VM is implemented in [`gnex32/vm.go`](../gnex32/vm.go).
Instruction widths and effects are backed by SDK assembler SAL/SGS output, the
qualified image, and boundary tests. Static control-flow analysis starts at the
main entry and all named callbacks, follows direct branches/calls, and reports
unknown opcodes and unregistered service IDs with code offsets.

Preflight constructs symbol/media storage and requires zero reachable unknown
opcodes and zero unsupported services before product execution. Multiword
instructions reject truncation atomically; stack producers/consumers are tested
for overflow and underflow; invalid symbols, elements, lvalues, calls, branches,
and division by zero do not partially mutate VM state. Arithmetic is 32-bit
wrapping, signed comparisons use `int32`, logical right shift is unsigned, and
shift counts of 32 or more yield zero.

Entering an event clears value, call, mark, and lvalue-reference stacks before
installing `swData..swData3`, so a callback cannot inherit a suspended main
expression.

The service registry in [`gnex32/services.go`](../gnex32/services.go) defines
the name and successful pop/push count for all 80 implemented services. Runtime
dispatch verifies each successful result against that contract before the VM
commits its stack change. Service errors preserve the caller's stack. The
registry includes display, text, string/byte-array, math, random, timer, NV,
audio, vibration, browser, network, and system-information families.

`RandSeed`/`Rand` use the legacy GVM/MS-compatible sequence
`state = state*214013 + 2531011`, then `(state>>16)&0x7fff`. The upper range is
exclusive, reversed bounds are normalized, and equal bounds do not consume a
draw.

## Runtime behavior

### Time, timers, and events

The qualified profile runs at a deterministic 60 Hz guest clock. Date/time
queries use its fixed timezone and do not consult or advance host time. Timer
deadlines round up to a frame, simultaneous slots dispatch 0→1→2, one-shot and
repeat modes follow the SDK values, and a failed callback stops the frame before
later slots or clock advancement.

Natural sound completion dispatches `EVENT_RESULT` with `swData=23`. Network
completion uses the shared event/coordinator path. Stop, reset, and close deliver
`EVENT_END` when applicable. A host injects network, serial, and SMS input
through the optional `core.HostEventReceiver` contract. The runtime copies a
connected packet of at most 1024 bytes into compiler symbol 14 (`swRcvBuf`) and
dispatches `EVENT_RECEIVE` with the byte count in `swData`. Serial and SMS
events dispatch the four host-supplied `swData..swData3` words. Missing
callbacks drop these external events rather than inventing guest buffers or
metadata.

### Input

Mobile C key values 1–22 cover numeric, `*`, `#`, clear, send, end, directions,
OK, and soft keys. Every release delivers `SWAP_KEY_RELEASE` (`0xff`). Long-key
repeat begins after 500 ms and repeats every 50 ms on guest time. A physical
press may start a key tone; synthetic repeats do not. Pause freezes input repeat,
timer, and audio progress together.

### Graphics and text

The renderer supports mono kinds `0x02`/`0x05`, selector-indexed kinds
`0x06`/`0x07`, and RGB-indexed kinds `0x09`/`0x0a`/`0x0b`. Qualified-corpus
counts are 144, 99, 4, 3, 61, 237, and 354 respectively. Exact payload bounds,
palette length, packed-pixel length, index range, and the observed optional zero
pad are validated.

Palette selectors and RGB triplets use RGB order. Mode 1 makes palette index 0
transparent; mode 0 draws it. Mono Color0/Color1 follow the same index order.
These rules, all seven image kinds, copy direction, alpha modes, 1×/2× zoom,
mirror, rotation, hotspot placement, clipping, and gamma are covered by pixel
tests and qualified reference frames.

`FlushPartial` applies an inclusive clipped rectangle to the last presentation.
Line/rectangle/rounded-rectangle operations use inclusive LCD coordinates and
treat reversed bounds as empty. EUC-KR decoding rejects replacement characters.
The six font sizes have fixed handset metrics; glyph baseline/advance, alignment,
and solid-background height are covered by raster tests. Invalid font sizes fail
atomically.

### Audio and handset controls

All 30 flag-`0x200` resources decode through the MMMD/MIDI/WAV pipeline. Product
tests reach multiple menu/BGM/SFX resources, observe non-silent PCM, validate
guest timestamps, replacement, explicit stop, and natural completion. Invalid
or unsupported resources return the shared media error without installing a
partial clip.

Key tones use standard DTMF for numeric/`*`/`#`, a deterministic 1 kHz fallback
for other controls, and a 60 ms duration. They mix with ordinary sound on the
shared media timeline. `SetKeyTone(0)` stops only the key tone.

Backlight modes preserve `S_OFF=0`, `S_ON=1`, and `S_MMI=2`; the headless Virus
profile resolves MMI to on. Gamma supports levels 0–6 with neutral level 3.

### State and persistence

GNEX32 volatile save states are versioned, checksummed, bounded, and bound to the
exact source SHA-256. They include VM stacks/references, symbols, media and
palette overrides, all LCD buffers, timers, RNG, clock, UserNV, handset state,
network state, active audio decoders, and queued PCM. Restore validates a new
runtime completely before replacing the live one.

Restart persistence is distinct from a volatile save state. GNEX32 exports its
16-word UserNV area; GVM versions 1/2 export their fixed persistent byte record.
Both implement the optional `core.SaveDataMachine` contract and reject imports
during unsafe lifecycle states. Reset preserves persistent data while recreating
volatile execution state.

Audio and video share a guest timestamp and discontinuity generation. Load and
reset publish a new generation; stop and close discard buffered output. Pause
preserves queued PCM and freezes the guest clock.

## Shared GVM/GNEX host boundary

The two guest ABIs remain separate, but their product-facing contracts are now
explicit:

| Area | Shared boundary | ABI-specific part |
| --- | --- | --- |
| palette | `runtime.SKTGammaColor`, `SKTGammaRGBA`, `SKTPackedColor` | GVM packed selectors versus GNEX32 selector/RGB image palettes |
| input | `core.InputEvent`, common normalized control names | GVM press callback codes versus GNEX32 press/release values and repeats |
| persistence | optional `core.SaveDataMachine` | GVM byte record versus GNEX32 16-word UserNV envelope |
| audio | `core.Machine.DrainAudio` / `core.AudioChunk` | GNEX32 currently produces timestamped PCM; the qualified GVM path returns the valid zero chunk because no decoded playback service is exposed by that path |

This separation prevents one guest ABI's numeric values or serialization format
from leaking into the other while keeping the frontend lifecycle stable.

## Verification

The optional reference tests locate packages recursively through
`ARAM_TEST_DATA`; they do not embed commercial game data in the repository.
The exact Virus hash selects the version-4 assertions, so unrelated ZIP files
are skipped or handled by their own loaders.

Current evidence includes:

- all 30 sound resources decoded independently;
- all seven rendered image kinds scanned and pixel-tested;
- exact symbol/media class counts and runtime mutability checks;
- static reachable analysis with zero unknown opcodes and zero unsupported
  services;
- automated menu/help and gameplay traces with service argument samples; and
- a main-menu branch probe proving six distinct entries, six-step wraparound,
  and at least five distinct result-frame branches; and
- a long gameplay probe covering 3,745,463 instructions, 69 opcodes, 47 of 73
  statically reachable service IDs, 122 distinct frame hashes, and 6,116,608
  drained PCM samples without a VM or service fault.

The remaining 26 statically reachable but unvisited service IDs are browser,
vibration, network, drawing, string/array, math, NV-write, and system-information
branches. Each has direct unit/contract coverage; the long trace is not presented
as proof that every game branch or an entire commercial playthrough was reached.

Relevant suites are
[`application/gnex32_reference_test.go`](../application/gnex32_reference_test.go),
[`application/gnex32_audio_reference_test.go`](../application/gnex32_audio_reference_test.go),
[`loader/gnex/reference_test.go`](../loader/gnex/reference_test.go), and the
package-local tests under [`gnex32`](../gnex32).

Run the complete checks with:

```powershell
$env:ARAM_TEST_DATA = 'path-to-a-directory-containing-GNEX-zips'
go test ./...
go vet ./...
```

Without `ARAM_TEST_DATA`, commercial-corpus tests skip while all synthetic,
fuzz-seed, stack-contract, lifecycle, rendering, and codec tests still run.
