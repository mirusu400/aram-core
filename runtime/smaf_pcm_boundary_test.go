package runtime

import (
	"fmt"
	"slices"
	"testing"
)

func pcmExhaustionDecoder(rate uint32, channels int, family string) *smafDecoder {
	pcm := make([]int16, 64*channels)
	for index := range pcm {
		pcm[index] = int16(index*701 - 30000)
	}
	wave := smafWave{number: 1, sampleRate: 11025, channels: channels, pcm: pcm}
	decoder := &smafDecoder{rate: rate}
	for index := range decoder.channels {
		decoder.channels[index].volume, decoder.channels[index].expression = 1, 1
	}
	switch family {
	case "sampled":
		voice := parseSMAFVoice(smafTestPCMVoice(1, 63, 63))
		voice.key.program = 1
		voice.pcm.sampleRate, voice.pcm.envelope.sus = wave.sampleRate, true
		decoder.voices = []smafParsedVoice{voice}
		decoder.channels[0].sampledWaves = []smafWave{wave}
		decoder.addEvent(smafEvent{kind: smafProgram, a: 1})
		decoder.addEvent(smafEvent{kind: smafNoteOn, a: 60, b: 100, noteID: 1})
	case "stream":
		decoder.channels[0].bankMSB = 125
		decoder.channels[0].streamWaves = []smafWave{wave}
		decoder.addEvent(smafEvent{kind: smafNoteOn, a: 0, b: 100, noteID: 1})
	case "atr":
		decoder.tracks = []smafTrack{{number: 0, audio: true, waves: []smafWave{wave}}}
		decoder.addEvent(smafEvent{kind: smafWaveOn, a: 1, noteID: 1})
	default:
		panic("unknown PCM fixture family")
	}
	return decoder
}

// The event positions are derived from the old sample-by-sample renderer,
// not a sourceRate/outputRate multiplication: repeated floating-point position
// additions may cross the final frame a sample later than that formula.
func TestSMAFPCMExhaustionEventBoundariesMatchSampleWalk(t *testing.T) {
	for _, rate := range []uint32{8000, 48000, 192000} {
		for _, channels := range []int{1, 2} {
			for _, family := range []string{"sampled", "stream", "atr"} {
				t.Run(fmt.Sprintf("%dHz/%dch/%s", rate, channels, family), func(t *testing.T) {
					oracle := newSMAFRenderStream(pcmExhaustionDecoder(rate, channels, family))
					oracle.render(nil, 1, true)
					for oracle.decoder.pcmPool[0].active {
						if oracle.cursor > uint64(rate)/10 {
							t.Fatal("short PCM fixture did not exhaust")
						}
						oracle.render(nil, oracle.cursor+1, true)
					}
					if oracle.decoder.pcmPool[0].position < float64(oracle.decoder.pcmPool[0].end) {
						t.Fatal("fixture retired by its envelope instead of source exhaustion")
					}
					exhaustion := oracle.cursor - 1
					for _, action := range []struct {
						name string
						kind smafEventKind
						held bool
					}{
						{"note_on", smafNoteOn, false},
						{"cc64", smafHoldPedal, true},
						{"cc120", smafAllSoundOff, false},
						{"cc121", smafResetControllers, true},
						{"cc123", smafAllNotesOff, false},
					} {
						for _, offset := range []int64{-1, 0, 1} {
							t.Run(fmt.Sprintf("%s/%+d", action.name, offset), func(t *testing.T) {
								at := uint64(int64(exhaustion) + offset)
								makeDecoder := func() *smafDecoder {
									decoder := pcmExhaustionDecoder(rate, channels, family)
									key := decoder.events[len(decoder.events)-1].a
									if action.held {
										decoder.channels[0].holdPedal = true
										decoder.addEvent(smafEvent{sample: exhaustion - 3, kind: smafNoteOff, a: key, noteID: 1})
									}
									event := smafEvent{sample: at, kind: action.kind}
									if action.kind == smafNoteOn {
										event.a, event.b, event.noteID = key, 80, 2
										if family == "atr" {
											event.kind = smafWaveOn
										}
										// A stale gate must not release the replacement,
										// including when it reused the exhausted voice's slot.
										decoder.addEvent(smafEvent{sample: exhaustion + 2, kind: smafNoteOff, a: key, noteID: 1})
									}
									decoder.addEvent(event)
									sortSMAFEvents(decoder.events)
									return decoder
								}
								reference := newSMAFRenderStream(makeDecoder())
								want := reference.renderUntil(nil, reference.end)
								probe := newSMAFRenderStream(makeDecoder())
								if got := probe.probeEnd(); got != reference.cursor {
									t.Fatalf("event probe endpoint %d, exact sample walk %d", got, reference.cursor)
								}
								incremental := newSMAFRenderStream(makeDecoder())
								var got []int16
								for _, target := range []uint64{1, at, at + 1, exhaustion + 2, exhaustion + 3, reference.cursor} {
									got = incremental.renderUntil(got, target)
								}
								if !slices.Equal(got, want) {
									t.Fatal("splitting the render around exhaustion/controller events changed PCM")
								}
								if incremental.cursor != reference.cursor || !incremental.finished {
									t.Fatalf("incremental endpoint %d finished=%t, want %d", incremental.cursor, incremental.finished, reference.cursor)
								}
							})
						}
					}
				})
			}
		}
	}
}
