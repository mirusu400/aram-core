package runtime

import (
	"math"
	"slices"
	"testing"
	"time"
)

func audioControlsSMF(t *testing.T, track []byte) *smafDecoder {
	t.Helper()
	d := decodeSMFEvents(smfFile(0, 480, append(track, smfEndOfTrack()...)), 44_100)
	if d == nil {
		t.Fatal("synthetic MIDI fixture did not parse")
	}
	return d
}

func TestSMFOverlappingPitchReleases(t *testing.T) {
	for _, ending := range []string{"note-offs", "all-notes-off", "end-of-track"} {
		t.Run(ending, func(t *testing.T) {
			track := []byte{0, 0x90, 60, 100, 48, 0x90, 60, 100}
			switch ending {
			case "note-offs":
				track = append(track, 48, 0x80, 60, 0, 96, 0x80, 60, 0)
			case "all-notes-off":
				track = append(track, smfVLQ(144)...)
				track = append(track, 0xb0, 123, 0)
			case "end-of-track":
				track = append(track, smfVLQ(432)...)
				track = append(track, 0xff, 0x2f, 0)
			}
			d := audioControlsSMF(t, track)
			starts, offs := 0, 0
			for _, event := range d.events {
				if event.kind == smafNoteOn {
					starts++
				}
				if event.kind == smafNoteOff {
					offs++
				}
				d.fire(event)
			}
			held := 0
			for _, voice := range d.pool {
				if voice.active && voice.keyDown {
					held++
				}
			}
			if starts != 2 || offs != 2 || held != 0 {
				t.Fatalf("started %d notes, sent %d releases, left %d keys held", starts, offs, held)
			}
		})
	}
}

func TestSMFAllSoundOffStopsReleaseTails(t *testing.T) {
	for _, releaseFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "held-key", true: "release-tail"}[releaseFirst], func(t *testing.T) {
			track := []byte{0, 0x90, 60, 100, 0, 0x91, 67, 100}
			if releaseFirst {
				track = append(track, 48, 0x80, 60, 0)
			}
			track = append(track, 48, 0xb0, 120, 0)
			d := audioControlsSMF(t, track)
			// The other channel must survive the channel-0 command. EOF cleanup
			// releases its key but leaves its normal envelope tail active.
			for _, event := range d.events {
				d.fire(event)
			}
			other := 0
			for _, voice := range d.pool {
				if voice.active && voice.channel == 0 {
					t.Fatal("all sound off left a channel-0 oscillator active")
				}
				if voice.active && voice.channel == 1 {
					other++
				}
			}
			if other != 1 {
				t.Fatal("all sound off affected a different channel")
			}
		})
	}
	var renders []*decodedPCM
	for _, control := range []byte{120, 123} {
		pcm := decodeSMFPCM16(smfFile(0, 480, append([]byte{0, 0x90, 60, 100, 96, 0xb0, control, 0}, smfEndOfTrack()...)), 44_100)
		if pcm == nil {
			t.Fatal("channel mode fixture did not decode")
		}
		renders = append(renders, pcm)
	}
	if renders[0].duration > 101*time.Millisecond || renders[1].duration <= renders[0].duration || slices.Equal(renders[0].samples, renders[1].samples) {
		t.Fatalf("CC120 duration %s, CC123 duration %s: immediate mute must differ from release", renders[0].duration, renders[1].duration)
	}
}

func TestSMFHoldPedalDefersRelease(t *testing.T) {
	var renders []*decodedPCM
	for _, hold := range []byte{127, 0} {
		track := []byte{0, 0xb0, 64, hold, 0, 0x90, 60, 100, 96, 0x80, 60, 0}
		track = append(track, smfVLQ(384)...)
		track = append(track, 0xb0, 64, 0)
		pcm := decodeSMFPCM16(smfFile(0, 480, append(track, smfEndOfTrack()...)), 44_100)
		if pcm == nil {
			t.Fatal("hold fixture did not decode")
		}
		renders = append(renders, pcm)
	}
	// The held envelope continues decaying at SR; its eventual release can be
	// shorter than the early key-off's release. It must still sound much longer.
	if renders[0].duration < renders[1].duration+300*time.Millisecond || slices.Equal(renders[0].samples, renders[1].samples) {
		t.Fatalf("pedal on %s, pedal off %s: released note was not sustained", renders[0].duration, renders[1].duration)
	}
}

func TestSMFResetControllersRestoresExpressionAndPitch(t *testing.T) {
	d := audioControlsSMF(t, []byte{0, 0xb0, 11, 0, 0, 0xe0, 0, 96, 0, 0xb0, 121, 0, 0, 0x90, 60, 100, 96, 0x80, 60, 0})
	for _, event := range d.events {
		d.fire(event)
	}
	channel := d.channels[0]
	if channel.expression != 1 || channel.bend != 0 {
		t.Fatalf("reset left expression %.3f and pitch bend %.3f semitones", channel.expression, channel.bend)
	}
}

func TestSMFEndOfTrackPreservesTrailingRest(t *testing.T) {
	track := []byte{0, 0x90, 60, 100, 96, 0x80, 60, 0}
	track = append(track, smfVLQ(3744)...)
	track = append(track, 0xff, 0x2f, 0)
	assertScoreTrailingRest(t, smfFile(0, 480, track))
}

func assertScoreTrailingRest(t *testing.T, file []byte) {
	t.Helper()
	full := decodeSMAFPCM16(file, 44_100)
	if looksLikeSMF(file) {
		full = decodeSMFPCM16(file, 44_100)
	}
	lazy := decodeScoreLazyPCM16(file, 44_100)
	if full == nil || lazy == nil {
		t.Fatal("trailing rest fixture did not decode")
	}
	if full.duration < 4*time.Second || full.duration > 4*time.Second+time.Second/44_100 {
		t.Fatalf("declared end 4s, rendered end %s", full.duration)
	}
	if lazy.duration != full.duration {
		t.Fatalf("incremental duration %s differs from full render %s", lazy.duration, full.duration)
	}
	lazy.ensureFrame(44_100 * 5)
	if !slices.Equal(lazy.samples, full.samples) {
		t.Fatal("incremental render lost the rest or changed the sound")
	}
}

func TestSMFSMPTEFrameRates(t *testing.T) {
	for _, test := range []struct {
		name    string
		frames  byte
		seconds float64
	}{
		{"24", 0xe8, 30.0 / 24}, {"25", 0xe7, 30.0 / 25}, {"29-drop", 0xe3, 1.001}, {"30", 0xe2, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			track := append(smfTempo(250_000), 0, 0x90, 60, 100)
			track = append(track, smfVLQ(2400)...)
			track = append(track, 0x80, 60, 0)
			d := decodeSMFEvents(smfFile(0, int(test.frames)<<8|80, append(track, smfEndOfTrack()...)), 44_100)
			if d == nil {
				t.Fatal("SMPTE fixture did not decode")
			}
			found := false
			for _, event := range d.events {
				if event.kind == smafNoteOff {
					found = true
					if seconds := float64(event.sample) / 44_100; math.Abs(seconds-test.seconds) > 1.0/44_100 {
						t.Fatalf("30 frames lasted %.9fs, want %.9fs", seconds, test.seconds)
					}
				}
			}
			if !found {
				t.Fatal("no release recorded")
			}
		})
	}
}

func audioControlsFixedPanFM(pan byte) []byte {
	body := []byte{0, pan<<3 | 1, 0x21} // basic octave 1, PE=1, parallel 2-op
	for range 2 {
		body = append(body, 0x10, 0x70, 0xf0, 0, 0, 0x10, 0)
	}
	return append([]byte{0x43, 0x79, 7, 0x7f, 1, 0, 0, 0, 0, 0}, body...)
}

func TestSMAFFixedPanFMOverridesChannelPan(t *testing.T) {
	for _, initialRight := range []bool{false, true} {
		t.Run(map[bool]string{false: "mid-note-pan", true: "initial-channel-pan"}[initialRight], func(t *testing.T) {
			sequence := []byte{0, 0x90, 60, 127, 125, 25, 0xb0, 10, 127}
			if initialRight {
				sequence = append([]byte{0, 0xb0, 10, 127}, sequence...)
			}
			file := smafStreamTestFile(smafTestUploadTrack(smafTestExclusive(audioControlsFixedPanFM(0)), sequence, nil))
			pcm := decodeSMAFPCM16(file, 44_100)
			if pcm == nil {
				t.Fatal("fixed pan FM fixture did not decode")
			}
			for _, at := range []time.Duration{20 * time.Millisecond, 200 * time.Millisecond} {
				left := smafRegressionChannelRMS(t, pcm, at, 0)
				right := smafRegressionChannelRMS(t, pcm, at, 1)
				if left < 0.001 || right > left/20 {
					t.Fatalf("fixed left pan at %s: RMS left %.5f right %.5f", at, left, right)
				}
			}
		})
	}
	file := smafStreamTestFile(smafTestUploadTrack(smafTestExclusive(audioControlsFixedPanFM(15)), []byte{0, 0xb0, 10, 127, 0, 0x90, 60, 127, 125}, nil))
	pcm := decodeSMAFPCM16(file, 44_100)
	if pcm == nil {
		t.Fatal("fixed centre fixture did not decode")
	}
	left, right := smafRegressionChannelRMS(t, pcm, 20*time.Millisecond, 0), smafRegressionChannelRMS(t, pcm, 20*time.Millisecond, 1)
	if left < 0.001 || math.Abs(left-right) > left/100 {
		t.Fatalf("fixed centre pan became channel pan: RMS left %.5f right %.5f", left, right)
	}
}

func TestSMAFZeroVelocityIsSilent(t *testing.T) {
	for _, sampled := range []bool{false, true} {
		t.Run(map[bool]string{false: "FM", true: "PCM"}[sampled], func(t *testing.T) {
			sequence := []byte{0, 0x90, 60, 0, 125}
			file := smafScore(sequence)
			if sampled {
				file = smafStreamTestFile(smafTestSampledTrack(0, smafTestPCMVoice(1, 7999, 7999), sequence, smafTestPCM16Wave(8000, 1)))
			}
			pcm := decodeSMAFPCM16(file, 44_100)
			if pcm == nil {
				t.Fatal("zero velocity fixture did not decode")
			}
			for _, sample := range pcm.samples {
				if sample != 0 {
					t.Fatal("velocity zero produced audible PCM")
				}
			}
		})
	}
}

func TestSMAFResetControllersClearsRememberedVelocity(t *testing.T) {
	sequence := []byte{0, 0x90, 60, 127, 25, 50, 0xb0, 121, 0, 0, 0x80, 60, 25}
	d := &smafDecoder{rate: 44_100}
	if !d.parse(smafScore(sequence)) || !d.buildEvents() {
		t.Fatal("SMAF reset fixture did not parse")
	}
	var velocities []int
	for _, event := range d.events {
		if event.kind == smafNoteOn {
			velocities = append(velocities, event.b)
		}
	}
	if !slices.Equal(velocities, []int{127, 64}) {
		t.Fatalf("velocities across reset %v, want [127 64]", velocities)
	}
}

func TestSMAFEndOfSequencePreservesTrailingRest(t *testing.T) {
	sequence := []byte{0, 0x90, 60, 127, 25}
	sequence = append(sequence, smfVLQ(1000)...)
	sequence = append(sequence, 0xff, 0x2f, 0)
	assertScoreTrailingRest(t, smafScore(sequence))
}

func TestSMFKeyOffDoesNotReleaseStolenOrMutedReplacement(t *testing.T) {
	for _, steal := range []bool{false, true} {
		t.Run(map[bool]string{false: "all-sound-off", true: "voice-steal"}[steal], func(t *testing.T) {
			track := []byte{0, 0x90, 60, 100}
			if steal {
				for range 32 {
					track = append(track, 0, 0x90, 60, 100)
				}
			} else {
				track = append(track, 0, 0xb0, 120, 0, 0, 0x90, 60, 100)
			}
			track = append(track, 96, 0x80, 60, 0, 96, 0x80, 60, 0)
			d := audioControlsSMF(t, track)
			stream := newSMAFRenderStream(d)
			stream.render(nil, 4411, true)
			if !d.pool[0].keyDown {
				t.Fatal("old key-off released the replacement before its own key-off")
			}
			for _, event := range d.events {
				if (event.kind == smafNoteOn || event.kind == smafNoteOff) && event.noteID == 0 {
					t.Fatal("MIDI key pairing lost its identity")
				}
			}
		})
	}
}

func audioControlsNativeDecoder(t *testing.T, sampled, sus bool, sequence []byte) *smafDecoder {
	t.Helper()
	voice := audioControlsFixedPanFM(15)
	voice[12] &^= 0x20
	var wave []byte
	if sampled {
		voice = smafTestPCMVoice(1, 0, 7999)
		voice[15] = 0x70 // RR=7, long enough to distinguish release from sustain
		wave = smafTestPCM16Wave(8000, 1)
		if sus {
			voice[14] |= 2
		}
	} else if sus {
		voice[13] |= 2
		voice[20] |= 2
	}
	file := smafStreamTestFile(smafTestSampledTrack(0, voice, sequence, wave))
	d := &smafDecoder{rate: 44_100}
	if !d.parse(file) || !d.buildEvents() {
		t.Fatal("native control fixture did not parse")
	}
	return d
}

func TestSMAFNativeHoldRespectsSUSAndThreshold(t *testing.T) {
	for _, sampled := range []bool{false, true} {
		for _, sus := range []bool{false, true} {
			for _, hold := range []byte{63, 64, 127} {
				sequence := []byte{0, 0xb0, 64, hold, 0, 0x90, 60, 100, 25, 50, 0xb0, 64, 0}
				d := audioControlsNativeDecoder(t, sampled, sus, sequence)
				stream := newSMAFRenderStream(d)
				stream.render(nil, 6615, true) // 150 ms, after gate and before pedal lift
				keyDown, phase := d.pool[0].keyDown, d.pool[0].operators[1].envelope.phase
				if sampled {
					keyDown, phase = d.pcmPool[0].keyDown, d.pcmPool[0].envelope.phase
				}
				if keyDown || (phase != smafEnvelopeRelease && phase != smafEnvelopeIdle) != (sus && hold >= 64) {
					t.Fatalf("sampled=%t SUS=%t hold=%d: keyDown=%t phase=%d", sampled, sus, hold, keyDown, phase)
				}
				stream.render(nil, 11025, true) // 250 ms, pedal has lifted
				phase = d.pool[0].operators[1].envelope.phase
				if sampled {
					phase = d.pcmPool[0].envelope.phase
				}
				if phase != smafEnvelopeRelease && phase != smafEnvelopeIdle {
					t.Fatalf("sampled=%t: pedal lift did not release the gate", sampled)
				}
			}
		}
	}
}

func TestSMAFControllerResetUpdatesSoundingVoicesAndPreservesMix(t *testing.T) {
	for _, sampled := range []bool{false, true} {
		d := audioControlsNativeDecoder(t, sampled, true, []byte{
			0, 0xb0, 64, 127,
			0, 0x90, 60, 100, 25, 0, 0x90, 64, 100, 125,
			0, 0xb1, 11, 33, 0, 0x91, 67, 100, 125,
			25, 0xb0, 7, 47, 0, 0xb0, 10, 0,
			0, 0xb0, 0, 5, 0, 0xb0, 32, 9, 0, 0xc0, 13,
			0, 0xb0, 11, 0, 0, 0xb0, 1, 77, 0, 0xe0, 0, 96,
			25, 0xb0, 121, 0,
		})
		stream := newSMAFRenderStream(d)
		stream.render(nil, 6615, true)
		stream.render(nil, 11025, true)
		channel := d.channels[0]
		if channel.expression != 1 || channel.bend != 0 || channel.modulation != 0 || channel.holdPedal {
			t.Fatalf("sampled=%t: reset left muted/bent/held controller state", sampled)
		}
		if channel.volume != 47.0/127 || channel.pan != -1 || channel.bankMSB != 5 || channel.bankLSB != 9 || channel.program != 13 {
			t.Fatal("reset changed volume, pan, bank or program")
		}
		if d.channels[1].expression != 33.0/127 {
			t.Fatal("reset affected another channel")
		}
		if sampled {
			first, held := d.pcmPool[0], d.pcmPool[1]
			if first.keyDown || first.heldByPedal || first.envelope.phase != smafEnvelopeRelease || !held.keyDown {
				t.Fatal("PCM reset failed to release pedal notes or released a held key")
			}
			if first.volume != first.velocity*(47.0/127) || first.step != 8000.0/44100 {
				t.Fatal("PCM reset did not restore sounding volume and pitch")
			}
		} else {
			first, held := d.pool[0], d.pool[1]
			if first.keyDown || first.heldByPedal || first.operators[1].envelope.phase != smafEnvelopeRelease || !held.keyDown {
				t.Fatal("FM reset failed to release pedal notes or released a held key")
			}
			if first.volume != 47.0/127 || first.operators[1].frequency != smafNoteFrequency(60) {
				t.Fatal("FM reset did not restore sounding volume and pitch")
			}
		}
	}
}

func TestSMAFAllSoundOffKillsPCMAndXOFWithChannelIsolation(t *testing.T) {
	for _, sampled := range []bool{false, true} {
		d := audioControlsNativeDecoder(t, sampled, true, []byte{
			0, 0xb0, 64, 127, 0, 0x90, 60, 100, 25,
			0, 0xb1, 64, 127, 0, 0x91, 60, 100, 25,
			50, 0xb0, 120, 0,
		})
		if sampled {
			d.voices[0].pcm.envelope.xof = true
		} else {
			for index := range d.voices[0].patch.operators {
				d.voices[0].patch.operators[index].xof = true
			}
		}
		stream := newSMAFRenderStream(d)
		stream.render(nil, 11025, true)
		muted, other := d.pool[0].active, d.pool[1].active
		if sampled {
			muted, other = d.pcmPool[0].active, d.pcmPool[1].active
		}
		if muted || !other {
			t.Fatalf("sampled=%t: CC120 muted=%t other-channel-active=%t", sampled, muted, other)
		}
	}
}
