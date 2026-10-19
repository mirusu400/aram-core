package runtime

import (
	"fmt"
	"slices"
	"testing"
)

// A one-frame render refreshes the pan memo on every sample, as the original
// mixer did. Whole intervals must produce the same PCM, including new voices,
// fixed instrument pans, slot reuse, and several controls at the same sample.
func TestSMAFEventIntervalPanMatchesPerSampleMemo(t *testing.T) {
	for _, rate := range []uint32{8000, 48000, 192000} {
		for _, channels := range []int{1, 2} {
			for _, family := range []string{"sampled", "stream", "atr"} {
				t.Run(fmt.Sprintf("%dHz/%dch/%s", rate, channels, family), func(t *testing.T) {
					makeStream := func() *smafRenderStream {
						decoder := pcmExhaustionDecoder(rate, channels, family)
						fixed := defaultSMAFPatch()
						fixed.panFixed, fixed.panDefault = true, -0.375
						decoder.voices = append(decoder.voices, smafParsedVoice{
							key: smafVoiceKey{program: 2}, patch: fixed,
						})
						decoder.channels[2].program = 2
						decoder.addGatedNote(0, 600, 1, 60, 100)
						decoder.addGatedNote(0, 600, 2, 67, 100)
						for _, at := range []uint64{0, 1, 7, 31, 63, 300, 301} {
							for channel := range 3 {
								decoder.addEvent(smafEvent{sample: at, kind: smafPan, channel: channel, a: int(at) & 127})
								decoder.addEvent(smafEvent{sample: at, kind: smafPan, channel: channel, a: 127 - int(at)&127})
							}
						}
						decoder.addEvent(smafEvent{sample: 65, kind: smafAllSoundOff, channel: 1})
						decoder.addGatedNote(66, 400, 1, 72, 110)
						sortSMAFEvents(decoder.events)
						return newSMAFRenderStream(decoder)
					}
					whole, sampleWalk := makeStream(), makeStream()
					want := []int16(nil)
					for !sampleWalk.finished {
						want = sampleWalk.renderUntil(want, sampleWalk.cursor+1)
					}
					got := whole.renderUntil(nil, whole.end)
					if !slices.Equal(got, want) || whole.cursor != sampleWalk.cursor {
						t.Fatalf("event-interval pan differs from per-sample memo: frames %d/%d", whole.cursor, sampleWalk.cursor)
					}
				})
			}
		}
	}
}

func TestSMAFRenderEntryRefreshesExternallyChangedPan(t *testing.T) {
	makeStream := func() *smafRenderStream {
		decoder := &smafDecoder{rate: 8000}
		decoder.channels[0].volume, decoder.channels[0].expression = 1, 1
		decoder.addGatedNote(0, 600, 0, 60, 100)
		return newSMAFRenderStream(decoder)
	}
	changed, controlled := makeStream(), makeStream()
	got, want := changed.renderUntil(nil, 20), controlled.renderUntil(nil, 20)
	changed.decoder.pool[0].pan = 0.75
	controlled.decoder.fire(smafEvent{kind: smafPan, a: 112})
	got = changed.renderUntil(got, changed.end)
	want = controlled.renderUntil(want, controlled.end)
	if !slices.Equal(got, want) {
		t.Fatal("render entry reused stale pan after an external decoder change")
	}
}
