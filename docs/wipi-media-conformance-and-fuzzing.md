# WIPI media semantic conformance audit and fuzzing plan

Status: 2026-09-09

This document audits ARAM's media behavior against the WIPI references and
defines a dynamic test strategy for the sound failures seen in real titles.
It is the semantic companion to [the API coverage audit](api-coverage-audit.md):
that audit asks whether a symbol can be resolved, while this one asks whether
the resolved operation has the required state transition, callback, audio
output, and lifetime behavior.

## References and scope

The standard references are:

- [WIPI Wiki index](https://mirusu400.github.io/wipi-wiki/llms.txt), covering
  WIPI 1.2.1, 2.0, and 2.2.0;
- [WIPI 1.2.1 C media API](https://mirusu400.github.io/wipi-wiki/c-api/media.md);
- [WIPI 1.2.1 Java `Clip`](https://mirusu400.github.io/wipi-wiki/java-api/org/kwis/msp/media/Clip.md),
  [`Player`](https://mirusu400.github.io/wipi-wiki/java-api/org/kwis/msp/media/Player.md),
  and [`PlayListener`](https://mirusu400.github.io/wipi-wiki/java-api/org/kwis/msp/media/PlayListener.md);
- [WIPI 2.0 C media API](https://mirusu400.github.io/wipi-wiki/v20/c-api/media.md)
  and [Java media API](https://mirusu400.github.io/wipi-wiki/v20/java-api/media.md);
- [WIPI 2.2 C media API](https://mirusu400.github.io/wipi-wiki/v22/c-api/media.md)
  and [Java media API](https://mirusu400.github.io/wipi-wiki/v22/java-api/media.md);
- the MIDP media index linked by `llms.txt` for SKVM's
  `javax.microedition.media` surface.

The implementation surfaces inspected are:

- the shared mixer and timeline in `runtime/media.go`;
- the frontend-facing PCM publication path in
  `application/audio_presentation.go`;
- public WIPI-C in `application/internal/wipi/wipi_media.go`;
- KTF Java and WIPI-C in `application/internal/ktf/ktf_java_media.go` and
  `ktf_wipic_media.go`;
- Raptor's private sound imports and WIPI-C bridge;
- SKVM's `com.skt.m.AudioClip` native bridge.

ARAM is a headless runtime rather than a handset HAL implementation. Missing
direct `MH_*` exports are therefore not automatically defects when an
equivalent internal service exists. This audit treats application-visible
behavior and the frontend audio contract as the conformance boundary.

## Executive summary

The dominant failures are semantic rather than missing dispatch entries:

1. Enhanced audio mixing can detach an infinite music voice from its guest
   clip. The voice deliberately survives `Stop`, `Clear`, and `Destroy`, and a
   repeated finite track can be promoted to that infinite voice. This directly
   explains sound that will not stop and tracks that loop unexpectedly.
2. Public WIPI-C and KTF WIPI-C deliver incorrect media status values. Internal
   playback states `0..3` are being used where WIPI callback constants `1..7`
   are required.
3. Invalid state transitions generally report success. Replaying an already
   playing clip can change its repeat count; resuming a stopped clip can make
   adapter state disagree with mixer state.
4. KTF Java only delivers the natural-completion listener event. START, STOP,
   PAUSE, and RESUME notifications are absent, and several methods are
   successful no-op stubs.
5. Clip storage is modeled as immutable source data, not the consumable stream
   required by WIPI. Appending to a sequenced clip after decoding does not
   update the active decoder.
6. Unsupported media can enter `ClipPlaying` forever without output or a
   completion/error event. KTF advertises MP3 even though no MP3 decoder exists.
7. A per-clip stop does not retract PCM already published to the frontend.
   Retention drops are bounded but can be heard as discontinuities when the
   consumer falls behind.

Existing unit tests pass because most of these contracts are not asserted.
Some enhanced-mixing tests explicitly preserve behavior that is incompatible
with normal WIPI stop semantics.

## API and format gaps

The mechanical inventory remains:

| Surface | Current coverage | Important gaps |
|---|---:|---|
| WIPI-C catalog | 238 / 239 | `MC_phnCallPlace` |
| WIPI-C 1.2.1 `MC_MDA` | 21 / 21 symbols | semantic issues below |
| WIPI-C 2.0/2.2 media additions | 0 | player allocation, controls, info, default volume, video areas |
| KTF Java 1.2.1 classes | 149 / 149 classes | method semantics and no-op methods remain |
| Raptor sound | 8 `RAPTOR.snd*` + 12 `MC_mda*` | data/type/seek, pause/resume, record, global volume |
| SKVM CLDC | 56 / 81 host classes | 25 effective class gaps |
| SKVM MIDP | 15 / 67 host classes | 52 effective class gaps |
| SKVM MIDP media | 0 / 8 | entire `javax.microedition.media` surface |

The shared decoder supports SMAF/MMF, SMF/MIDI, and 8/16-bit mono/stereo PCM
WAV. MP3, AMR, QCP/QCELP, AAC, WIPI tone sequences, and video are absent.

WIPI 2.x media calls missing from the public catalog include:

- `MC_mdaClipAllocPlayer`, `MC_mdaClipFreePlayer`;
- `MC_mdaClipControl`, `MC_mdaClipDevControl`,
  `MC_mdaClipModeControl`, `MC_mdaClipGetModeList`, `MC_mdaClipGetInfo`;
- `MC_mdaGetInfo`;
- `MC_mdaGetDefaultVolume`, `MC_mdaSetDefaultVolume`;
- `MC_mdaSetClipArea`, `MC_mdaReleaseClipArea`,
  `MC_mdaUpdateClipArea`.

KTF's WIPI-C media vector also leaves slots 1, 2, 12, 14, and 15 unmapped.
Their identities need reference-firmware evidence rather than guesses.

## Semantic findings

### MDA-001 — a hidden music voice violates clip lifetime and stop semantics

Severity: critical when `AudioMixMode` is enabled.

`Media.playAsBGMVoice` changes the guest clip to `ClipStopped` and creates a
detached `ClipPlaying` voice with `remainingPlays = -1`. That hidden voice is
not a registry clip and cannot be addressed by the guest.

Consequences:

- `Pause` targets the stopped guest clip and the hidden voice keeps playing;
- `Stop` only marks `bgmVoiceStopped`; the voice remains audible until a later
  `Play` of a different source;
- `Clear` and `DestroyClip` do not stop or mark the hidden voice at all;
- after destroy, no guest handle remains that can stop the voice;
- replaying the same finite track within 750 ms of natural completion promotes
  it to an infinite voice when its duration is at least 1.2 seconds;
- `Info` can report `ClipStopped` while audio from that clip remains audible.

The enhanced-mixing tests require a voice to survive clip destruction, so the
behavior is intentional rather than a race. `AudioMixMode` defaults to false
in `application.Factory`, but a composition root can enable it.

There is also a save-state divergence: `bgmVoiceStopped` affects the next
audible decision but is absent from `MediaState`. Saving after `Stop` and
restoring before the next `Play` loses that pending stop decision.

Required correction:

- explicit `Pause`, `Stop`, `Clear`, and `Destroy` must always affect every
  voice derived from the addressed clip;
- an infinite WIPI play should remain a normal registered clip, because the
  mixer already supports concurrent voices;
- hand-loop seam concealment should retain clip ownership and only smooth the
  boundary after natural completion;
- any compatibility behavior that intentionally ignores guest stop must be a
  hash-keyed title quirk, not a global playback mode;
- all audible policy state must round-trip through save state.

### MDA-002 — callback constants are confused with internal states

Severity: critical for titles that drive clip lifetime from callbacks.

WIPI defines the following callback status values:

| Event | WIPI value | Public WIPI-C value today | KTF WIPI-C value today |
|---|---:|---:|---:|
| END_OF_DATA | 1 | 0 | 0 |
| START | 2 | 1 | not delivered |
| STOP | 3 | 0 | not delivered |
| PAUSE | 4 | 2 | not delivered |
| RESUME | 5 | 1 | not delivered |
| RECORD | 6 | 3 | not delivered |
| FULL_OF_DATA | 7 | not implemented | not implemented |

`application/internal/wipi/wipi_media.go` stores `0 = stopped`, `1 = playing`,
`2 = paused`, and `3 = recording`, then forwards those internal values to the
guest callback. Natural completion is separately sent as zero by
`application/machine_wipi.go`. KTF's completion task likewise invokes the
callback with `{handle, 0}`.

These values must be translated at the adapter boundary. Internal enum values
must never double as guest ABI constants.

Raptor is a separate case: reference evidence requires its provider-specific
natural-end code `-1`, while explicit stop uses `3`. That quirk must remain
isolated to the Raptor adapter.

### MDA-003 — transition validity and return values are not enforced

Severity: high.

The shared `Media` methods return success when the requested transition is not
applicable. The adapters then frequently update their own state and enqueue a
callback regardless of whether the mixer changed state.

Examples:

- `Resume(stopped)` leaves the mixer stopped, while public WIPI-C records the
  adapter state as playing and emits a callback;
- `Pause(stopped)` can leave the KTF WIPI-C adapter paused while the mixer is
  stopped;
- `Stop(stopped)` reports success and may emit another stop callback;
- `Play(playing, repeat)` overwrites `remainingPlays`, changing a finite play
  into an infinite loop or an infinite loop into a finite play;
- KTF Java `Player.play`, `stop`, `pause`, and `resume` return `true` even for a
  rejected/no-op transition;
- public WIPI-C `MC_mdaClipSetPosition` ignores a shared-service seek error and
  still reports success.

The WIPI C API specifies error/no-op behavior for duplicate operations, and
the Java API returns `false` for invalid transitions. The shared service should
return a typed transition result so adapters can produce the correct ABI
return and only deliver callbacks for real transitions.

### MDA-004 — KTF Java listener and control behavior is incomplete

Severity: high.

KTF Java now preserves position across pause/resume and delivers
`END_OF_DATA`, but it does not deliver `PlayListener.playUpdate` for START,
STOP, PAUSE, or RESUME.

Additional stubs include:

- `Clip.setPosition` returns true without seeking;
- `playStart` returns true without invoking an override or enforcing its
  result;
- `record`, `recordStart`, media read/write/freeze, and atomic update hooks;
- `control`;
- global `Volume.set`, which resolves but has no effect;
- WIPI 2.x player allocation, watermarks, per-source/default volume, camera,
  still-image, and video classes.

At minimum, lifecycle listener events, `setPosition`, and global volume need
semantic tests. Recording and video can remain explicitly unsupported until a
provider exists, but they must not pretend to succeed.

### MDA-005 — clip buffering does not implement WIPI streaming semantics

Severity: high for streaming titles, otherwise medium.

The WIPI clip buffer is consumable: available bytes decrease as playback uses
them, and a title may replenish it with `putData` before it becomes empty.
ARAM retains the entire encoded source and advances a time position instead.

Consequences:

- `availableDataSize` does not fall as playback progresses;
- buffer watermark behavior cannot be implemented correctly;
- `putData` after SMAF/SMF decoding leaves the active decoded object pointing
  at the pre-append stream;
- `Clear` is accepted during playback or pause even though WIPI requires an
  error;
- `Free` destroys a playing clip instead of returning `M_E_INUSE`;
- adapters contain title-specific rewind/clear heuristics to compensate for
  the missing buffer model, increasing cross-runtime differences.

The source store, decode cursor, and consumable input buffer should be modeled
separately. A non-streaming file clip can use a sealed source fast path.

### MDA-006 — unsupported media can remain playing forever

Severity: high.

`Media.Play` succeeds even when no decoder recognizes the source. During
advance, an undecoded playing clip only increases its position; it never
reaches a duration, produces audio, or emits completion/error.

KTF reports `MEDIADEVICES=audio/MIDI,audio/MP3`, while the KTF WIPI-C create
path rejects MP3 and the shared service has no MP3 decoder. Different adapters
therefore fail the same advertised type differently: creation failure, silent
permanent playback, or immediate close in the SKVM bridge.

Required correction:

- capability properties must be generated from enabled decoders and adapter
  support;
- a sealed unsupported source must fail `Play` with a typed unsupported-format
  result;
- a malformed source must emit an error transition rather than becoming an
  immortal playing clip;
- a true streaming clip needs an explicit "waiting for more data" state with a
  bounded starvation policy, distinct from playing decoded media.

### MDA-007 — clip stop does not cancel already published PCM

Severity: medium to high, depending on frontend buffering.

`Media.Stop` prevents future mixing but does not remove samples already in
`Media.queuedPCM16` or `Machine.publishedAudio`. The application publication
layer retains up to 500 ms and only advances audio generation for machine
lifecycle discontinuities, reset/load-state, close, and mix-mode changes.
Guest `MC_mdaStop` and Java `Player.stop` do not signal a generation change.

This creates two observable failure modes:

- stop latency from core and host-device buffers;
- discontinuities when a slow frontend exceeds retention and older PCM is
  discarded.

The existing debug snapshot exposes `PublishedDropped`,
`MediaDroppedSamples`, queued chunks, and queued samples. Corpus runs should
treat unexpected nonzero drop counters as failures, not merely diagnostics.

A global stop/mute can use a generation discontinuity and host flush. A
per-clip stop cannot unmix one source from already mixed PCM, so ARAM should
bound render-ahead tightly and define a maximum stop latency. If exact
per-source cancellation is required, the frontend boundary needs voice-tagged
buffers rather than only premixed PCM.

### MDA-008 — SKVM uses a different frontend timeline contract

Severity: integration risk; not yet proven as a title defect.

The application WIPI/KTF path publishes `StartGuestNS`, `StartSample`, and
`Generation`. `application/internal/skvmhost.Machine.DrainAudio` returns PCM
without those fields. A frontend that relies on the timestamped contract
cannot apply the same gap, overlap, reset, and stale-buffer rules to SKVM.

SKVM should either use the common publication helper or document and test a
separate contract. The preferred outcome is one frontend audio contract for
all runtimes.

## Stateful fuzzing design

Byte-level decoder fuzzing remains useful for panics, excessive allocation,
and malformed input, but it will not find most lifecycle bugs. The primary
test should be a model-based stateful fuzzer.

### Abstract command stream

A fuzz input decodes into a bounded sequence of operations:

```text
Create(type, capacity)
Append(clip, source fragment)
PlayOnce(clip)
PlayLoop(clip)
Pause(clip)
Resume(clip)
Stop(clip)
Seek(clip, milliseconds)
SetClipVolume(clip, volume)
SetGlobalVolume(volume)
Clear(clip)
Destroy(clip)
Advance(duration)
Drain
Snapshot
Restore
```

Use a small collection of synthetic, distinguishable sources:

- one-cycle mono and stereo WAV ramps;
- WAV at the output rate and at lower/higher rates;
- short and long SMAF/SMF fixtures;
- truncated and structurally invalid variants;
- an unsupported but well-formed marker fixture;
- two sources with clearly different constant amplitudes for mix attribution.

Keep the command count, source bytes, clip count, total virtual time, and event
queue bounded so every input completes quickly and can be minimized.

### Reference model

The model tracks, per clip:

```text
allocation: free | allocated
buffer: empty | partial | sealed | waiting
playback: stopped | playing | paused | recording | error
position: exact output-frame cursor
repeat: finite remaining count | infinite
volume/mute/pan
expected callbacks
```

The model should not synthesize SMAF. It only needs known fixture durations and
expected frame sequences. This keeps the oracle independent of the decoder
implementation.

### Required invariants

| Invariant | Failure detected |
|---|---|
| Explicit stop produces no samples from that clip after the allowed stop boundary | stop leakage, hidden voices |
| Finite play emits exactly its expected frames and one END event | truncation, unintended looping, duplicate callback |
| Infinite play wraps until stop and does not naturally complete | premature stop, incorrect END |
| Pause holds position and emits no clip samples | pause implemented as stop or ignored pause |
| Resume continues at the exact next frame | restart, skip, duplicated boundary sample |
| Duplicate Play does not alter active repeat policy | finite/infinite repeat corruption |
| Callback values and order match the guest ABI | internal/ABI enum confusion |
| Adapter state equals shared-service state after each operation | split-brain transitions |
| One `Advance(T)` equals any partition whose sum is `T` | rounding and block-boundary discontinuities |
| Save/restore continuation equals uninterrupted execution | missing audible state |
| Consecutive published chunks neither overlap nor gap without modeled silence | frontend resynchronization and clicks |
| Drop counters remain zero at the required drain cadence | queue starvation/retention loss |
| Invalid calls do not mutate state or emit callbacks | false-success paths |
| Every playing sealed clip eventually outputs, completes, or errors | immortal silent clips |

For multiple simultaneous clips, compare the mixed result against the sum of
separately rendered fixture streams with the same gain and clipping rules.

### Fuzzing layers

#### Layer 1: shared media service

Add `FuzzMediaStateMachine` under `runtime/`. This is the fastest target and
should run thousands of traces per second. It owns exact PCM, state, event, and
save/restore invariants.

Suggested local commands:

```powershell
go test ./runtime -run TestMedia -count=1
go test ./runtime -run '^$' -fuzz FuzzMediaStateMachine -fuzztime 10m
go test -race ./runtime
```

Do not require `-race` on every fuzz iteration; replay minimized corpus cases
under the race detector instead.

#### Layer 2: adapter differential testing

Run the same abstract trace through the operations shared by:

- public WIPI-C;
- KTF WIPI-C;
- KTF Java;
- Raptor, with provider-specific statuses normalized;
- SKVM `AudioClip`, where its blocking method contract permits comparison.

Record a normalized transcript:

```text
operation, return class, service state, guest-visible state,
position frame, callbacks, output frame count, output hash
```

Differences are only accepted when documented as carrier ABI behavior or a
hash-keyed title quirk.

#### Layer 3: publication scheduling fuzzing

Vary:

- frame advances from sub-millisecond pieces to 100 ms stalls;
- timer-split quanta and non-integral 44.1 kHz boundaries;
- frontend drain cadence, including temporary stalls around retention limits;
- stop/pause/resume just before and after a sample boundary;
- reset, load-state, and mix-mode changes with queued PCM.

Assert generation changes, cursor continuity, bounded stop latency, and drop
counter policy. Feed output to a discontinuity detector that reports unexpected
sample-cursor gaps/overlaps and boundary impulses.

#### Layer 4: real-title corpus fuzzing

The `aram-test` corpus runner should randomize user input and scheduling while
collecting media API/state traces. Useful mutations include:

- key sequences that enter/leave menus and toggle sound repeatedly;
- rapid background/foreground and pause/resume cycles;
- frame quantum and audio drain cadence;
- save/restore injection at every observed media transition;
- output sample rate and faithful/enhanced policy selection;
- long runs that cross several natural loop boundaries.

Per-title assertions should begin generic: no machine fault, bounded resources,
no permanent silent-playing clip, no invalid callback value, and no unexpected
drop. PCM hashes are useful after a title reaches a deterministic path, but
they should not be the only oracle.

Every failure artifact should contain:

- fuzz seed and minimized command stream;
- title/source SHA-256 and profile stack;
- runtime/adapter identity;
- virtual timestamps and audio generation;
- normalized state/callback transcript;
- PCM block hashes and cursor ranges;
- drop counters and the last relevant host trace records.

## Implementation plan

### P0 — restore faithful lifecycle semantics

1. Disable the detached persistent voice path while retaining ordinary
   multi-clip mixing.
2. Make explicit stop/clear/destroy silence all derived playback immediately.
3. Introduce named guest callback constants and translate from an internal
   transition enum.
4. Make shared transitions report `changed`, `invalid`, or `unsupported` and
   update adapters only after success.
5. Add deterministic regression tests for each issue before changing the
   implementation.

### P1 — add the stateful oracle

1. Implement `FuzzMediaStateMachine` with WAV fixtures first.
2. Add advance-partition and save/restore equivalence properties.
3. Add public WIPI-C and KTF Java adapter transcript tests.
4. Add minimized regression seeds for every discovered mismatch.

### P2 — eliminate silent permanent states and delivery instability

1. Return typed errors for sealed unsupported/malformed sources.
2. Derive advertised capabilities from enabled decoders.
3. Define publication stop-latency and retention requirements.
4. Unify SKVM with the timestamped audio publication contract.
5. Turn debug drop counters into corpus-run assertions.

### P3 — close observable feature gaps

1. Deliver all KTF Java lifecycle listener events.
2. Implement `setPosition` and global volume correctly.
3. Separate consumable streaming buffers from sealed source clips.
4. Add codecs only in corpus-demonstrated order; MP3 is the first candidate
   because it is already advertised.
5. Add WIPI 2.x player/control APIs as titles demonstrate demand.

Recording, camera, and video require explicit host providers and permission
boundaries. They should remain clearly unsupported instead of becoming
successful no-ops.

## Acceptance criteria

The first media-stability milestone is complete when:

- faithful mode has no hidden voice and every explicit stop is observable
  within the documented latency;
- callback values, order, and duplicate-operation behavior match WIPI 1.2.1;
- KTF Java and WIPI-C normalized traces agree with the shared model;
- unsupported sealed media terminates with a deterministic error rather than
  permanent silent playback;
- arbitrary advance partitioning and save/restore produce identical PCM and
  events;
- publication fuzzing produces no unexplained gap, overlap, or drop under the
  supported drain cadence;
- the media fuzz corpus replays cleanly under normal and race-enabled tests;
- real-title runs record no invalid callback values, leaked infinite voices,
  unbounded clip growth, or silent-playing deadlocks.

## Verification baseline

At the time of this audit:

- `go test ./...` passed;
- race-enabled tests passed for `runtime`, public WIPI-C, KTF, SKVM, and
  `application`;
- no media state-machine fuzz target existed; the repository's Go fuzz targets
  covered container/loader parsing instead.

These results establish that the findings are contract and coverage gaps, not
currently detected data races or ordinary unit-test failures.
