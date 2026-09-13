# SKT GNEX (SinjiSoft GVM) titles

"GNEX" is the corpus/marketing label for a distribution of SK Telecom titles
that predate WIPI adoption and run on **SinjiSoft's ("신지소프트") GVM**
runtime rather than SK-VM (Java, `loader/skvm`) or WIPI-C (`loader/raptor`,
`loader/ktf`). `loader/gnex` recognizes the archive shape and decodes the
payload's title header; it does not decode or execute the GVM bytecode body,
which has not been reverse engineered far enough to run.

## What a GNEX archive looks like

A paired GNEX title ships as a ZIP containing a basename-matched pair:

- an `.SGS`/`.sgs` payload ("Sinji Game Script" - confirmed from the string
  `"Sinji Game Script (*.SGS)|*.sgs|..."` in SinjiSoft's PC-side player,
  `cr32256_Magic.exe`, which also self-identifies as
  `"GVM 2x Emulator (128KB)"` / `"GVM1X "` / `"EMULATOR GVM2X"`), and
- a small binary descriptor, in one of two observed shapes:
  - a newer `.mod` installer record (448 or 512 bytes depending on title),
    containing length-prefixed fields including a timestamp, the MIME type
    `application/x-gnex-sgs`, the magic string `SGS`, a version string
    (`1.0.0` in every sample), a `IP:;PORT:0;ID:<hex>` connection line, and a
    handful of `http://wipi.nate.com:9100/...` download-manager URLs;
  - an older `.inf` record (2004-dated titles), which does not carry the MIME
    string and is otherwise undecoded here.

Some archives also carry a `.res` icon resource (magic `0xDEADFACE`, also
undecoded), a `.wmr` bundle, a `.dat`/`user.dat`/`UserNV.sys` save-state file,
and/or a bundled copy of `cr32256_Magic.exe` itself. None of these are needed
to recognize a title and `loader/gnex` does not interpret them; `Package.Files`
exposes the raw archive members for callers that want them.

Neither the `.mod` nor the `.inf` shape is required to be byte-perfect for
recognition - see "What `loader/gnex` actually validates" below for why.

The authorized legacy-platform corpus checked on 2026-09-11 also contains a
ZIP with an SGS payload and no descriptor. All nine SGS payloads in that
corpus have a recognized header at offset zero, a clean title, and a
nonempty body. One reports version 1 and eight report version 2. These are
entry counts, not a claim of nine unique or executable games. `Inspect` now
accepts standalone SGS in a ZIP and raw SGS, without manufacturing a manifest.

## The `.SGS` payload header

Reverse engineered by static analysis of `cr32256_Magic.exe` (IDA, no
dynamic tracing - x64dbg was not available in this environment) and
cross-checked by round-tripping the decoded title against all 12 corpus
titles' known display names (`loader/gnex/reference_test.go`,
`TestReferencePackages`):

| Offset | Field | Notes |
|---|---|---|
| 0 | format version | Only `1` and `2` observed, matching the emulator's `GVM1X`/`GVM2X` self-identification. |
| 1-4 | unmodeled | Varies per title; not reverse engineered. |
| 5 | constant `0x01` | Every sample. Combined with a clean title decode, this is what `ParseHeader` scans for. |
| 6-7 | title checksum (LE uint16) | Every sample falls in `0x8400`-`0x85ff`; behaves like a hash of the title bytes, but the algorithm is not confirmed, so it is exposed (`Header.TitleChecksum`) but not validated. |
| 8-9 | unmodeled | Not reverse engineered. |
| 10.. | title, cp949/EUC-KR, terminated by `0x00 0x00` | cp949 lead/trail bytes and ASCII text never produce a bare `0x00`, so this terminator is unambiguous. |

11 of the 12 corpus titles have this header at offset 0. One repack
(창세기전 외전 크로우, re-exported 2025-05-27 unlike the others' 2004-2006
timestamps) carries a 32-byte zero-padded prefix ahead of it; `ParseHeader`
scans up to `maxHeaderScan` (48 bytes) rather than assuming a fixed offset,
to tolerate that and unknown-but-similar variants.

Everything from `Header.BodyOffset` onward - the GVM bytecode, the symbol and
media (resource) tables, and in-title image data - is **not decoded**.

### What is known about the body, and why it isn't decoded yet

Static analysis of the same player found enough of the GVM runtime's shape to
describe it, but not enough to write an interpreter:

- It is a **stack-based bytecode VM** with a symbol table (the article
  ["모바일게임 변천사"](https://www.inven.co.kr/webzine/news/?news=179050)
  describes GVM's "최대 변수 256개" limit; the disassembled exception table
  matches: `Stop`, `DivideByZero`, `ModByZero`, `SymbolOverflow`,
  `MediaOverflow`, `StackOverflow`, `PCStackOverflow`, `InvalidOpcode`,
  `InvalidSymbolIndex`, `InvalidMediaIndex`, `InvalidSymbolAddr`,
  `InvalidMediaAddr`, `OutOfArray`). A symbol table entry is 8 bytes; a
  decompiled opcode handler was observed indexing it as
  `symbolTable + 8*index` and reading a value dword at `entry+4`.
- Media (image/sound) resources are addressed separately from symbols
  (`InvalidMediaIndex`/`InvalidMediaAddr` are distinct from the `Symbol`
  pair), but the media table layout was not located.
- A related but distinct format, tagged with the ASCII magic `SIS` (two
  sub-versions) or `SAF`, decodes to an image (dimensions, palette, pixel
  data) and - for one `SIS` sub-version - is deflate-compressed (the
  player links zlib and checks the standard `Z_DEFLATED` method byte before
  calling into it). This format is **not** present inside the `.SGS` payloads
  in the corpus (a raw byte search for `SIS`/`SAF` found no matches in any of
  the 12 samples), so it is likely used only for external icon-style assets,
  not a title's own in-game media - but that is inference, not confirmation.
  The `.SGS` body itself did not decompress as raw or zlib-wrapped DEFLATE at
  any of the first 400 byte offsets tried, and its overall byte-value
  histogram is dominated by "nibble-duplicated" bytes (`0x00, 0x11, 0x22, ...,
  0xff`), consistent with uncompressed low-bit-depth (likely 4bpp grayscale)
  raster image data rather than compressed or encrypted bytes.
- In that earlier pass, no opcode mnemonics, dispatch table, or resource-table
  layout were located. Finding them required substantially more
  static call-graph archaeology from the `Invalid*` exception sites, or
  dynamic tracing (running `cr32256_Magic.exe <path>.sgs` - it auto-opens a
  path given on the command line via the standard MFC single-document
  command-line path - under a debugger and diffing memory across `ReadFile`
  calls), which was not available in this environment.

A separate 2026-09-12 static pass on a hash-qualified supplied GVM2X build
located its dispatcher and opcode `0x05` (signed-byte immediate push). See
[local reference research](reference-emulators-20260912.md#verified-single-opcode-contract)
for exact addresses, stack/PC effects and guard. Binary equivalence to the
earlier player is unverified. SGS entry/code mapping and resource layout are
still unresolved, so no loader execution milestone changes.

## What `loader/gnex` actually validates

For paired distributions, `Inspect` requires a basename-matched
`.SGS`/`.sgs` + (`.mod`|`.inf`) pair and a header that parses per `ParseHeader`
above. It deliberately does
**not** require the `.mod`'s MIME string or any other manifest field to
match, because the `.inf` shape does not carry one and the `.mod` shape
itself was observed in three slightly different byte layouts across only 12
samples - hard-coding manifest offsets would be fragile. The structural `.SGS`
header check (version byte + constant byte + a title that decodes cleanly as
cp949 within a bounded length) is the strong signal; the manifest pairing is
corroborating evidence that this is a distribution archive.

Without a paired descriptor, recognition is intentionally narrower: the
header must occur at offset zero or after exactly 32 all-zero prefix bytes,
the two observed placements. The title must decode without replacement
characters or control characters, and a nonempty body must follow its bounded
terminator. These checks also apply to raw input without relying on its file
extension. An SGS extension alone is insufficient. Multiple valid candidates,
including a paired candidate plus a standalone candidate, are rejected as
ambiguous. Raw payloads are limited to 64 MiB, and existing ZIP limits remain.

`ParseHeader` now rejects invalid character sequences even when the EUC-KR
decoder silently substitutes a Unicode replacement character. Historical
paired-descriptor prefix scanning remains available. The checksum algorithm
and SGS body-to-code mapping remain unknown, and are not invented as extra
checks. The separately recovered single-opcode contract does not validate
arbitrary bodies.

## Where this is wired in

`application/machine_load.go`'s `Load` tries `gnex.Inspect` once the KTF,
Raptor, and generic ABHS/EADS container probes have all declined a ZIP. A
recognized GNEX archive returns a specific
`%w: %q is an SKT GNEX title (%q); ARAM recognizes SinjiSoft GVM packages but
does not yet execute GVM bytecode` error (wrapping `ErrUnsupportedSource`)
that names the parsed title, rather than the generic "no valid ABHS or EADS
records" message an unrecognized ZIP gets - the same pattern
`loader.AndroidPackage` uses for Android APKs (see the comment above that
check in `machine_load.go`). This is a **recognized** milestone, not a
**loads** or **playable** one: nothing in this codebase executes a single GVM
instruction yet.
