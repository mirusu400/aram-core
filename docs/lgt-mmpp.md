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

Both volume methods return an explicit host `unsupported` capability error.
There is no guessed successful return value, parser, default String, or guessed
Java exception class. The documentation does not establish the handset's error
classes for invalid arrays, codecs, lifecycle transitions, or missing sources.
Those paths likewise fail explicitly rather than pretending successful playback.

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
* Stop rewinds to zero. Start uses the stopped clip. Repeated start, pause outside
  playback, resume outside pause, stop outside active playback, and playback
  without a source fail explicitly. These are emulator limits, not handset error
  mappings.
* Shared media defaults apply, including gain 100 and unmuted output. That is an
  emulator mixer default, not an assertion about the undocumented volume String.
* The adapter uses `runtime.Media` clips, decoder, virtual-time advancement,
  PCM output, pause/resume, and loop counts. It does not synthesize silence as a
  successful substitute for unsupported media.

Coordinator integration also enables the exact MMPP class
`mmpp/media/BackLight`. Only documented static `on(I)V` and `off()V` are installed
by its separate adapter. No color methods or speculative constructor are added.

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
class digest to `lgt-mmpp-native-policy-v1\0`, defining version 1 of this native
capability/semantic set. Thus old LGT sessions previously hosted under generic
J2ME policy are intentionally rejected, not silently migrated. Future incompatible
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
empty/undecodable sources, failure preserving a prior clip, explicit volume
unsupported, deterministic paused replay, independent registries, exact OEM
allowlisting, existing digest stability, and atomic cross-policy rejection.
Tests were added before the implementation and initially failed on missing
`NativePolicyLGT`. Core tests need neither audio hardware nor network.
