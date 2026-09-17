# LGT MMPP native subset

## Evidence

Public API page fetched directly on 2026-09-11:
https://nikita36078.github.io/J2ME_Docs/docs/LG_MMPP_API/mmpp/media/MediaPlayer.html

This third-party archive does not establish an exact handset or SDK version.
This is an implementation from public behavioral documentation and synthetic
fixtures, not copied handset/reference product code. No proprietary bytes or
corpus extraction are involved. It is not a claim of complete LGT compatibility.

Verified by that documentation:

| Method | Descriptor | Documented behavior |
|---|---|---|
| constructor | `()V` | Public no-argument constructor |
| setMediaLocation | `(Ljava/lang/String;)V` | Set media location. Missing resource throws `java.io.IOException` |
| setMediaSource | `([B)V` | Set byte-array source |
| setMediaSource | `([BII)V` | Use offset and length within byte-array source |
| setPlayBackLoop | `(Z)V` | True repeats, false plays once |
| isPlayBackLoop | `()Z` | Read loop setting |
| start / stop / pause | `()V` | Start / stop / temporarily pause playback |
| resume | `()V` | Resume from the paused position |
| getVolumeLevel | `()Ljava/lang/String;` | Obtain available volume level(s), with no String grammar specified |
| setVolumeLevel | `(Ljava/lang/String;)V` | Set volume, with no String grammar specified |

The mirrored Javadoc alone does not specify volume grammar. The setter now has
an explicitly bounded interoperability implementation, described below. The
getter remains an explicit host `unsupported` error because "available levels"
and "current level" are different contracts. Invalid arrays, codecs, lifecycle
transitions and missing-source errors are not mapped to invented handset
exceptions or successful playback. The source-less cleanup identity is the
narrow exception described below.

### Later local reference evidence (2026-09-12)

[Hash-qualified ROM analysis](reference-emulators-20260912.md#lgt-midp)
now provides additional evidence for one supplied emulator: its getter reads
a cached decimal volume, its constructor uses a preference value, and its
stop wrapper delegates to MIDlet-scoped shared MIDI ownership. Its setter
apparently guards the old volume rather than the new argument. These are
static wrapper observations with unresolved quick-op/native/runtime details,
not a universal handset specification. They do not silently replace the
conservative product choices below. In particular, the unsupported getter
is now a pending implementation/verification boundary, not an absence of
any evidence for a current-value getter.

## Emulator choices, not verified handset facts

* Initial loop setting is false. No source is created by construction.
* Locations use the existing packaged-resource service with an optional leading
  slash. No network URLs, filesystem access, or speculative locator schemes.
* Source bytes are copied. The bounded overload validates using wide arithmetic.
* Only a complete source accepted by the shared decoder is installed. Source
  installation uses a candidate shared clip and keeps the prior clip on failure.
  The stopped candidate is validated with shared `Media.Prepare`, without any
  Play/Stop transition. Failed candidates are destroyed while stopped. Candidate
  preparation and stopped destruction preserve other players' queued PCM and
  output revision. This preserves the player's old
  source, gain, position and playback state, not the service allocator sequence.
* Replacement is supported while stopped. Replacement while playing or paused,
  and changing loop settings during playback, are explicitly unsupported because
  the page does not define those interactions.
* Stop on a validated, constructed player with no installed clip (`clip == 0`)
  is an identity, including repeated cleanup. It does not allocate a clip, change
  fields or shared services, clear queued PCM, or affect another active player.
  Missing/uninitialized receivers and invalid nonzero clip IDs remain errors.
  This is documented emulator cleanup semantics, not verified handset behavior.
  An independently observed original trace reached constructor then stop at
  43907 with zero source attempts. Static code following that stop sets media
  location, loop and volume, then starts in an IOException-protected block.
  This observation does not establish the unknown source or successful playback.
* Stop on a validated already-stopped real clip is also an identity, whether
  freshly prepared, explicitly stopped or naturally completed. It preserves
  the cursor and other players' queued PCM. Restart of a completed clip uses
  the shared Play contract to rewind. A spaced original control sequence
  independently exposed this cleanup path at 111444 instructions before the
  correction. This is the same explicit idempotent-cleanup model, not a claim
  about handset lifecycle errors.
* Stop of an active or paused real clip stops playback and rewinds to zero.
  Repeated start, pause outside playback and resume outside pause still fail.
  Start/pause/resume without a source
  still fail. Missing locations still throw IOException before cleanup, and
  unknown source errors are never converted into playback success. These are
  emulator limits, not handset error mappings.
* Shared media defaults apply, including gain 100 and unmuted output. That is an
  emulator mixer default, not an assertion about the undocumented volume String.
* The adapter uses `runtime.Media` clips, decoder, virtual-time advancement,
  PCM output, pause/resume, and loop counts. It does not synthesize silence as a
  successful substitute for unsupported media.

Coordinator integration also enables the exact MMPP class
`mmpp/media/BackLight`. Only documented static `on(I)V` and `off()V` are installed
by its separate adapter. No color methods or speculative constructor are added.

## Bounded volume interoperability

The independent public J2ME-Loader implementation at commit
[`e4d5872a57d167fbf4b22058a24cd2ea4f8ae15b`](https://github.com/nikita36078/J2ME-Loader/blob/e4d5872a57d167fbf4b22058a24cd2ea4f8ae15b/app/src/main/java/mmpp/media/MediaPlayer.java)
uses a maximum level of five and maps integer strings to a percentage. This is
third-party emulator evidence, not an exact handset/SDK specification. No
implementation code was copied. The mirrored Javadoc only states that the
setter sets volume; it does not settle units, ranges or error behavior.

ARAM's explicit LGT interoperability subset accepts decimal signed-32-bit
integer strings whose value is 0 through 5, mapping them to shared clip gains
0, 20, 40, 60, 80 and 100. Whitespace, lists, fractional values, overflow and
out-of-range values remain unsupported. The player must have an installed
source. This does not install a source, start playback, invent a decoder or
accept an invalid receiver. Existing mute and pan are preserved. Repeating the
same gain leaves queued PCM and output revision unchanged; actual changes use
the shared service's existing discontinuity behavior. Gain belongs to the
installed clip and is serialized there. A successful source replacement uses
the ordinary new-clip defaults, rather than inventing a persistent volume
String property. Failed source replacement preserves the prior clip and gain.

`getVolumeLevel` remains unsupported: the Javadoc describes available levels,
whereas that independent implementation returns the current String. ARAM does
not turn this ambiguity into a guessed getter value or handset exception.

`TestMMPPVolumeLevelsProduceExactPCM` uses a synthetic constant 10000-amplitude
WAV and checks each positive level produces exact 2000-times-level PCM samples.
Zero produces no nonzero PCM while virtual playback advances. Transactionality,
other-clip gain isolation, same-gain PCM retention, policy isolation and
byte-identical replay are checked separately. These tests failed on the former
unsupported setter before the adapter was implemented. They verify actual
mixing, not merely successful native returns or stored fields.

## Policy and save-state boundary

`NativePolicySKT = 0` and `NativePolicyJ2ME = 1` retain their existing values.
`NativePolicyLGT = 2` is an explicit opt-in. Generic J2ME and SKT do not acquire
MMPP. LGT receives standard Java/MIDP plus four exact classes:
`mmpp/media/MediaPlayer`, `mmpp/media/BackLight`, `mmpp/lang/MathFP`, and
`mmpp/microedition/lcdui/GraphicsX`. This is not a prefix-wide OEM namespace
or a claim that every method of those classes is supported. Registries are per-VM. LGT uses generic CLDC/MIDP
system-property fallback and does not expose application metadata as system
properties. Host profile identity remains the coordinator's existing
`j2me-1.0/lgt/generic` configuration.

The outer VM save-state format remains version 4. Existing SKT class digests
remain unmodified. Generic J2ME retains `j2me-native-policy-v1\0`. LGT binds its
class digest to `lgt-mmpp-native-policy-v2\0`, defining version 2 of this native
capability/semantic set with idempotent stop cleanup and the bounded volume
setter. Prior LGT v1 states and
old LGT sessions hosted under generic J2ME policy are intentionally rejected
atomically, not silently migrated. Future incompatible
LGT native semantics must revise this policy domain. No unrelated old-policy
state version bump is required.

MMPP reuses serialized `audioClipState` service IDs and object fields. Shared
services serialize source data, decoded progress, remaining loops, gain and PCM
state. There is no new ad-hoc decoder state outside the existing state graph.
Cross-policy restores are rejected before replacing the running VM/services.

## Synthetic verification

Tests in `skvm/natives_lgt_media*_test.go` cover actual nonzero PCM, timeline
progress, paused-position preservation, resume, repeat versus once, stopped
replacement, exact source slicing and copying, missing resources, null/bounds/
empty/undecodable sources, failure preserving a prior clip, unsupported volume
getter and out-of-subset setter values, deterministic paused replay, independent
registries, exact OEM
allowlisting, existing digest stability, and atomic cross-policy rejection.
Tests were added before the implementation and initially failed on missing
`NativePolicyLGT`. Core tests need neither audio hardware nor network.

Cleanup regression tests were red before the v2 change. They cover byte-identical
repeated empty stop with another active player's queued PCM/services unchanged,
save/restore, missing-location IOException followed by cleanup, unknown-source
failure, subsequent real WAV playback and replay, invalid receivers/clip IDs,
byte-identical cleanup of prepared, explicitly stopped and naturally completed
clips with another player's PCM preserved, verified restart, and atomic v1-state
rejection.
