# Shared runtime services for WIPI-C and SKVM

## Purpose

ARAM needs to run more than one carrier runtime:

- KTF's mixed native WIPI-C and Java environment;
- LGT/LGTP WIPI-C applications;
- SK Telecom SKVM Java applications;
- other native WIPI-C containers that use the public API with a different
  bootstrap or provider ABI.

These runtimes should share device behavior without sharing carrier-private
binary layouts. This document defines that boundary and the migration rules
for extracting reusable services from the current runtimes.

The target is not one universal guest runtime. The target is a set of
deterministic, serializable host services with small carrier or VM adapters.

## Terminology

| Term | Meaning |
|---|---|
| execution engine | ARM `cpu.Backend`, the SKVM bytecode interpreter, or another bounded guest executor |
| runtime adapter | KTF, LGT/LGTP, Raptor, or SKVM code that translates guest ABI/API operations into service requests |
| service | guest-neutral state and behavior such as surfaces, files, timers, or audio clips |
| device profile | layered standard, carrier, manufacturer, model, and title configuration |
| frontend | presentation, host input, document selection, permissions, and product UI outside `aram-core` |

An adapter may expose WIPI-C function tables, Java native methods, private
object layouts, or carrier callbacks. A service must not expose any of those
representations.

## Design rules

1. **Services do not know guest addresses.**
   A service uses typed values and stable service identifiers. The adapter
   validates guest memory, copies or views the required data, and translates
   the result back into the guest representation.

2. **Guest-visible layouts stay with the adapter.**
   KTF framebuffer and image wrapper layouts, an LGT provider structure, and
   an SKVM Java object are different representations of the same service
   object. They are not shared structs.

3. **Determinism is part of every service contract.**
   Time, random values, event ordering, storage, network responses, media
   completion, and scheduling decisions must not depend on host wall-clock
   timing.

4. **Serializable state is implemented with the service.**
   A service is not considered extracted until its live state can be saved,
   validated, and restored. Save-state support must not be postponed until
   after all runtimes are migrated.

5. **Profiles describe differences; code implements semantics.**
   Screen formats, key maps, properties, capabilities, limits, and documented
   quirks belong in layered `profile` data. Title behavior must remain
   SHA-256 keyed.

6. **Host side effects stay behind explicit providers.**
   Core tests must not require a display, audio device, network, mobile
   permission, or native library. The default providers are deterministic and
   headless.

7. **Trace collection is observational.**
   Enabling or disabling trace collection must not change guest-visible
   behavior, scheduling, allocation addresses, or event ordering.

8. **Untrusted inputs remain bounded.**
   Package expansion, guest copies, decoded assets, storage, trace buffers,
   and save-state metadata require explicit size and count limits.

## Target architecture

```text
core.Machine and frontend-facing contracts
                    |
            Runtime coordinator
       lifecycle, execution budget, owner
                    |
     +--------------+----------------+
     |       shared runtime services |
     |                               |
     |  Surface / Raster / Present   |
     |  Assets / Images / Text       |
     |  Input / Events               |
     |  Clock / RNG / Timers         |
     |  VFS / Resources / DB         |
     |  Audio / Device / Network     |
     |  Trace / Snapshot / Replay    |
     +--------------+----------------+
                    |
     +--------------+----------------+
     |              |                |
 KTF adapter   LGT/LGTP adapter   SKVM adapter
 WIPI-C+Java      WIPI-C ABI       Java natives
     |              |                |
 ARM backend     ARM backend      bytecode VM
     |              |                |
 KTF loader      LGT loader       MSD/MOD/WMR/JAR
```

The runtime coordinator owns application lifecycle and determines which
adapter may execute or present. Adapters own execution contexts and invoke
services. Services own reusable device state.

## Current ownership and duplication

The repository already contains useful parts of this design:

| Current owner | Existing responsibility | Extraction concern |
|---|---|---|
| `core.Machine` | lifecycle, input, framebuffer, audio, save/load contract | keep as the stable frontend boundary |
| `profile` | standard/carrier/OEM/device/title layers, screen and key data | extend with capabilities and service limits |
| public `application.wipiRuntime` | WIPI-C graphics, files, DB, media, network, timers, programs, UIC | service behavior is coupled to CPU memory and WIPI dispatch |
| `application.ktfRuntime` | KTF CPU tasks, Java bridge, plus another graphics/files/media/device implementation | keep KTF ABI and task bridge; extract duplicated device behavior |
| `application.raptorRuntime` | native container bootstrap using the public WIPI runtime | useful example of an adapter reusing standard services |
| `skvm.VM` | Java heap and frames plus screen, resources, RMS, properties, device methods | keep Java execution; move reusable device behavior behind an adapter |

SKVM package loading and bytecode execution already exist, but SKVM is not yet
integrated into `application.Factory`. That integration should use the shared
services rather than copying `application.Machine` behavior into `skvm.VM`.

## Service catalogue

### 1. Runtime coordinator and lifecycle

The coordinator owns:

- load, start, pause, resume, background, foreground, destroy, stop, and reset;
- the active display or presentation owner;
- execution budgets and transitions to paused, stopped, or faulted state;
- delivery policy for input and asynchronous events while backgrounded;
- global cleanup ordering when an application exits;
- the connection between an adapter and `core.Machine`.

The adapter owns:

- the KTF Clet/Jlet/Card lifecycle sequence;
- LGT/LGTP entry points and provider initialization;
- SKVM MIDlet/SKTP main-class construction and Java callbacks;
- carrier-specific exit codes and lifecycle callback ABIs.

### 2. Guest memory, heaps, and handles

Native runtimes require guest memory allocation, while SKVM requires Java
objects and references. Those representations must remain engine-specific,
but their service-object ownership rules can be shared.

Common behavior:

- stable service IDs for surfaces, images, fonts, files, clips, and timers;
- ownership, retain/release, destruction, and use-after-destroy validation;
- resource limits and deterministic ID allocation;
- save-state validation of object graphs and references.

Adapter behavior:

- guest heap addresses and allocation headers;
- WIPI memory IDs and indirect-buffer layouts;
- carrier-private nested structs and vtables;
- Java object references and native payload attachment;
- argument and return-value marshalling.

Service IDs must not be guest pointers. An adapter may maintain a mapping from
its guest handle or Java reference to a service ID.

### 3. Surfaces, raster operations, and presentation

The graphics service owns:

- surface creation and destruction;
- width, height, stride, pixel format, color masks, and optional palette;
- clipping, translation, raster operation, transparency, and alpha state;
- pixel, line, rectangle, arc, polygon, copy, blit, and scaled-blit semantics;
- dirty-region tracking;
- present sequence numbers and immutable frame snapshots;
- conversion from a guest-visible pixel format to frontend RGBA.

A surface that is directly visible in guest memory must preserve its declared
pixel layout. Converting every surface to RGBA at creation time would break
native applications that read or write framebuffer bytes directly. RGBA is
the presentation format, not necessarily the storage format.

Presentation identity is part of the contract because a driver polls the
machine once per host tick whether or not the guest drew anything.
`Graphics.LastFramePresentation` reports the committed frame's sequence and
content hash without copying the surface, and `Machine.FramePresentation`
republishes the same immutable image for as long as the content is unchanged.
`Machine.Framebuffer` still copies on every call and stays the API for a
caller that needs to own or mutate the pixels.

The adapter owns:

- `MC_GrpFrameBuffer` object layout;
- screen and offscreen framebuffer handles;
- direct guest-memory mapping;
- WIPI graphics context struct decoding;
- Java `Graphics` and `Image` object creation;
- carrier-specific flush and repaint entry points.

PNG screenshot encoding does not belong in the service. Core should expose a
frame snapshot, sequence number, dirty region, and optional frame hash.
`aram-frontend`, test tools, or debugging commands may encode PNG files.

### 4. Images, animation, and fonts

The asset service owns:

- bounded decoding of BMP, PNG, GIF, JPEG, and SKVM LBMP;
- indexed palettes, masks, transparency, and alpha;
- animated-image frames, loop metadata, and frame timing;
- decoded-asset caches keyed by content hash and decode options;
- image encoding where required by a public API;
- font faces, sizes, styles, metrics, glyph lookup, and glyph caches;
- deterministic fallback glyphs when an authorized device font is absent.

Text handling is a separate layer over font rasterization:

- UTF-8, UTF-16, and required Korean legacy encoding conversion;
- string measurement;
- baseline, ascent, descent, anchor, alignment, and line breaking;
- locale and fallback policy.

The adapter owns image and font handles, property indices, object fields,
string memory layouts, Java strings, and callback entry points.

Proprietary device fonts and extracted game assets must not be committed.
Font sources require an explicit licensing policy.

### 5. Input state and the event bus

Input is one source of events, not a standalone callback mechanism.

The shared input service owns:

- normalized frontend control names;
- physical press, release, and repeat state;
- deterministic timestamps;
- held-key state for polling APIs;
- repeat delay and repeat interval;
- focus and lifecycle gating;
- event queue limits and ordering.

The shared event bus also carries:

- timer expiry;
- repaint and UI events;
- audio completion;
- network readiness and response delivery;
- storage or serial completion where an API is asynchronous;
- lifecycle transitions;
- application-posted events.

The adapter converts a common event into KTF `keyNotify`, an LGT callback, an
SKVM `keyPressed` method, or the relevant private carrier operation. Numeric
key codes and Java method calls do not belong in the shared queue.

### 6. Virtual time, wall time, and random numbers

The clock service distinguishes:

- monotonic execution time used for timers and media;
- wall time used by date and calendar APIs;
- timezone and locale;
- the epoch and unit expected by each adapter.

Time advances only through an explicit coordinator operation. A frontend frame
rate must not accidentally change guest time.

The random service owns one or more seeded streams. Its seed, stream position,
and any per-runtime compatibility policy are save-state data. Java
`Random`, C `rand`, and carrier random APIs may use separate streams while
sharing the same deterministic implementation.

### 7. Scheduler, timers, and callbacks

The common scheduling layer owns:

- ordered deadlines;
- timer definition, activation, cancellation, and reuse;
- deferred callback queues;
- event ordering when several deadlines are equal;
- execution budgets and starvation limits;
- deterministic callback sequence numbers.

Execution continuations remain engine-specific:

- KTF native tasks preserve CPU registers and stacks;
- KTF Java calls preserve bridge and exception state;
- SKVM threads preserve Java frames, monitors, and pending exceptions;
- an LGT native runtime may use a different cooperative scheduler.

The scheduler selects a runnable adapter continuation; it does not attempt to
represent every continuation in one common struct. Reentrant callbacks must
pass through an adapter-controlled invocation gate.

### 8. Resources, VFS, persistent files, and databases

The storage service provides explicit namespaces:

| Namespace | Expected behavior |
|---|---|
| package resources | read-only bytes mounted from the validated package |
| private storage | application-specific persistent files |
| shared storage | profile- and permission-controlled shared files |
| temporary storage | deterministic runtime-only data |

It owns:

- normalized service paths independent of host paths;
- open handles, positions, access modes, metadata, directories, and quotas;
- deterministic timestamps from the virtual clock;
- atomic persistence import and export;
- database records and stable record identifiers;
- Java RMS and WIPI DB views over an appropriate common record engine;
- shared buffers where public WIPI semantics require them.

The adapter owns path spelling rules, flag values, error-code translation,
file descriptor representation, and Java exception conversion.

Package resources and mutable files must not be mixed in one unqualified map.
No guest path may escape into a host filesystem path.

### 9. Audio and media

The media service owns:

- clip lifecycle, source data, decoded format, position, duration, and state;
- volume, mute, pan if required, loop count, and global mixing;
- deterministic completion based on virtual time;
- PCM16 output collected by `core.Machine.DrainAudio`;
- completion events and bounded queued audio;
- codec capability reporting.

The default headless implementation may model timing without producing host
sound. Host playback belongs to the frontend.

MMF and other proprietary codecs require separate bounded decoders. Unknown
formats remain identified resources rather than being silently treated as
successful audio.

### 10. Device capabilities and system services

The device service resolves profile data for:

- screen geometry and pixel format;
- physical and virtual key maps;
- manufacturer, model, carrier, WIPI version, and system properties;
- memory, storage, socket, and media limits;
- supported codecs and device features;
- volume steps, vibration, backlight, LED, battery, and signal state;
- locale, timezone, phone-number policy, and network availability.

Vibration, backlight, LED, phone, SMS, and browser requests are state changes
or recorded requests in the headless core. A frontend may choose to perform an
authorized host action, but core must not do so implicitly.

### 11. Network, HTTP, serial, and external responses

These services share:

- descriptors and connection state;
- read/write buffers;
- asynchronous readiness events;
- callback registration;
- deterministic mock responses;
- bounded request and response storage;
- save-state rules for in-flight operations.

Real network access is an optional provider. Record/replay must capture all
external responses if a run is expected to be reproducible. Loading a state
with a live host socket is not portable; the service must restore a modeled
connection or reject the state explicitly.

### 12. Trace, metrics, screenshots, snapshots, and replay

These are related but distinct products:

| Facility | Purpose | Semantic state |
|---|---|---|
| trace | structured observation of execution and service calls | no |
| metrics | bounded counters and compatibility coverage | only if exposed to the guest |
| screenshot | evidence from one presented frame | no |
| save state | complete resumable machine state | yes |
| replay | ordered input, time, scheduling, and external-response log | drives state |

The common trace schema should include:

- virtual timestamp and monotonic sequence number;
- runtime/adapter and task identity;
- category and stable event name;
- guest PC or Java class/method/bytecode PC when available;
- service ID and privacy-safe scalar arguments;
- result, error class, and optional bounded diagnostic payload.

Raw game bytes, absolute private paths, secrets, and unbounded memory dumps are
not trace fields. Trace storage uses bounded ring buffers or an explicit
streaming sink.

Each save-state component requires:

- a stable component identifier;
- an independent schema version;
- bounded, explicit-width encoding with explicit byte order;
- validation before mutation;
- deterministic ordering for maps and object tables;
- cross-reference validation;
- source hash, profile ID, and execution-engine compatibility checks.

A save state includes the coordinator, execution engine, adapter, and every
active service. A state is invalid if any required component is absent.

Replay captures:

- normalized input events;
- explicit clock advances;
- random seeds when a run starts;
- external network and device responses;
- any profile or title-quirk selection;
- scheduler choices only when they are not derivable from deterministic state.

## Adapter responsibilities

Every runtime adapter owns the following:

1. package-specific bootstrap after a loader has validated the package;
2. function-table, syscall, native-method, or Java-class registration;
3. ABI argument and return-value marshalling;
4. guest object layouts, vtables, handles, and allocation headers;
5. callback entry, reentrancy, exception, and unwind behavior;
6. translation between service errors and carrier return values or Java
   exceptions;
7. execution continuations and task/thread frame state;
8. carrier-private lifecycle, UIC/LWC, and application-manager behavior;
9. profile and title-quirk selection;
10. adapter-specific trace details and save-state components.

The KTF adapter therefore retains its Java bridge, private framebuffer/image
wrappers, task registers, and exception frames. It should not retain a private
BMP decoder, rasterizer, virtual clock, or persistent-file engine after those
services are shared.

The SKVM adapter retains its class parser, bytecode interpreter, Java heap,
frames, exceptions, monitors, and native method table. It should not retain a
separate screen rasterizer, RMS persistence backend, input clock, or audio
timeline after those services are shared.

## Code that must not be generalized

The following should remain adapter or title data:

- raw KTF, LGT, or SKVM object layouts;
- provider slot numbers and import-table addresses;
- Java field offsets and private carrier class hierarchies;
- one runtime's exception or callback frame layout;
- package encryption keys or proprietary assets;
- title-specific patches without a SHA-256 identity and original-byte check;
- frontend window, mobile Activity, permission, and document-picker code;
- native host audio, socket, or graphics handles.

Duplicated code is not automatically common code. It is common only when the
observable semantics are the same and representation differences can be
handled by an adapter.

## Illustrative service flow

An LGT WIPI-C image creation call should flow as follows:

1. the LGT adapter reads and validates the guest arguments;
2. the adapter resolves a package buffer or copies bounded encoded bytes;
3. the asset service decodes the bytes into a service image;
4. the graphics service creates the required pixel-format surface;
5. the adapter constructs its guest-visible image and framebuffer objects;
6. the adapter maps those guest objects to service IDs;
7. save-state data records both the service objects and the adapter mappings.

An SKVM image creation call follows the same service path but constructs a Java
object reference instead of native guest structs.

## Migration plan

This is an incremental extraction, not a rewrite.

### Phase 0: lock down behavior

- retain current KTF, public WIPI-C, Raptor, and SKVM regression suites;
- add service-level golden tests for formats and semantics before moving code;
- record privacy-safe compatibility deltas for authorized corpora;
- distinguish first-frame smoke results from sustained or interactive results.

### Phase 1: define common value types and state rules

- surface IDs, pixel formats, rectangles, colors, events, deadlines, and
  service errors;
- deterministic ID allocation and ownership validation;
- component versioning for save states;
- profile capability and limit keys.

No runtime dispatch should move in this phase.

### Phase 2: extract graphics, assets, and text

- move bounded image decoders and raster algorithms behind guest-neutral
  inputs;
- preserve guest-visible native surface formats;
- replace KTF, public WIPI-C, and SKVM drawing paths one operation at a time;
- keep adapter object layout tests beside each adapter;
- verify frame hashes and direct framebuffer access.

### Phase 3: extract time, events, input, and timers

- introduce one virtual clock and deterministic event ordering;
- route native callbacks and Java methods through adapter invocation gates;
- preserve KTF task behavior and add real SKVM continuation scheduling;
- serialize queues, held keys, timers, and clock state.

### Phase 4: extract resources, VFS, DB, and RMS

- separate read-only package resources from mutable storage;
- unify persistence and quota handling;
- retain adapter-specific paths, flags, return codes, and exceptions;
- add restart and save/load persistence tests.

### Phase 5: extract media and optional device services

- establish the virtual-time media timeline and PCM drain path;
- add deterministic vibration, backlight, LED, phone, and network providers;
- add real host providers only behind explicit frontend integration.

### Phase 6: integrate runtime adapters

- make KTF use shared services without changing its ABI;
- add the LGT/LGTP loader and adapter, or connect an existing validated
  container path when evidence proves it is the same format;
- connect SKVM to `application.Factory` and `core.Machine`;
- keep probing order unambiguous for ZIP-based KTF, LGT, and SKVM packages.

Every phase must preserve portable pure-Go tests and Android/arm64 builds.

## Verification requirements

A service extraction is complete only when:

- service unit tests run without a CPU backend or Java VM where practical;
- each adapter has marshalling and guest-layout tests;
- existing authorized corpus results have no unexplained regression;
- input, timer, storage, and media ordering are deterministic;
- save, restore, and replay produce the same frame and trace sequence;
- malformed sizes, handles, paths, and state graphs fail before mutation;
- trace-disabled and trace-enabled runs produce the same guest-visible state;
- `go test ./...`, `go vet ./...`, and the Android/arm64 build pass.

Compatibility reports must continue to identify whether a title only loads,
boots, presents a first frame, sustains execution, accepts input, or reaches an
interactive state.

## Open design decisions

The following require evidence before interfaces are frozen:

- exact LGT/LGTP package probing and bootstrap order;
- which native surfaces require direct guest-memory coherence;
- whether service IDs use generations to detect stale handles;
- the canonical Korean legacy encoding policy per device profile;
- licensed font sources and deterministic fallback metrics;
- SKVM thread, monitor, and timer scheduling granularity;
- persistence namespace and quota compatibility across carriers;
- portable treatment of in-flight network operations in save states;
- which WIPI public semantics differ by standard version rather than carrier.

These decisions should be resolved with standard documentation, firmware or
package evidence, synthetic conformance fixtures, and corpus deltas. A result
from one title must not silently define another carrier's behavior.
