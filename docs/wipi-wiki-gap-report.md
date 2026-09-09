# WIPI wiki implementation gap report

Audited 2026-09-09 against
[`mirusu400/wipi-wiki`'s `llms.txt`](https://mirusu400.github.io/wipi-wiki/llms.txt)
and the linked WIPI v1.2.1, v2.0, v2.2, CLDC 1.1, and MIDP 2.0 API
documents. The implementation snapshot is aram-core commit `e024599`.

This report answers a different question from
[`api-coverage-audit.md`](api-coverage-audit.md): not whether every symbol in an
aram-core dispatch catalog has a handler, but which documented APIs are absent
or behave differently from the wiki.

## Executive summary

The largest discrepancy is Java. KTF registers all 149 v1.2.1 documented class
names, but that is class-presence coverage, not method or behavioral
conformance. Many entries are parent-only declarations whose methods resolve
lazily, and several resolved methods are compatibility stubs.

| Surface | Result | Interpretation |
|---|---:|---|
| KTF WIPI Java v1.2.1 classes | 149 / 149 names | Does not establish method or semantic coverage |
| KTF WIPI Java v1.2.1 documented members | 1,302 summary entries | Only 469 direct method declarations exist for those classes; dynamic handlers cover part of the difference |
| KTF WIPI Java v2.x | Major packages absent | Address book, generic I/O, GPS, SMS, terminal resources, graphics, and media extensions |
| SKVM CLDC 1.1 | 25 real class gaps | 56 of 81 classes are host classes; static-only classes and interfaces account for the raw/effective difference |
| SKVM MIDP 2.0 | 52 real class gaps | Only a small LCDUI/RMS subset and carrier APIs are implemented |
| WIPI-C v1.2.1 | 8 exact wiki names absent | Five InputMethod APIs and three naming divergences |
| WIPI-C v2.0 | 113 documented names absent | 88 when the optional VGI family is excluded |
| WIPI-C v2.2 | 104 documented names absent | 79 when the optional VGI family is excluded |

The `1,302` versus `469` Java member counts are a triage signal, not an
implementation percentage. KTF has shared and lazy native dispatch, so some
methods absent from a class declaration still execute. Conversely, a method
that resolves may return a fixed value or do nothing. Compatibility must
therefore be measured at three separate levels: class presence, method
resolution, and behavior.

## Scope and method

The wiki inputs used for this snapshot were:

| Input | Bytes | SHA-256 |
|---|---:|---|
| `llms.txt` | 95,899 | `6a21b8bb5f7a2f3383c54257bbb5c27a4c233039dff29506d2f6d6dfcce5f53e` |
| [WIPI v1.2.1 full text](https://mirusu400.github.io/wipi-wiki/llms-full.txt) | 5,507,250 | `98fb299de5978ca92ad8b397da41f05c61b049efb920f5cefc5de21d0ef537f8` |
| [WIPI v2.0 full text](https://mirusu400.github.io/wipi-wiki/llms-v20-full.txt) | 1,190,283 | `6858fa025b664e66d60adf4114d5653d01c90039f8b97bbe2c1b61b26caf88fd` |
| [WIPI v2.2 full text](https://mirusu400.github.io/wipi-wiki/llms-v22-full.txt) | 1,607,686 | `d6820ecb0554a0fa4c7ec9eb077750c496e2a4f1fab758c392290701b9451235` |

The comparison used:

- Java class and member summaries extracted from the wiki pages;
- `HostJavaClassSpecs`, parent relationships, and native dispatchers for KTF;
- `defaultHostSupers`, registered natives, and interfaces for SKVM;
- documented `MC_*` headings versus `wipi/catalog_gen.go` for WIPI-C;
- direct source review where a present method could still be a stub.

WIPI v2.x gaps are reported separately. aram-core currently behaves as a
v1.2.1-oriented application runtime; v2.x absence is a version-scope gap, not
necessarily a regression against the current target.

## 1. KTF WIPI Java v1.2.1

### 1.1 Class count overstates coverage

`HostJavaClassSpecs` contains all 149 documented v1.2.1 class/interface names
plus two project-specific entries. However, a large tail of the table is
explicitly labeled `Parent-only declarations`; their methods are expected to
resolve through shared handlers. A mechanical method-name comparison flags at
least one non-declared documented member on 136 of 149 classes. This is useful
for finding review targets but cannot by itself distinguish a lazy
implementation from a missing method.

Any future coverage score should therefore report all of the following:

1. class can be loaded;
2. exact name and descriptor resolve;
3. method has a non-stub implementation;
4. observable behavior matches the documented contract.

### 1.2 Core `java.lang` and `java.io` gaps

#### `java.lang.String`

The current handler does not implement these documented overloads:

- `String(StringBuffer)`;
- `startsWith(String, int)`;
- `valueOf(float)`;
- `valueOf(double)`.

#### `java.lang.StringBuffer`

The handler is missing these overload groups:

- `append(char[])`, `append(float)`, and `append(double)`;
- `insert(char[])`, `insert(long)`, `insert(float)`, and `insert(double)`.

#### `java.io.PrintStream`

The class resolves, but the generic dispatcher returns success without
distinguishing `print`, `println`, `write`, `flush`, or `checkError`. Guest
output and error state are therefore not faithfully observable.

#### `java.lang.System`

`System.getProperty` returns an empty string for every key rather than reading
the runtime/device property model.

#### Wrapper parsing edge behavior

`Byte.parseByte` and `Long.parseLong` return zero for invalid radix or invalid
input instead of throwing `NumberFormatException`. Float/double wrapper
comparison and narrowing paths also need JVM edge-case tests for NaN payloads,
infinities, signed zero, and saturation.

### 1.3 Runtime and event behavior

#### `java.lang.Class`

`isArray()` and `isInterface()` currently return `false` unconditionally.

#### `EventQueue`

- `getNextEvent` clears the supplied buffer instead of waiting for or
  retrieving a queued event;
- `dispatchEvent` is a no-op;
- `postEvent` drops the event;
- `hookEvent` is a no-op.

Card input has a separate delivery path, so some applications still receive
keys, but this does not implement the documented queue semantics.

#### `InputMethodHandler`

The input-method handler mostly returns fixed defaults or absorbs calls. This
aligns with the five missing WIPI-C InputMethod APIs described later and is a
cross-runtime functional gap rather than an isolated missing symbol.

#### Calendar and timezone

Calendar calculation is usable, but timezone behavior is effectively GMT/UTC
only. Setting a timezone is absorbed and timezone setters do not establish a
real zone model.

### 1.4 LWC UI

LWC maintains useful widget state, but several important operations are
explicit compatibility no-ops:

- `paint`, `paintContent`, and `paintFrame`;
- frame/inset controls;
- `setLayout`;
- key grab/ungrab;
- `setParameter`.

Some traversal methods return `null`, and an unknown LWC signature is recorded
in `UnimplementedJava`. This means a title may construct widgets successfully
while still missing documented layout, rendering, focus, or callback behavior.

### 1.5 Media

The following paths are implemented or materially modeled:

- clip buffer creation, clear, put, and get;
- play, stop, pause, and resume;
- volume and listener storage;
- content type;
- playback-position seek;
- completion callback delivery.

The remaining documented methods include compatibility stubs:

- `playUpdate` and `recordStart` return a fixed false/zero result;
- `getPlayerID`, `mediaFreeze`, `mediaReadData`, `mediaWriteData`, and
  `control` return failure;
- atomic update methods do nothing;
- recording is unsupported.

### 1.6 Telephony and networking

`Call` operations are accepted but do not model a call state machine. Socket
access is an intentional offline model: input reaches EOF, output is discarded,
and HTTP-style accessors report `503 Service Unavailable`. This may be the
correct sandbox policy, but it should be documented as unsupported external
I/O rather than counted as API conformance.

## 2. WIPI Java v2.0 and v2.2

The v2 Java indexes add substantial API surface that has no corresponding KTF
class specification or implementation.

### 2.1 Entirely absent feature groups

| Feature | Representative absent types |
|---|---|
| Address book | `Address`, `AddressBook` |
| Generic I/O | `IODevice` |
| Graphics | `AnimateImage` |
| Location/GPS | `StationLocationInfo`, `GPSConfig`, `GPSLocationInfo`, `GPSProvider`, `GPSException`, `GPSListener` |
| SMS | `SMSMessage`, `SMS` |
| Terminal resources | `ResourceGroup` |
| Media | `PlayerListener`, media `UnavailableException`, `Camera`, `StillClip`, `VideoClip` |

Address book, GPS, SMS, camera, and external I/O require device-policy
decisions. They can initially be implemented as explicit, deterministic
unsupported services, but silently returning success would hide application
control-flow errors.

### 2.2 `BaseClip` is only an alias, not the v2 hierarchy

The native dispatcher routes `BaseClip` and `Clip` names to the same handler,
but `HostJavaClassSpecs` has no actual `BaseClip` entry. The current `Clip`
inherits directly from `Object`, and player descriptors use `Clip`, not the v2
`BaseClip` hierarchy. The implementation also uses the v1 `PlayListener`, not
the v2 `PlayerListener`.

Consequently, the following v2 behavior is not present:

- player allocation/freeing;
- device, mode, information, and generic media controls;
- watermark buffer operations;
- v2 listener descriptors;
- video clip-area operations;
- mute and default-volume extensions.

## 3. SKVM CLDC/MIDP gaps

SKVM is a separate Java runtime and should not inherit KTF's coverage score.
Its missing classes also fail more strongly: allocating an unavailable class
or resolving a missing direct method returns an error and can stop the guest.

### 3.1 CLDC 1.1

There are 25 effective class gaps after excluding static-only classes and
interfaces that do not require allocation:

- wrappers: `Boolean`, `Character`, `Short`, `Float`, `Double`;
- references: `Reference`, `WeakReference`;
- character streams: `Reader`, `Writer`, `OutputStreamWriter`;
- fifteen documented exception/error classes, including `EOFException`,
  `ClassNotFoundException`, `NoClassDefFoundError`, `OutOfMemoryError`, and
  `SecurityException`.

`java.lang.Math` also exposes only a small subset compared with CLDC. Missing
`min` and `max` overloads are particularly likely to appear in compiled game
code.

### 3.2 MIDP 2.0

There are 52 effective class gaps. The highest-impact groups are:

- all eight `javax.microedition.media` and media-control types;
- game API: `GameCanvas`, `Sprite`, `TiledLayer`, `Layer`, `LayerManager`;
- high-level LCDUI such as `Form`, `List`, `TextBox`, `TextField`, `Alert`,
  `ChoiceGroup`, `Command`, `Gauge`, and `Item`;
- secure/server/UDP networking types and `PushRegistry`;
- RMS enumeration, filters, listeners, and specific exceptions;
- PKI types and `MIDletStateChangeException`.

`TextField` constants have native/static registration, but that does not
provide an allocatable `TextField` host class.

The private SKT corpus currently uses proprietary `com.skt.m.AudioClip`, which
is implemented, so absence of MIDP media did not prevent the existing lifecycle
smoke test. It remains a standards gap and will fail packages that use
`Manager.createPlayer` directly.

## 4. WIPI-C differences

### 4.1 v1.2.1 exact names

The current catalog has 239 total entries: 208 `MC_*` names and 31 C-library
entries. The v1.2.1 wiki has 211 distinct `MC_*` function headings. Exact set
comparison finds these missing names:

| Wiki name | Current state |
|---|---|
| `MC_imGetCurrentMode` | Absent |
| `MC_imGetSupportedModes` | Absent |
| `MC_imGetSurpportModeCount` | Absent; wiki spelling retained |
| `MC_imHandleInput` | Absent |
| `MC_imSetCurrentMode` | Absent |
| `MC_grpFillPolygon` | Implemented/cataloged as `MC_grpDrawFillPolygon` |
| `MC_knlDestroyShareBuf` | Implemented/cataloged as `MC_knlDestroySharedBuf` |
| `MC_knlGeAppManagerID` | Implemented/cataloged as `MC_knlGetAppManagerID` |

The current catalog additionally contains `MC_knlDefTimer` and `MC_mdaRecord`,
which are not headings in the v1.2.1 wiki comparison set.

This explains why internal catalog coverage can report `239/239` while the
standard-name comparison is not complete.

### 4.2 v2.x additions

Mechanical comparison finds 113 absent v2.0 names and 104 absent v2.2 names.
The optional 25-function VGI family accounts for part of both totals.

The main non-VGI missing groups are:

- terminal-resource APIs;
- generic I/O;
- SSL;
- GPS/location and SMS;
- kernel extensions;
- media allocation/control/video extensions;
- math extensions;
- InputMethod and graphics additions.

These should not all be added speculatively. Import/reference scans of the
actual application corpus should determine which versioned APIs need a real
implementation first.

## 5. Other behavioral gaps

The shared media service decodes SMAF/MMF, Standard MIDI, and PCM WAV. It does
not decode MP3, AMR, QCP/QCELP, AAC, tone sequences, or video. This affects all
runtimes that share the service and is more significant than the presence of a
media method name.

Direct `MH_*` HAL symbols are not scored one-for-one in this report. Application
mode models services above that boundary, while system mode runs firmware
against modeled hardware. Cellular/RF, camera, Bluetooth, and similar hardware
need separate system-mode conformance work.

## 6. Drift in the existing audit

The existing coverage audit predates the implementation snapshot used here:

- `MC_phnCallPlace` is now implemented, so the old `238/239` catalog statement
  is stale;
- media `setPosition` now performs seek rather than acting as a no-op;
- `149/149` KTF classes remains true only as a class-name statement and should
  not appear as an unqualified Java implementation score.

The old audit remains useful as a work history, but future updates should use
this report's three-level class/method/behavior distinction.

## 7. Recommended implementation order

1. Replace headline class counts with a generated class + descriptor manifest.
   Mark methods as implemented, deterministic-unsupported, or stubbed.
2. Close v1.2.1 core Java gaps first: `String`, `StringBuffer`, `PrintStream`,
   `System.getProperty`, `Class`, wrapper parse errors, and timezone behavior.
3. Implement or explicitly reject the event/input paths: `EventQueue`,
   `InputMethodHandler`, LWC paint/layout/focus callbacks.
4. If v2 is a target, introduce the real `BaseClip` hierarchy and
   `PlayerListener`, then add generic I/O and terminal resources. Model
   address book, GPS, SMS, and camera behind explicit device services.
5. For SKVM, prioritize `Math`, wrapper classes, `GameCanvas`/game layers, and
   MIDP media according to corpus references.
6. Add the five v1.2.1 InputMethod C symbols and compatibility aliases for the
   three exact-name divergences before attempting broad v2 C coverage.

## Source landmarks

- KTF Java class specifications:
  [`ktf_java_specs.go`](../application/internal/ktf/ktf_java_specs.go)
- KTF core native dispatch:
  [`ktf_java_native.go`](../application/internal/ktf/ktf_java_native.go)
- KTF strings and buffers:
  [`ktf_java_string.go`](../application/internal/ktf/ktf_java_string.go)
- KTF events and input method:
  [`ktf_java_jlet.go`](../application/internal/ktf/ktf_java_jlet.go)
- KTF LWC:
  [`ktf_lwc.go`](../application/internal/ktf/ktf_lwc.go)
- KTF media:
  [`ktf_java_media.go`](../application/internal/ktf/ktf_java_media.go)
- KTF networking:
  [`ktf_java_msf.go`](../application/internal/ktf/ktf_java_msf.go)
- SKVM host class table and hard-failure path:
  [`vm.go`](../skvm/vm.go)
- SKVM native registration:
  [`natives_install.go`](../skvm/natives_install.go)
- WIPI-C catalog:
  [`catalog_gen.go`](../wipi/catalog_gen.go)
- WIPI-C application handlers:
  [`application/internal/wipi`](../application/internal/wipi/)
