package runtime

import (
	"bytes"
	"slices"
	"testing"
)

func mixedATRLengthScore(seconds int) []byte {
	fm := sustainedSMAFScore(seconds)
	atr := smafTestATR(1, []byte{0, 0, 0x11, 0, 2, 2},
		[]byte{0, 1, 125, 0, 0, 0, 0}, bytes.Repeat([]byte{0x77, 0xff}, 128))
	return smafStreamTestFile(fm[8:], atr)
}

func TestSMAFMixedATRRemainsLazyAndMatchesWholeRender(t *testing.T) {
	for _, rate := range []uint32{8000, 44100} {
		file := mixedATRLengthScore(5)
		full, lazy := decodeSMAFPCM16(file, rate), decodeSMAFLazyPCM16(file, rate)
		if full == nil || lazy == nil || lazy.smaf == nil || lazy.smaf.cursor != 0 || len(lazy.samples) != 0 {
			t.Fatal("a short ATR track eagerly rendered the accompanying long FM score")
		}
		if full.duration != lazy.duration {
			t.Fatalf("rate %d: lazy natural duration %s, full %s", rate, lazy.duration, full.duration)
		}
		for frame := uint64(733); frame < uint64(len(full.samples)/2); frame += 733 {
			lazy.ensureFrame(frame)
		}
		lazy.ensureFrame(uint64(len(full.samples) / 2))
		if !slices.Equal(full.samples, lazy.samples) {
			t.Fatalf("rate %d: mixed ATR incremental samples differ from whole render", rate)
		}
	}
}

func TestSMAFPCMEventLengthProbeMatchesSampleWalk(t *testing.T) {
	for _, rate := range []uint32{8000, 22050, 44100} {
		for _, loop := range []bool{false, true} {
			for _, control := range []smafEventKind{smafHoldPedal, smafAllSoundOff, smafAllNotesOff, smafResetControllers} {
				makeDecoder := func() *smafDecoder {
					loopPoint := 63
					if loop {
						loopPoint = 13
					}
					pcm := parseSMAFVoice(smafTestPCMVoice(1, loopPoint, 63))
					pcm.key.program = 1
					pcm.pcm.envelope.sus = true
					decoder := &smafDecoder{rate: rate, voices: []smafParsedVoice{pcm}}
					for index := range decoder.channels {
						decoder.channels[index].volume, decoder.channels[index].expression = 1, 1
					}
					decoder.channels[0].streamWaves = []smafWave{{number: 1, sampleRate: 8000, channels: 1, pcm: make([]int16, 64)}}
					decoder.addEvent(smafEvent{kind: smafProgram, channel: 0, a: 1})
					decoder.addEvent(smafEvent{kind: smafHoldPedal, channel: 0, a: 127})
					// Exhausting the sixteen PCM slots exercises voice stealing;
					// later notes reuse retired slots with the same envelope phase.
					for note := range 24 {
						decoder.addGatedNote(uint64(note%5), uint64(500+note*11), 0, 48+note%24, 100)
					}
					decoder.addGatedNote(0, 1700, 1, 64, 100) // a concurrent FM voice
					decoder.addEvent(smafEvent{sample: 100, kind: smafPitchBend, channel: 0, a: 1200})
					decoder.addEvent(smafEvent{sample: 900, kind: control, channel: 0})
					decoder.addEvent(smafEvent{sample: 1200, kind: smafHoldPedal, channel: 0})
					decoder.addGatedNote(2000, 1000, 0, 60, 100)
					sortSMAFEvents(decoder.events)
					return decoder
				}
				probe, reference := newSMAFRenderStream(makeDecoder()), newSMAFRenderStream(makeDecoder())
				reference.render(nil, reference.end, true)
				if got := probe.probeEnd(); got != reference.cursor {
					t.Fatalf("rate %d loop %t controller %d: event probe %d, exact sample walk %d", rate, loop, control, got, reference.cursor)
				}
			}
		}
	}
}

func TestSMAFLongMixedATRPlayDoesNotSynthesizeAtOpen(t *testing.T) {
	for _, seconds := range []int{10, 120} {
		decoded := decodeSMAFLazyPCM16(mixedATRLengthScore(seconds), 44100)
		if decoded == nil || decoded.smaf == nil || decoded.smaf.cursor != 0 || len(decoded.samples) != 0 {
			t.Fatalf("%d-second mixed score synthesized audio at open", seconds)
		}
	}
}

func BenchmarkColdMixedATRPlay(b *testing.B) {
	file := mixedATRLengthScore(120)
	b.ReportAllocs()
	for b.Loop() {
		if decoded := decodeSMAFLazyPCM16(file, 44100); decoded == nil {
			b.Fatal("mixed score did not decode")
		}
	}
}
