# Local reference emulator research (2026-09-12)

## Scope and evidence boundary

This is interoperability research on user-supplied emulator/SDK artifacts, not
an import of their implementation or a redistribution license. Artifacts were
read in place. No reference executable, installer or APK was run or installed.
No expiry check was patched. No game, ROM, extracted class, native library,
asset or raw disassembly is included in this repository. Saved database
contents were not examined. Metadata and static control flow are evidence for
these particular builds, not an original-handset oracle.

The product baseline for this research is core `1001bd5` with the earlier
explicit LGT native subset. Existing BREW/GVM recognition must not become a
claim of execution merely because reference symbols have been identified.

## Hash-qualified inventory

| Label | Bytes | SHA-256 |
|---|---:|---|
| GVM2X executable | 1056768 | `c2c170b3c47cd99dc55fdadad31dd1927473b4362868205c5eb49eff96c60719` |
| LGT MIDP executable | 6459392 | `6a989f357ef95db1e10b393018915f7b8729d61a754d83fb2c71d74a81158e72` |
| LGT pdb.f | 262144 | `3b874d3ba46c638fc3094f8e92fb744ca974893873f8885f54e23760f9b6311b` |
| LGT sfs.f | 2097152 | `4bda3a28f4ffe603c0ec1258c0034d65a1a0d35ab7bd523a834608adabf03cc5` |
| LGT ZIP distribution | 3327420 | `5d07966316c8b15a68856235eae1a02fef5b54d6f23ff3b73876d0b979049a8a` |
| i830 SDK installer | 12938197 | `69f0f81dc4632fa437ad67414874a37026953bf0f1e0b9731d867d7dd2c3bb8c` |
| i830 SDK 7z distribution | 12227147 | `301c0a68ff1bdf94494e55e12feeb767f78fb8841ff0e1d602c19983723a62f3` |
| Melange APK | 1177435 | `aa391bd9099ebb50bd7f8f41c53bc0616e568e250698e3b342f49d8a12044437` |

The three LGT runtime files inside the ZIP hash-match the loose supplied files.
The ZIP has 85 entries and 8851310 total advertised expanded bytes, with no
absolute/traversal paths, case-folded duplicate entries or symlink entries in
this inspection. These checks do not make executing it safe or licensed.

## Initial platform classification

### GVM2X

The PE version resource identifies Sinjisoft, product `GVM2X Emulator`, version
`2, 0, 6, 40328`. It is x86 PE32 at preferred image base `0x400000`. The visible
SGS filter and `GVM 2x Emulator (128KB)` strings corroborate the platform.
The supplied filename suggests prior modification; publisher authenticity and
bitwise equivalence to the earlier `cr32256_Magic.exe` research are unverified.

A `GVM1X` string reference near VA `0x411e1c` is a runtime string-table
initializer, **not** evidence of a loader/version check. Existing
[GNEX research](gnex-format.md) already documents the broad format and VM
shape. New progress requires numeric opcode, operand/stack, loader or resource
contracts, not repeating those strings.

#### Verified single-opcode contract

For the exact GVM hash above, static x86 control flow now establishes the
following contract. Addresses are preferred-image VAs, not runtime captures.

| Boundary | Address / observation |
|---|---|
| Dispatcher | `0x41e3b0`, u8 fetch at `0x41e3d8`, indexed call at `0x41e3f2` |
| Dispatch table | `0x494a68`; entry 5 at `0x494a7c` points to `0x415a20` |
| Opcode | `0x05`, analyst label `push_signed_i8` |
| Operand | One signed byte read at `0x415a41`, sign-extended to a 16-bit slot |
| Stack effect | Increment top index, store one i16 at `0x415a46` |
| PC effect | One operand byte plus opcode, total instruction size 2 |
| Guard | Signed top index >= `0x40` invokes error 5 before reading operand |
| Globals | PC `0x527704`, top index `0x527708`, stack base `0x52770c` |

The dispatcher uses the table below `0xf3`. Values `0xf3..0xfe` take error 7;
`0xff` returns without an operand. A nonzero entry initializes stack indices
to -1 and derives the initial PC from the code base and an unsigned 16-bit
entry offset. These observations do **not** establish how SGS headers map
that entry/code base, the complete stack capacity, other opcodes, resource
decoding, cross-version compatibility, or successful guest execution. In
particular, retain the observed guard rather than inventing a corrected
capacity. This is enough to design a future independent opcode unit test,
not enough to turn recognized SGS files into executable machines.

### LGT MIDP

The x86 PE contains ROMized Java class/method metadata. Both `.f` files were
independently verified to contain only `0xff`; they are not populated
class/debug stores. MediaPlayer has 11 Java methods and no direct native
methods. Its class at VA `0xa1dd60` owns method table `0x8996a0`, field table
`0x8cdb50`, and constant pool `0x8f5ca0`.

Hash-gated structural verification passed all 11 owner links, the target field
slots, 10 complete method bodies (230 normalized instructions), and every
decoded branch boundary. Quickened field operands identify instance slots,
not constant-pool indexes. Their interpretation is corroborated structurally;
native interpreter dispatch and dynamic execution remain unverified.

| Method body VA | Static Java-wrapper observation |
|---|---|
| Constructor `0x9e144c` | Sets isPlay false and initializes volume from `Preference.getRingingSoundLoudness()I`, not a literal five |
| Getter `0x9e14f4` | `getVolumeLevel()Ljava/lang/String;` returns cached instance volume as a decimal string, not a list of allowed values or a HAL query |
| Setter `0x9e14fc` | Parses the string first, checks **old cached volume** against 0..5, then assigns the parsed new value; calls HAL only while isPlay |
| Stop `0x9e1554` | Delegates unconditionally to ResourceManager.stopMidi, then clears isPlay on successful return |

The setter's surprising old-value guard follows two explicit receiver-field
loads at `0x9e1502` and `0x9e1509`, followed by assignment from parsed local 2
at `0x9e151a`. Do not silently substitute a new-value range check when
describing this image. Parser failures precede mutation. Null, empty, invalid
digit and overflow paths construct NumberFormatException; the Character.digit
repertoire and native HAL volume effects remain unresolved.

ResourceManager's stop overload at `0x9cf847` locks a MIDI mutex, compares the
current MIDlet state with the shared MIDI owner, and conditionally clears that
owner and calls HAL.stopMidi. Consequently, stopping a source-less player is
not proven globally side-effect-free: ownership is MIDlet-scoped rather than
player-instance-scoped. The preference volume storage is zero-fill memory;
its initialized runtime value is not established by inspecting file bytes.

These facts expose differences from the intentionally conservative
[current LGT adapter](lgt-mmpp.md): its getter remains unsupported, its new
volume range/source guards are emulator policy, and its empty-stop identity
does not model shared MIDlet ownership. No behavior is changed solely on this
static evidence. Verify quick-op dispatch and isolate a reference-specific
compatibility policy before adopting the apparent setter defect or shared
audio side effects across titles.

### Motorola i830 SDK

The supplied i830 label and initial installer metadata identify a Motorola iDEN
SDK, not an LGT SDK. However, the recovered README identifies Motorola iDEN SDK
1.1.0 (2005-12-16) and says the emulated device is **i730**, using
**CLDC 1.0 / MIDP 2.0**. Preserve this label/payload discrepancy; do not claim a
verified i830-specific runtime from the filename. The InstallShield launcher
version `7, 01, 100, 1248` is separate from all of those versions.

The installed 7-Zip 9.10 cannot open the EXE wrapper. A bounded in-memory parser
using the publicly described InstallShield 6/7 cabinet layout located candidate
volumes at EXE offsets 105513 and 599387, and the descriptor header at 416536.
The descriptor enumerates 90 directories and 1738 file records, including linked
or duplicate records; this is not a count of unique APIs. Each selected payload
was decoded using length-prefixed raw-DEFLATE chunks, checked against expanded
size and the cabinet's MD5, and discarded after inspection. No installer or
payload code was executed. Format reference: Unshield `lib/cabfile.h` and
`lib/file.c`, read on 2026-09-12; no Unshield implementation is imported here.

Nine selected records passed size/MD5 checks: two license texts, README,
Canvas/RecordStore/GameCanvas class files, and their three HTML API documents.
The class files are version 46 and their class ownership/descriptors were
parsed independently. An API signature or a Code attribute does not prove
that an SDK class body is an operational handset implementation rather than
an SDK stub. The license text contains restrictions concerning reverse
engineering/disassembly and source code. This research does not assert legal
permission to redistribute or link the SDK; keep license review separate.

| Record | Payload | SHA-256 after verified in-memory decode |
|---:|---|---|
| 278 | Canvas.class | `3c0bfccd88a52bf8fd3e0213cf4c1d5453de5ee84341d2559014d8dd295d13f3` |
| 319 | RecordStore.class | `0a054f020b3f8956c7cd25a0c59fa62d44c0d25f501fe95c9cc0ed83bbfb5240` |
| 324 | GameCanvas.class | `b1f8c6a6bc0930a3631f609ada3ecf101992a33cd219545a41bddfa73d22b5aa` |
| 386 | README | `1d75007971f642d326b7cdcd108eb4d82901de60102945ccd8a29bc5eef32e3a` |
| 742 | Canvas API document | `cd746c8a4195cd2092ace29714a795fc2b0471ff6c0ca995b37fb7b40d3ca09d` |
| 798 | RecordStore API document | `cc07d0656180180f994b2773f40eb3bb21b238f475b4b988d175ef829ad67dd0` |
| 803 | GameCanvas API document | `83dd0bd5ce22798777b905748b31b540cf34f873b61e8e097ac7fdf032b66007` |

The GameCanvas API document provides testable standard contracts:

- `getKeyStates()I` reports keys held now **or pressed since the previous poll**;
  polling clears the latter latch without clearing currently held keys.
- Hidden canvases return zero. Showing a canvas resets reported keys; a key
  already held must be released and pressed again before being reported.
- `flushGraphics(IIII)V` clips to the canvas bounds, writes no pixels for width
  or height less than one, and does nothing when the canvas is not shown.
- `paint(Graphics)` respects the destination clip and origin translation.

These are recovered documentation contracts, not dynamic execution results from
this SDK. They provide independent synthetic conformance targets for ARAM's
standard MIDP path; Motorola-specific methods must not leak into generic/LGT
profiles just because this SDK supplies them.

### Melange

Binary Android manifest metadata identifies package
`io.github.usernameak.brewemulator`, version `1.1.3`, code `101030`, min SDK 10,
target SDK 20. Its sole native library is ARM32 little-endian ELF `ET_DYN`,
EABI5, with dynamic symbols and no ordinary symbol/debug table observed.
A `BREW/3.1.5.179` user-agent string fingerprints the runtime family but does
not prove conformance to that BREW release.

Surviving symbols include `AEEMod_Load`, `AEEStaticMod_New`, `AEEApplet_New`,
`OEMMod_GetStaticModLists`, `OEMMod_GetStaticClassLists`, `AppInst_ReloadModules`
and `breMainStart`. Symbol values with low bit set are Thumb addresses, not
absolute runtime addresses. In particular, a static-module `AEEMod_Load`
symbol does not by itself specify arbitrary raw MOD mapping or bootstrap.
Android `.rel.dyn`/`.rel.plt` are host ELF relocation evidence, not automatically
the guest MOD relocation format.

The package namespace does not authenticate publisher identity. Mixed compiler
metadata and component copyright notices do not establish redistribution rights
for the whole binary. No LICENSE/NOTICE-named ZIP entry was found. Keep it as a
local research oracle, not a linked or shipped dependency.

#### Bounded loader investigation

Static call resolution confirms that exported `AEEMod_Load` (Thumb symbol
`0x1cc157`, code `0x1cc156`) calls `AEEStaticMod_New` through PLT `0x2a2550`
at call site `0x1cc176`. Evaluating that stub yields GOT `0x2a77e0`, whose
host ELF relocation identifies the callee. Its arguments are a 20-byte object
size, the three incoming arguments, and two zero arguments. The callee builds
a static-module object/vtable. Those object fields are not guest MOD headers.

The resolved `mmap` call at `0x1b9c1e`, inside `OEM_GetHeapInitBytes`, requests
private anonymous RWX memory (fd -1, offset zero), not a MOD file mapping.
This does not rule out subsequent copying of MOD bytes into that heap.
`AppInst_ReloadModules` enumerates through virtual calls and forwards values
to an unresolved vtable slot. Its name alone is not a dynamic loader contract.

The next candidate file-I/O anchors are calls at `0x2314c2`/`0x23150c`
(`OEMFS_Open`) and `0x231102` (`OEMFS_Read`). They are **not yet proven
MOD-specific**. File identity and buffer consumers must be traced before
inferring segment layout, BSS, relocation, entry state, r9/static base or
applet ABI. No raw MOD bootstrap is justified by this pass.

## Development priorities and acceptance boundary

| Finding | Owning next step | What it does not prove |
|---|---|---|
| SDK GameCanvas polling contract | Standard `skvm` input latch and display-transition regression tests | Motorola handset fidelity or a complete game playthrough |
| LGT cached-volume getter | Verify quick-op dispatch, then separately test an explicit LGT policy | A universal MMPP range/default or safe adoption of this setter's apparent defect |
| GVM opcode 0x05 | Independent opcode tests after code/entry mapping is established | A loadable SGS program or implemented GVM runtime |
| BREW wrapper and heap mapping | Follow file identity through buffer consumers and actual module entry | A raw MOD memory/relocation/initial-register contract |

The initial GameCanvas reproducer uses independently authored guest bytecode:
construction, Display.setCurrent and getKeyStates all dispatch through guest
instructions entered with public VM.InvokeStatic; input uses public VM.KeyEvent.
No proprietary SDK class body or direct native-map invocation is involved.
At baseline `1001bd5`, held-key repeated polling passes, but a press/release
between polls loses its bit, a hidden canvas reports a stale bit, and a
reshown canvas reports an already-held key. The latter three are observed
failures, not conclusions from source inspection. This is a public VM
conformance test, not a full MIDlet or ordinary desktop launch.

The owning implementation now separates physical held keys, per-canvas
pressed-since-poll latches, and physical keys suppressed on display entry.
Numeric/directional/SKT aliases retain independent lifetimes. Polling does
not clear held keys, actual display transitions reset the new canvas, and
reselecting the current canvas does not reset it. No SDK implementation was
imported and no Motorola-only natives were added.

| Changed contract | Concrete regression check | Observed focused result |
|---|---|---|
| Short tap retained for one poll; held keys retained | `TestSDKGameCanvasPublicContract`, held_control/released_between_polls | Correct masks and latch clearing |
| Hidden/reshown/first-shown and same-canvas transitions | Same test, visibility/reselect/hidden_release scenarios | Hidden zero, held-on-entry suppressed until released/repressed, reselect preserved |
| Multiple physical keys for the same action | Same test, aliases_held/aliases_suppressed | Releasing one alias preserves another hold; fresh alias is not wrongly suppressed |
| Serializable latch and suppression | Same test, replay_latch/replay_suppression | Byte-identical deterministic replay |
| Alert/null-display path and existing callbacks | `TestGameCanvasDisplayTransitionsAndCallbacks` | Both display overloads checked, raw SKT callback keys and suppression preserved |
| Fresh/pre-input/legacy and malformed snapshots | `TestGameCanvasInputStateCompatibility` | Fresh restore and missing-field migration pass; wrong type/extra field rejected transactionally |

The public guest-bytecode scenarios run under J2ME, SKT and LGT policies
(11 scenarios each). Supplemental native/state boundary tests are distinct
from those public-dispatch checks. Focused tests and vet passed after the
original three failures and review-discovered alias/snapshot issues were
fixed. The outer state schema remains unchanged. A legacy snapshot lacks
physical key history, so its missing physical mask defaults to zero while
its existing per-canvas action mask survives until input or a transition.
This cannot reconstruct which historical aliases were held.

Independent post-analysis checks confirmed all eight inventoried hashes
unchanged. GVM table/operand/store/guard checks and LGT ROM structural checks
were rerun successfully without executing either reference. Detailed local
helpers and normalized reports live outside the product repositories; none
of the supplied payloads is a build or runtime dependency.

## Workspace validation of the input fix

After source freeze, ordered core tests/vet, 94 runner unit tests, frontend
tests/vet and product integration tests/vet passed. The ordinary workspace
probe synthetic suite passed all 19 expected cases, including its existing
frame/input conformance checks. An equal-scope comparison reports 20 unchanged
synthetic/reference rows. Windows product/probe/frontend builds, pure-Go
Android/arm64, Linux/amd64 and Darwin/arm64 core builds, and the official
Android frontend binder passed. This round did not repeat a desktop GUI
playthrough or establish a new commercial-title playability milestone.

The configured private `all` gate still exits **1**, not success: the same six
KTF/Raptor reference tests cannot find their required packages in the supplied
legacy corpus. Equal-scope comparison with the preceding baseline reports
**495 unchanged rows, zero regressions**. The synthetic-only run's automatic
delta removes 475 private rows because its input scope excludes them; it is
not the like-for-like comparison. Both final delta and triage were inspected.
The largest remaining cluster is 358 recognized-but-unexecuted BREW inputs,
followed by unsupported external savedata; nine GVM inputs remain recognized
only. Missing reference inputs remain a failed acceptance gate and block an
unqualified whole-workspace completion claim. Expectations were not weakened.
