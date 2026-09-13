# BREW container recognition and P4-P7 evidence boundary

Status: 2026-09-11, **recognized only**. `loader/brew` does not load or execute
native code. The legacy-platform plan belongs to `aram-emu`; this document
records the core parser's evidence and remaining execution prerequisites.

## Read-only investigation

The user-authorized legacy corpus was inspected in place, without extracting
or executing applications. No reference implementation was copied. Counts
below concern top-level ZIP members, not unique games. Nested ZIP and ALZ
contents were not recursively interpreted by this investigation.

- 361 archives contain 362 MIF entries and 359 MOD entries.
- 357 archives have one MIF and one MOD, one has two of each, and three have
  MIF metadata but no MOD member.
- None of the 359 MOD entries starts with an ELF or DOS/PE container marker.
  Dominant initial instruction patterns are recognizable little-endian ARM
  register-save and PC-relative startup code. This is evidence of native ARM
  startup code in those variants, not a complete executable format or ABI.
- MIF and MOD names are not same-path basename pairs in this sample. Some MIF
  basenames match a module directory, others are merely co-located, and some
  have neither relation. The parser does not fabricate a MIF-to-MOD link.
- A SIG member's presence does not prove signature validity, protection state,
  authorization for device installation, or an executable loading contract.

The reference workspace's documented HAL/BREW backend mappings describe
selected firmware services beneath WIPI. They do not establish the standalone
BREW application's module bootstrap ABI. Reusing a WIPI entry point, stack,
relocation scheme, object table, or service selector would be an unsupported
assumption.

## Observed MIF envelope

All 362 inspected MIF members have this bounded little-endian envelope. The
field names below describe structural regions only. The records' contents,
resource types, ClassIDs, display strings, and MOD associations remain opaque.

| Header offset | Parser metadata | Evidence and validation |
|---|---|---|
| 0 | `Tag` | Observed `0x00010011`, not assigned an invented SDK version |
| 4 | Not exposed | Meaning not established |
| 8 | `TableOffset` | Observed 32; must be at least the 32-byte envelope size |
| 12 | `TableSize` | Region must fit the member |
| 16 | `IndexOffset` | Must follow the table without overlap |
| 20 | `IndexCount` | `(count + 1) * 4` bytes must fit before the data region |
| 24 | `DataOffset` | Must follow the index region without overlap |
| 28 | `DataSize` | Region must fit the member |

Observed relationships include an 8-byte table-record stride, an index region
of `(count + 1) * 4` bytes, and 28 bytes beyond the stated data region. The
parser only checks the documented envelope ordering and bounds. It does not
interpret index contents, assign semantics to trailing bytes, or demand an
unverified checksum. Widened arithmetic prevents 32-bit offset/count overflow.
Other MIF tags are not recognized as this variant.

## Public loader contract

`brew.Inspect([]byte)` returns a `Package` containing independently sorted
`Modules`, `MIFs`, and bounded archive `Files`. Every `Module.Format` is
`opaque-native`; no CPU, entry point, memory layout, relocation, load success,
or execution-success field is invented. Module size and same-stem signature
member presence are metadata only.

At least one known MIF tag, valid MIF envelopes, and one nonempty MOD member
are required. MIF-only inputs return `FormatError` identifying the missing MOD.
Raw MOD and extension-only guesses return `ErrNotPackage`. A bundle with
multiple modules is recognized without selecting a presumed application.

Limits are 10,000 ZIP entries, 64 MiB per member, and 128 MiB total expanded
content. Unsafe names, symbolic links, case-insensitive duplicates, invalid ZIP
checksums/sizes, and malformed envelope spans are rejected. Errors identify
member-relative offsets when known. All payload bytes remain untrusted.

## Validation and milestones

Synthetic tests cover recognition, multiple unassociated modules, opaque
signature/resource handling, other-format rejection, missing/empty modules,
truncated MIF headers, offset/count overflow, overlapping regions, unsafe
paths, duplicate names, symlinks, and ZIP limits. Fuzz seeds exercise MIF
boundary validation. No fixture contains corpus bytes or executable guest code.

The optional `TestReferencePackages` uses process-local `ARAM_TEST_DATA` and
logs aggregate counts without private paths or payloads. The authorized run
recognized 358 BREW containers containing 359 opaque MOD members and 359 MIF
envelopes, and explicitly classified three more containers as missing MOD.
All 362 MIF envelopes were also checked in the read-only structural survey.
No module was mapped or executed, and missing modules were not counted as
successful loads.

For SGS, `loader/gnex` tests and the authorized reference run pass after adding
standalone recognition. Nine corpus SGS entries have valid known headers.
See [GNEX format](gnex-format.md) for the stricter standalone checks.

| Plan step | Implemented or established | Blocking evidence still needed |
|---|---|---|
| P4 BREW loader/bootstrap | Bounded container recognition and MIF envelope metadata | Code/data/BSS boundaries, entry/load contract, relocation and CPU requirements, app creation ABI and repeatable entry/event observation |
| P5 BREW services | Existing shared services remain reusable in principle | Verified object interfaces, method arguments, reference lifetime, event/timer behavior, BAR decoding, and a BREW-specific state schema |
| P6 GVM specification | Version/title header plus paired and standalone recognition | Versioned instruction encoding, stack/value/control-flow semantics, resource tables and reproducible expected outcomes |
| P7 GVM execution | No implementation or execution claim | P6 evidence, independent VM, service bindings, state schema and hash-specific first-frame/input verification |

These changes do not complete P4, P5, P6, or P7. Generic native ARM execution
and Java execution are not substitutes for either missing runtime contract.
