# WIPI / CLDC / MIDP API coverage audit

Audited 2026-09-08 against `https://mirusu400.github.io/wipi-wiki/llms.txt`
(WIPI v1.2.1, v2.0, v2.2.0 plus the CLDC 1.1 and MIDP 2.0 class indexes the
same wiki carries).

This document is the work list for closing the media and Java API gaps. It
records what the spec asks for, what the four runtimes actually bind today, and
where the fix goes.

For state transitions, callback values, clip lifetime, PCM delivery, and the
dynamic test plan, see
[`wipi-media-conformance-and-fuzzing.md`](wipi-media-conformance-and-fuzzing.md).

## What "implemented" means here

The comparison is mechanical and it measures **symbol presence**, not semantic
conformance:

| Runtime | Inventory taken from |
|---|---|
| KTF | `HostJavaClassSpecs` in `application/internal/ktf/ktf_java_specs.go`, plus the handler switches in `ktf_java_native.go` |
| SKVM | `defaultHostSupers()` and every `RegisterNative` call under `skvm/` |
| WIPI-C (LGT) | `wipi/catalog_gen.go` entries versus the `case` labels in `application/internal/wipi/` |
| Raptor | the import-name switch in `application/internal/raptor/raptor_runtime.go` |

A dispatch entry existing does **not** mean the behavior is faithful. Several
entries are deliberate no-op stubs, and this document calls those out
individually. Conversely, per-method semantic conformance was only checked for
the media packages and `java.lang.Math`; the rest is class-level.

## Scoreboard

| Area | Status |
|---|---|
| WIPI-C C API catalog (all families) | 238 / 239 |
| WIPI-C `MC_MDA` (v1.2.1 set) | 21 / 21 |
| WIPI-C `MC_MDA` v2.0/v2.2 extensions | 0 — not in the catalog at all |
| KTF Java API v1.2.1 classes | 149 / 149 |
| KTF `org.kwis.msp.media` classes | 6 / 6 (pause/resume and completion callbacks fixed; recording remains absent) |
| Raptor sound | `RAPTOR.snd*` 8 + `MC_mda*` 12 |
| SKVM CLDC 1.1 classes | 56 / 81 |
| SKVM MIDP 2.0 classes | 15 / 67 |
| SKVM `javax.microedition.media` | 0 / 8 — absent |
| SKVM float/double bytecode | 26 / 26 opcodes |

## 1. Audio decoders (shared, `runtime/`)

All four runtimes funnel into the same `Media` service, so decoder coverage is
a single fact rather than four.

| Format | Status |
|---|---|
| SMAF / MMF, Yamaha FM synthesis | `runtime/smaf_decode.go`, `smaf_fm.go`, `smaf_voice.go` |
| SMF (Standard MIDI File) | `runtime/smf_decode.go` |
| WAV PCM, 8/16-bit, mono/stereo | `runtime/media.go:1249` |
| MP3, AMR, QCP/QCELP, AAC | absent |
| Tone sequences — `MC_MDA_TONE_*`, `MH_mdaTonePlay`, `MH_mdaFreqTonePlay`, MIDP `ToneControl` | absent; no tone synthesis anywhere in the tree |
| Video — `VideoClip`, `MC_mdaSetClipArea` | absent |

Note that the declared media-type string is effectively ignored: format is
chosen by content sniffing (`looksLikeSequencedScore`, then
`decodeWavePCM16`). That is the robust choice and should stay — a handset's
type strings are unreliable and titles lie about them.

## 2. Sound API, per runtime

### 2.1 WIPI-C (LGT), `MC_MDA`

All 21 catalog entries are implemented in `application/internal/wipi/wipi_media.go`.

The catalog itself only carries the v1.2.1 set, so the v2.x additions are not
merely unimplemented — they are unroutable. Missing from
`wipi/catalog_gen.go`:

- `MC_mdaClipAllocPlayer`, `MC_mdaClipFreePlayer`
- `MC_mdaClipControl`, `MC_mdaClipDevControl`, `MC_mdaClipModeControl`,
  `MC_mdaClipGetModeList`, `MC_mdaClipGetInfo`
- `MC_mdaGetInfo`
- `MC_mdaGetDefaultVolume`, `MC_mdaSetDefaultVolume`
- `MC_mdaSetClipArea`, `MC_mdaReleaseClipArea`, `MC_mdaUpdateClipArea` (video)

Unimplemented WIPI-C calls degrade gracefully: `DispatchAPI` returns
`defaultWIPIReturn(family)` and records the name in `r.Unimplemented`
(`wipi_runtime.go:846-860`). That counter is the right instrument for deciding
which of the above a real corpus actually needs.

### 2.2 KTF, `org.kwis.msp.media`

All six classes are present and every `Clip` method in the spec has a dispatch
arm. The audit found three behavioral defects; two are now resolved:

**(a) Resolved: `pause`/`resume` preserve playback position.** Before the fix,
`ktf_java_media.go`
computed `clip.playing = name == "play" || name == "resume"` and then called
`Media.Play` or `Media.Stop`, so `Player.pause` became a full stop and
`Player.resume` became a restart. The corrected path calls `Media.Pause` and
`Media.Resume`, which already preserve `clip.position` (`runtime/media.go:459,470`) — this
is a wiring fix, not new behavior.

**(b) Resolved: `PlayListener.playUpdate` is delivered.** Before the fix,
`setListener` stored the
listener (`ktf_java_media.go:181`) and `ktf_state.go:1050` serialized it,
but the `EventAudioComplete` arm only set `clip.playing = false`. It now queues
`playUpdate(clip, END_OF_DATA, 0)` through the serialized Java callback path.
This mirrors the WIPI-C arm of the same event (`machine_wipi.go:377`).

**(c) Remaining: no recording.** `record` returns 0 and `recordStart`,
`setPosition`,
`playStart`, `control`, `mediaFreeze`, `mediaReadData`, `mediaWriteData`,
`atomicGetUpdate`, `atomicPutUpdate` are no-op stubs.

Separately, the KTF WIPI-C media vector leaves slots 1, 2, 12, 14 and 15
unmapped (`ktf_wipic_media.go:225-250`); those fall to `ktfWIPICNoop`, which
traces the call and returns 0. Slot identities should be recovered from
reference evidence rather than guessed.

WIPI 2.0/2.2 Java media additions are absent apart from a name alias:
`BaseClip` (`allocPlayer`, `freePlayer`, `mediaControl`, `mediaModeControl`),
`PlayerListener`, `Camera`, `StillClip`, `VideoClip`,
`Volume.setMuteState`/`setDefaultVolume`/`setMute`, and the water-mark buffer
methods (`setWaterMark`, `reactiveWaterMark`). The only reference is
`ktf_java_native.go:993`, which routes the `BaseClip` class name to the `Clip`
handler.

### 2.3 Raptor

`RAPTOR.snd*` (8 functions) plus 12 `MC_mda*`. Missing relative to the
WIPI-C set: `MC_mdaClipGetData`, `MC_mdaClipAvailableDataSize`,
`MC_mdaClipGetType`, `MC_mdaClipPutDataByFile`, `MC_mdaClipSetPosition`,
`MC_mdaPause`, `MC_mdaResume`, `MC_mdaRecord`, `MC_mdaGetVolume`.

Unresolved imports are counted in `r.Public.Unimplemented`
(`raptor_runtime.go:577-585`), same instrumentation story as WIPI-C.

### 2.4 SKVM — `javax.microedition.media` absent

Zero references in the tree. The SKT-proprietary substitute
(`com/skt/m/AudioClip`, `com/skt/m/AudioSystem`) *is* properly wired to
`Services.Media`, including the blocking-play semantics titles depend on
(`natives_skt.go:665-790`).

The failure mode is what makes this urgent. WIPI-C and Raptor absorb unknown
calls; SKVM does not. A title reaching `Manager.createPlayer` gets a hard
`SKVM class "javax/microedition/media/Manager" is unavailable`
(`skvm/vm.go:766`) or `ErrMethodNotFound` (`vm.go:804`) and the machine stops.

`docs/skvm-runtime.md` now records that SKT audio reaches the shared media
service, while vibration, backlight, browser launch, and networking still have
no external side effects.

## 3. Java API coverage

### 3.1 KTF — v1.2.1, 149 of 149 classes

`java/lang/Long` now has a `HostJavaClassSpecs` entry covering every method
implemented by `handleLongMethod`. `java/lang/Float` and `java/lang/Double`
likewise declare their complete implemented method surfaces, so
`ktfHostJavaSpecOverride` can resolve constructors, conversions, parsing,
string conversion, equality, hash codes, and bit conversions. A table-driven
test checks every declared wrapper method against the native handler.

`java/lang/Math` now declares and implements the `I`, `J`, `F`, and `D`
overloads of `abs`, `min`, and `max`, plus `ceil`, `floor`, `sqrt`, `sin`,
`cos`, `tan`, `toDegrees`, and `toRadians`. NaN and signed-zero behavior is
covered by focused tests, and unknown methods now return a diagnostic error
instead of a plausible-looking zero.

`Math.E` and `Math.PI` need no work: `javac` inlines `static final double`
constants as `ldc2_w`.

### 3.2 SKVM — CLDC 1.1 and MIDP 2.0

All 26 CLDC 1.1 float/double bytecodes are now implemented:

```
fadd  dadd  fsub  dsub  fmul  dmul  fdiv  ddiv  frem  drem  fneg  dneg
i2f   i2d   l2f   l2d   f2i   f2l   f2d   d2i   d2l   d2f
fcmpl fcmpg dcmpl dcmpg
```

The implementation follows the existing int/long dispatch shape and preserves
JVM semantics for NaN comparisons, saturating float-to-integer conversions,
signed zero, infinities, and `fmod`-style remainder. Opcode-level regression
tests cover the complete set and the edge cases called out by this audit.

`invokedynamic` (0xba) is also unimplemented and should stay that way — no
CLDC-era compiler emits it.

Class-table gaps, with the effectively-harmless cases separated out:

| Spec | Missing from host table | Actually unimplemented |
|---|---|---|
| CLDC 1.1 | 34 of 81 | **25** |
| MIDP 2.0 | 53 of 67 | **52** |

The difference is 3 static-only classes with natives registered (`Math`,
`System`, `Connector` — no allocation needed) and 6 interfaces reached through
`hostInterfaces()` (`vm.go:469`).

The 25 real CLDC gaps, grouped:

- **Wrappers:** `Boolean`, `Character`, `Short`, `Float`, `Double` — no host
  class and no natives. `Float`/`Double` are now actionable because the
  supporting opcodes have landed.
- **Weak references:** `java/lang/ref/Reference`, `WeakReference`.
- **Character streams:** `Reader`, `Writer`, `OutputStreamWriter`
  (`InputStreamReader` exists).
- **Exceptions (15):** `EOFException`, `InterruptedIOException`,
  `UTFDataFormatException`, `ClassNotFoundException`, `Error`,
  `IllegalAccessException`, `IllegalMonitorStateException`,
  `InstantiationException`, `InterruptedException`, `NoClassDefFoundError`,
  `OutOfMemoryError`, `SecurityException`, `VirtualMachineError`,
  `EmptyStackException`, `ConnectionNotFoundException`.

`java/lang/Math` in SKVM has only `abs(I)I` and `abs(J)J`. Missing `min`/`max`
matters immediately — `javac` emits real calls for those and titles use them
constantly.

The 52 MIDP gaps, grouped:

- **Media (8):** the whole `javax.microedition.media` and
  `.media.control` surface. See §2.4.
- **Game (5):** `GameCanvas`, `Sprite`, `TiledLayer`, `Layer`, `LayerManager`.
  For a game runtime this is the second-largest functional hole after media.
- **High-level LCDUI (19):** `Form`, `List`, `TextBox`, `TextField`, `Alert`,
  `AlertType`, `Choice`, `ChoiceGroup`, `Command`, `CommandListener`,
  `CustomItem`, `DateField`, `Gauge`, `Item`, `ImageItem`,
  `ItemCommandListener`, `ItemStateListener`, `Screen`, `Spacer`,
  `StringItem`, `Ticker`.
- **Networking (7):** `CommConnection`, `HttpsConnection`, `PushRegistry`,
  `SecureConnection`, `SecurityInfo`, `ServerSocketConnection`,
  `UDPDatagramConnection`.
- **RMS (9):** `RecordStore` has 9 methods, but `RecordEnumeration`,
  `RecordComparator`, `RecordFilter`, `RecordListener` and the five specific
  exception types are absent.
- **PKI (2)** and `MIDletStateChangeException`.

### 3.3 Raptor

A thin Java surface by design (`StringBuffer`, `Reader` and a few others);
Raptor is a WIPI-C runtime and reaches the platform through `MC_*`. It reuses
KTF's spec table (`raptor_java.go:2054`), so the `java/lang/Long` gap in §3.1
propagates here unchanged.

## 4. WIPI-C C API, all families

238 of 239 catalog entries have a dispatch arm. The one miss is
`MC_phnCallPlace`, the sole `MC_PHN` entry — placing a real call is inherently
a no-op under emulation, so this is not a practical gap.

| Family | Entries |
|---|---|
| `MC_UIC` | 41 |
| `MC_GRP` | 39 |
| `CSTDLIB` | 31 |
| `MC_KNL` | 30 |
| `MC_MDA` | 21 |
| `MC_FS` | 17 |
| `MC_HTTP` / `MC_NET` | 15 each |
| `MC_DB` | 13 |
| `MC_SRL` / `MC_UTIL` | 6 each |
| `MC_MISC` | 4 |
| `MC_PHN` | 1 |

## 5. Work items

Ranked by demonstrated impact per unit of cost, not by spec mandate. The
distinction matters here: KTF is the runtime with a dense trail of
title-specific evidence in the tree (issues #48, #81, #119 and the named
titles in `ktf_*` comments), so a KTF defect has a measured blast radius. The
SKVM gaps are larger per title but affect an unknown number of them — no
evidence in this repo says any SKT title does float arithmetic or reaches
`javax.microedition.media`, and CLDC-era games routinely used fixed point
precisely because early handsets had no FPU.

The remaining SKVM class-library work and media extensions stay behind the
corpus measurement in §6.

### Completed — KTF media behavior

Sound defects in the runtime that demonstrably has titles, against a media
service that already implements the correct behavior one layer down. No new
subsystem, no spec ambiguity.

Completed in `96212e5`, including focused pause/resume, callback, and host-spec
tests. The bullets below record the implemented design.

- `Player.pause` routes to `Media.Pause` and `Player.resume` routes to
  `Media.Resume`, preserving `clip.position`.
- `PlayListener.playUpdate` is delivered using
  `invokeJavaVirtual(ctx, listener, "playUpdate", "(Lorg/kwis/msp/media/Clip;II)V", clip, event, parm)`,
  queued through the scheduler so callbacks remain serialized against tasks.
- `PlayListener` constants, for the `event` argument:
  `ERROR = -1`, `END_OF_DATA = 1`, `START = 2`, `STOP = 3`, `PAUSE = 4`,
  `RESUME = 5`, `RECORD = 6`, `FULL_OF_DATA = 7`.

This restores the everyday pause-for-menu flow. `setListener` remains
serialized in save state, and completion delivery survives the same lifecycle.

### Completed — numeric wrapper spec entries

Moved up alongside the media work for the same reason: cheapest change on the
list, known fault signature, and it removes the fault from Raptor too.

Implemented in `e441e2d` in `application/internal/ktf/ktf_java_specs.go`, matching the
existing `java/lang/Integer` shape and the descriptors `handleLongMethod`
already answers:

```go
"java/lang/Long": {
    Parent: "java/lang/Object",
    methods: []ktfHostJavaMethodSpec{
        {name: "<init>", descriptor: "(J)V"},
        {name: "longValue", descriptor: "()J"},
        {name: "toString", descriptor: "()Ljava/lang/String;"},
        {name: "parseLong", descriptor: "(Ljava/lang/String;)J", access: 0x0008},
        {name: "parseLong", descriptor: "(Ljava/lang/String;I)J", access: 0x0008},
    },
},
```

`access: 0x0008` is `ACC_STATIC`; instance methods take 0. Both `parseLong`
descriptors are needed — `handleLongMethod` answers the radix form too. No
`fieldSize`: the boxed value lives in the host-side `r.longValues` map keyed by
instance address (`ktf_java_lang.go:210`), not in guest object storage, which is
why `java/lang/Double` also omits it. `java/lang/Integer`'s `fieldSize: 4` is
unrelated to where its value lives (`r.integerValues`), so don't copy it here
without checking what reads that layout.

`Float` and `Double` received their complete implemented method lists in the
same change, with a table-driven resolution test for all three wrappers.

### Completed — SKVM float/double bytecode

Completed in `1775cd4`. The locally available corpus had no SKVM/SKT entries,
but the original audit requested the full opcode set, so the implementation
was completed and verified independently of corpus demand.

26 opcodes, `skvm/interpreter.go`. Insertion points follow the existing
int/long pattern exactly:

| Opcodes | Model on |
|---|---|
| `fadd`/`fsub`/`fmul`/`fdiv`/`frem` (0x62, 0x66, 0x6a, 0x6e, 0x72) | the int group at `interpreter.go:602` |
| `dadd`/`dsub`/`dmul`/`ddiv`/`drem` (0x63, 0x67, 0x6b, 0x6f, 0x73) | the long group at `:647`, using `pop2` |
| `fneg`/`dneg` (0x76, 0x77) | the existing `ineg`/`lneg` (0x74, 0x75) |
| conversions (0x86, 0x87, 0x89, 0x8a, 0x8b–0x90) | `i2l` at `:752` and `l2i` at `:758` |
| `fcmpl`/`fcmpg`/`dcmpl`/`dcmpg` (0x95–0x98) | `lcmp` at `:786` |

Three correctness traps, all of which need tests:

1. **`f2i`/`f2l`/`d2i`/`d2l` saturation.** The JVM requires NaN → 0, round
   toward zero, and clamping to `MinInt32`/`MaxInt32` (resp. 64-bit) on
   overflow. Go's `int32(someFloat32)` conversion is *implementation-defined*
   for out-of-range values, so it cannot be used directly. Write the clamp
   explicitly.
2. **`fcmpl` vs `fcmpg`.** They differ only on NaN: `fcmpl` pushes -1,
   `fcmpg` pushes 1. Getting this backwards silently inverts comparisons
   involving NaN.
3. **`frem`/`drem`** are C `fmod`, not IEEE remainder. Go's `math.Mod` matches;
   `math.Remainder` does not.

Determinism holds — IEEE-754 `float32`/`float64` arithmetic in Go is
reproducible across the required targets — but `runtime/`-style golden tests are
still the right guard.

With this landed, SKVM `java/lang/Float`, `Double`, and the float/double `Math`
overloads are now actionable follow-up class-library work.

### Completed — required `Math` gaps

KTF now declares `ceil`/`floor`/`toDegrees`/`toRadians` in the spec entry and
implements `sqrt`/`sin`/`cos`/`tan` plus the `J|F|D` overloads of
`abs`/`min`/`max`. The handler returns an error for unknown methods.

SKVM now implements `min`/`max` for `I`/`J`. The float/double class-library
overloads remain follow-up work now that the supporting bytecodes exist.

### P4 — `javax.microedition.media` for SKVM

Gate this on evidence. Before building `Manager`/`Player`/`PlayerListener`/
`VolumeControl`, check whether the SK-VM corpus actually reaches it — SKT
titles may use `com.skt.m.AudioClip` exclusively, in which case P4 drops below
`lcdui.game`. The measurement is cheap: SKVM already fails loudly, so any title
touching it shows up as `SKVM class ... is unavailable` in triage.

If the corpus does need it, `Manager.createPlayer` maps cleanly onto
`Services.Media.CreateClip` + `Append`, and `VolumeControl` onto
`SetGlobalGain` — the same wiring `natives_skt.go` already does.

### P5 — deferred

- WIPI-C v2.x `MC_mda*` extensions (§2.1). Add to `wipi/catalog_gen.go` only
  for names the `Unimplemented` counters actually show.
- Tone synthesis. Needed by `MC_MDA_TONE_*`, `MH_mdaTonePlay` and MIDP
  `ToneControl`. A square/sine generator over the note table is small work; the
  question is whether any corpus title uses it instead of shipping SMAF.
- Video (`VideoClip`, `Camera`, `StillClip`, the clip-area calls). Large, and
  outside the current application-mode milestone.
- MIDP high-level LCDUI (19 classes). Only if the corpus needs it; games
  usually live on `Canvas`.
- KTF WIPI-C media slots 1, 2, 12, 14, 15 — recover identities from reference
  evidence first.

## 6. Corpus gate results

The measurement ran against `aram-test/corpus/dubigame-202403`, which holds
218 KTF, 72 Raptor and 22 SKT titles. The earlier 1,005-result report used
here counted 694 KTF and 172 Raptor profiles; its lack of SKVM rows reflects
what that run probed, not the corpus, which does carry SKT titles. The
measurement used the existing instruments:

| Runtime | Instrument |
|---|---|
| WIPI-C | `wipi.Runtime.Unimplemented` — every missed call by name |
| Raptor | `raptor.Public.Unimplemented` — same, interned per import |
| SKVM | fails loudly; the class name is in the error text, and unsupported opcodes carry class, method and PC (`UnsupportedOpcodeError`) |

Results against the available `aram-test` corpus:

1. All 22 SKT titles run 600 headless frames on the pre-opcode build without
   raising `UnsupportedOpcodeError`, so no local title demands the 0x62-0x98
   range. The opcode work was completed anyway, as the original audit asked.
2. No SKT title's jar references `javax/microedition/media/*` or
   `javax/microedition/lcdui/game/*` at all, so P4 stays gated on demand that
   the local corpus does not show.
3. No v2.x `MC_mda*` or tone call was observed in the available results, so
   those P5 items remain deferred.

Future corpus additions should rerun the same measurement before promoting P4
or P5. A newly observed hard fault or unimplemented-call counter is direct
evidence to reprioritize the corresponding item.
