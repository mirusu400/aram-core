# SKT GNEX (SinjiSoft GVM) titles

"GNEX" labels SK Telecom titles for SinjiSoft's GVM family rather than SK-VM
(Java, `loader/skvm`) or WIPI-C (`loader/raptor`, `loader/ktf`). `loader/gnex`
recognizes the archive and decodes the SGS image. The application factory
starts nine exact-hash GVM version-1/2 titles through its operational profile,
with guest frames and input. The supplied version-4 Virus title has a separate
32-bit runtime in `gnex32/`: splash, title, menu, help, Korean opening dialogue,
the opening scene, and directional gameplay input run with keys and timers.
A complete playthrough remains unverified. Other hashes
remain unsupported. See [gvm-execution-subset.md](gvm-execution-subset.md).

On the supplied ten-ZIP corpus, a longer product probe with 20 repeated select
presses, five frame ticks held and released for each press, and 500 additional
ticks reports `ok_frame` for all nine GVM version-1/2 titles. Focused reference
tests also check the deeper paths that previously faulted in 놈1, 라그나로크 외전,
마시마로격투맞고 and 짜요짜요타이쿤1. This is bounded frame and input progress,
not a complete playthrough or proof of gameplay correctness. The version-4
바이러스-무삭제 archive has partial product execution through the GNEX32 runtime.

The format notes below preserve the original investigation sequence. Statements
that a version-4 VM or individual services are unimplemented describe earlier
stages of that investigation; the current runtime lives in `gnex32/`.

### Current Virus gameplay status

From a fresh launch, select advances from splash to title, then to the GAME
START menu. A third select enters a black-backed opening sequence that now draws
Korean dialogue. Nineteen more select presses advance its page counter to the
animated game scene, where Korean dialogue also appears. Three more selects
advance through the first scene dialogue; left and right inputs then produce
distinct guest frames without a VM fault. A product-level reference test covers
the visible text and directional input.

The blank dialogue was caused by an incorrect `pushx` operand interpretation:
the final operand selects an element of the index symbol, rather than adding a
constant to that symbol's first value. The old behavior wrote text to media 2151
while the guest displayed media 2147. Correcting the opcode reaches `StrSub`,
which copies the requested byte range into the display string. The game now
draws the original Korean text from its SGS resources. This bounded probe does
not prove a complete playthrough.

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
    (`1.0.0` in the older samples and `36.9.0` in the supplied version-4
    sample), a `IP:;PORT:0;ID:<hex>` connection line, and a
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

The version-1/2 table below describes the older GVM layout. One authorized
title, identified by outer SHA-256
`bf9cd39b1ae14ba2a5f005d66fde5390e53cb400bd36940d4b09dcbfe9f03883`,
uses a distinct version-4 GNEX layout. Its 32-byte prefix declares its own
length. The observed table offsets are relative to the byte immediately after
that prefix. The 14-byte cp949 title span in this sample decodes to `바이러스무삭제`. The image
length field at relative offset `0x78` equals the complete payload length minus
32. Relative offsets `0x48`, `0x4c`, `0x50`, and `0x54` delimit code, an
8-byte-per-symbol descriptor table, four-byte-word symbol data, a
12-byte-per-media descriptor table, and byte-addressed media data. The 479
symbol lengths and 2,233 media lengths exactly fill their respective spans.
`DecodeGNEX32Image` validates and exposes this storage layout. SinjiSoft's
[Mobile C Library Function Reference](https://cfile209.uf.daum.net/attach/165521104A3E87994E43CC)
(pp. 9-10) says the GNEX VM runs earlier GVM content, while content using new
GNEX features requires the Mobile C 3.x compiler. That backward compatibility
does not establish that the version-4 bytecode can use the older GVM2X opcode
decoder. The code span
has mostly zero high bytes when read as aligned 16-bit words; this alone does
not establish instruction boundaries or opcode meanings. The flag words,
entry field's runtime semantics, 32-bit instruction semantics, and GNEX
host services remain unimplemented; a
recognized version-4 package is not yet executable.

### Version-4 execution investigation

In the supplied version-4 SGS, 81.7% of the high bytes of aligned 16-bit code
words are zero. This makes word-oriented inspection useful, but does not prove
the instruction encoding: SinjiSoft's [GNEX presentation](https://www.slideserve.com/frayne/2004-04-24)
describes SAL as a one-byte opcode followed by variable operands. The SGS may
store or widen that stream differently. Do not feed these words to the GVM2X
bytecode interpreter.

`DecodeGNEX32Image` bounds-checks and exposes the eleven candidate code
pointers in header slots `0x1c..0x44`, preserving zero as an absent slot. The
fields at relative offsets `0x24`, `0x34`, and `0x38` point to code
locations whose first aligned word is `0x000c` after adding the 32-byte SGS
prefix. The unadjusted `0x34` field instead points into a larger apparent
operand. An exploratory scan also found many repeated control-like sequences
with a code address split into two 16-bit words. These observations support the
prefix-relative address candidate represented by `GNEX32Image.Entry`. In the
repeated control-like sequences, a code address uses two little-endian 16-bit
words with the **high word first**. `GNEX32Image.DecodeCodeAddress` bounds-checks
this candidate encoding and returns an index into `Code`. The two operands at
the observed entry point resolve to the first two code words under this order;
a later address with a nonzero high word resolves to the final code word. The
hash-qualified reference test checks all three. This does not identify
general instruction lengths, jump conditions, stack effects, or native
service calls. A GNEX reference runtime or a
documented numeric opcode table is still needed to validate execution.

SinjiSoft's [2004 GNEX programming lecture](https://www.slideserve.com/frayne/2004-04-24)
names `main()`/`EVENT_START()`, `EVENT_TIMEOUT()`, and `EVENT_KEYPRESS()` as
application, timer, and key handlers. The version-1/2 SGS files in this corpus
consistently have nonzero 16-bit pointers at `0x1c`, `0x20`, and `0x22`. The
existing operational GVM path dispatches `0x20` on timer events and `0x22` on
key presses. The version-4 header has nonzero 32-bit pointer slots at the same
three offsets (`0x1c`, `0x24`, `0x28` after widening); their corresponding code
starts at absolute offsets 85494, 15206, and 478. This is strong evidence that
version-4 slots 0, 2, and 3 respectively hold start, timer, and key handler
addresses. It does not establish the meanings of slots 6 and 7 (also present in
this sample), the call convention, or how the version-4 VM dispatches events.

`DecodeAddressInstruction` now captures six recurring address-bearing layouts
at known code boundaries. In the supplied image, `0x7f` and `0x80` are followed
by one uninterpreted 16-bit word and a four-byte code address; `0x81`, `0x82`,
`0x83`, and `0x85` are followed directly by a four-byte code address. The
hash-qualified reference test checks examples at the observed entry point,
another header code pointer, and near the end of code. The decoder reports
instruction size and target without assigning a branch or call effect.

The media table has 1,575 records with flag `0x100`, 538 with flag zero,
89 with flag `0x1`, 30 with flag `0x200`, and one with flag `0x101`.
Eighty-eight flag-`0x1` records and the flag-`0x101` record have empty data.
Long flag-`0x100` records commonly begin with type bytes `0x0a` or `0x0b`,
outside the GVM2X sprite decoder's supported type range. In the supplied
version-4 image, 591 flag-`0x100` records match an exact indexed-image layout
(237 kind-`0x0a`, 354 kind-`0x0b`), including 240 under 80 bytes. Another 23
small records share a kind byte but fail the full layout check. The layout is:
kind; one-byte width and
height; two apparent signed anchor bytes; a mode byte; a palette-entry count;
three bytes per palette entry; then high-bit-first packed pixels at two or
four bits per pixel, respectively. An odd-length payload has one trailing zero
pad byte. Every decoded pixel index fits its palette. The bounded
`DecodeGNEX32IndexedImage` decoder checks this layout; palette channel order,
transparency, and display behavior still need validation. In this sample, all
321 large mode-`1` images use `20 90 20` as the first palette triplet, while
none of the 30 large mode-`0` images do. Across all sizes, every one of 541
mode-`1` images uses that triplet, while one of 50 mode-`0` images also does.
This suggests a palette-zero transparency key, but does not establish its
rendering rule. The 30
flag-`0x200` records carry an `MMMD` audio marker.
The first `0x92` instruction reached by the candidate VM follows a load of
symbol `0x19`, whose 32-bit initializer is 542. Media record 542 has flag
`0x1` and zero initial bytes. Other flag-`0x1` records are also mostly empty;
flag-`0x101` has one empty record, flag-zero records often contain terminated
text, and flag-`0x200` records contain sound data. `NewMediaMemory` now creates
an isolated, size-bounded per-run copy, provisionally allowing replacement of
flag-`0x1` and `0x101` records. This does not establish what opcode `0x92`
does with media 542 or prove native media mutation rules.
The Mobile C function reference describes `SetMediaSize` as resizing a media
resource and `GetChar`/`PutChar` as indexed byte access (pp. 317, 324-325).
`GNEX32MediaMemory` now provides bounded resize and byte access primitives for
candidate services, with per-run isolation and read-only checks. The numeric
mapping from `0x92` service IDs to those named functions is still unknown.
The code repeatedly calls IDs `0xa0`, `0xa5`, and `0xa6` in sequences that fit
string length, byte read, and byte write operations, respectively. The local
GVM2X opcode sequence `0x7c`-`0x82` implements `StrLen`, `StrCpy`, a counted
substring copy, `StrCat`, `StrCmp`, `GetChar`, and `PutChar`; adding `0x24` to
each opcode matches the consecutive version-4 service IDs `0xa0`-`0xa6`.
This is stronger evidence than call shape alone, but still not a native GNEX
service table. The nearby `0x9f` service has two-argument call sites and could
correspond to the older `StrInit` operation, `0x7b`; it should not be treated
as the one-argument `GetMediaSize`. In particular, media 542 starts empty, while a
tentative `0xa5` byte read at index three would require an earlier allocation
or defined out-of-bounds behavior.

The symbol table has 327 records with flag `0x1` (15,320 initializer bytes)
and 152 with flag `0x100` (12,044 initializer bytes). Every flag-`0x1` record
in this SGS has an all-zero initializer; 146 of the flag-`0x100` records have
nonzero data. Mutable storage versus read-only constants is a plausible
interpretation, consistent with the older GVM layout, but the version-4
symbol access instructions and write rules still need verification.
`NewSymbolMemory` prepares an isolated per-run copy under that tentative flag
interpretation, rejects unknown flags, and provides bounded little-endian
32-bit reads and writes. It is a runtime building block; the ordinary product
path still has no version-4 VM.

An isolated candidate VM in `gnex32` now tests the first start-handler path.
In the sample, aligned `0x85` words followed by in-range address candidates
usually point far from their own location (median absolute separation 26,588
code words), while the comparable `0x80` and `0x83` candidates are usually
nearby and forward. These counts can include operand words. Code offset zero
contains `0x86`, and
another `0x86` lies immediately before the key-handler pointer. These patterns
support a provisional `0x85` call and `0x86` return interpretation. The
candidate VM also reads `0x04` as a 32-bit symbol-word push, following the
older GVM opcode. It now supports candidate `0x03` indexed 32-bit symbol reads
and `0x05` sign-extended immediate pushes, with separate malformed operand
tests. Under those assumptions, the hash-qualified version-4
archive executes four candidate steps: call code offset zero, return, call
code offset two, and read symbol `0x19` (value `0x21e`). It then stops at
opcode `0x92` at code offset six (SGS absolute offset 166). The meaning and
host effect of `0x92` are unknown. The candidate VM recognizes its following
16-bit service number and rejects it as unsupported by default. An optional
caller-supplied handler can state an explicit bounded stack change. Under a
diagnostic assumption that service 1 consumes the media index without returning
a value, the sample advances through `0x03` and `0x05`. In the local
hash-qualified GVM2X reference emulator, dispatch-table opcode `0x5d` points
to x86 handler `0x418cf0`, which pushes literal 5 without reading an operand.
That meaning is inconsistent with the version-4 context: among 413 aligned
`0x5d` words, 230 are followed by `0x10` and 167 by `0x83`; examples repeatedly
prepare two values just beforehand. Nearby `0x59`-`0x5e` opcodes show the same
comparison-and-branch shape. The candidate VM therefore treats `0x5d` as
32-bit equality (two inputs, one 0/1 result); this remains unverified against
a native GNEX runtime. The following `0x10` appears before 772 aligned `0x7f`
words in a repeated case-table pattern and before 228 `0x83` words. This
supports a tentative top-of-stack duplicate effect. The following `0x83`
contains a code address and appears after comparisons; the candidate VM treats
it as a branch on zero that consumes one stack value. With these additional
assumptions, the diagnostic trace calls code offset 17,314 with four values on
the stack. The callee begins with four `0x0b` instructions, each followed by a
different writable single-word symbol index. Treating `0x0b` as a pop into
that symbol advances the trace to service `0xa5` at code offset 17,338.
Two further recurring layouts support provisional direct symbol assignments:
638 aligned `0x75` words are followed by an in-range symbol index and another
word, usually a small constant or `0xffff`; 629 of those destinations are
mutable symbols. Seventy aligned `0x74` words have two in-range symbol
operands, 66 with mutable destinations. The candidate VM interprets `0x75`
as signed-immediate assignment and `0x74` as a source-to-destination word copy.
These counts may include operand words; the effects are not native-validated.
Additional candidate-only assumptions interpret `0x80` as a branch when its
top value differs from the 16-bit operand, `0x0f` as decrement, and `0x5a` as
a signed less-than comparison. The `0x80` direction is supported by the
repeated comparison chain at code offsets 334-454: each mismatch skips an
immediate symbol assignment and retries with the next constant. With service
`1` treated as a one-argument void operation and `0xa5` as byte read, the
isolated trace first reached opcode `0x8d` at code offset 17,436. That
diagnostic treats the out-of-bounds read from initially empty media 542 as
zero; native behavior is unverified. The corrected `0x80` branch skips the
nearby `0xa0` and `0xa6` calls in this trace. The nearby `0x8d` and `0x90` patterns suggest an
indexed lvalue/store pair. A candidate `0x8d` symbol/element reference and
`0x90` store, together with candidate 32-bit AND (`0x53`), XOR (`0x56`), and
logical right shift (`0x57`), advance that trace to opcode `0x7d` at code
offset 17,528. Four assignments to symbol `0x164` then hold `0x90`, `0xff`,
`0xaf`, and `0x92` under those assumptions. An additional signed
greater-or-equal immediate branch candidate for `0x7d`, plus candidate 32-bit
arithmetic at `0x12`-`0x14`, advance the trace to unsupported opcode `0x50`
at code offset 17,616. Their native effects and encoding remain unverified.
This is a trace through candidate semantics, not evidence that the title boots.
The service-1 stack effect and all version-4 opcode effects remain unverified
against a native GNEX runtime. They are not used in the product path, so the
candidate VM produces no game frame.

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

The earlier header-only investigation did not decode the body. Later work
recovered version-1/2 descriptors, operations, services, and a hash-qualified
application path. `Header.BodyOffset` is not an execution entry address.

### Historical body investigation

The following observations describe the earlier player investigation, not the
later hash-qualified build. In particular, its reported symbol stride must not
be substituted for the six-byte runtime entries recovered in the later build.
Static analysis of the earlier player found enough of the GVM runtime's shape to
describe it, but not enough at that time to write an interpreter:

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
earlier player is unverified. Follow-up work led to the current operational
subset documented in [the execution subset](gvm-execution-subset.md).

## What `loader/gnex` actually validates

For paired distributions, `Inspect` requires a basename-matched
`.SGS`/`.sgs` + (`.mod`|`.inf`) pair and a header that parses per `ParseHeader`
above. It deliberately does
**not** require the `.mod`'s MIME string or any other manifest field to
match, because the `.inf` shape does not carry one and the `.mod` shape
itself was observed in three slightly different byte layouts across only 12
samples - hard-coding manifest offsets would be fragile. The structural `.SGS`
legacy header check (version byte + constant byte + a title that decodes
cleanly as cp949 within a bounded length) is the strong signal; version 4 uses
full structural validation. The manifest pairing corroborates the archive.

Without a paired descriptor, recognition is intentionally narrower: the
legacy header must occur at offset zero or after exactly 32 all-zero prefix
bytes. A version-4 image may have a validated 32-byte length prefix. The title
must decode without replacement characters or control characters, and a
nonempty body must follow its bounded terminator or fixed title span. These
checks also apply to raw input without relying on its file
extension. An SGS extension alone is insufficient. Multiple valid candidates,
including a paired candidate plus a standalone candidate, are rejected as
ambiguous. Raw payloads are limited to 64 MiB, and existing ZIP limits remain.

`ParseHeader` now rejects invalid character sequences even when the EUC-KR
decoder silently substitutes a Unicode replacement character. Historical
paired-descriptor prefix scanning remains available. The checksum algorithm
and general SGS execution validity are not recognition checks. The separate
execution-image decoder imposes its own narrower variant and safety limits;
recognized headers do not establish executable bodies.

## Where this is wired in

`application/gvm_factory.go` routes supported exact-hash GVM samples into the
operational VM before `application/machine_load.go`'s generic inspection path.
For other GNEX archives, `Load` tries `gnex.Inspect` after the KTF, Raptor,
and ABHS/EADS probes. It returns `UnsupportedPlatformError` with the parsed
title. This includes the version-4 sample: recognizing and decoding its SGS
does not establish a working GNEX 32-bit interpreter, event services, or frame
generation.
