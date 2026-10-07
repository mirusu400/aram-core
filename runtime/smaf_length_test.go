package runtime

import (
	"math"
	"slices"
	"testing"
	"time"
)

func longSMFScore(seconds, channels int) []byte {
	var track []byte
	for channel := 0; channel < channels; channel++ {
		track = append(track, 0, byte(0x90+channel), byte(48+channel), 100)
	}
	track = append(track, smfVLQ(480*2*seconds)...)
	track = append(track, 0x80, 48, 0)
	for channel := 1; channel < channels; channel++ {
		track = append(track, 0, byte(0x80+channel), byte(48+channel), 0)
	}
	return smfFile(0, 480, append(track, smfEndOfTrack()...))
}

func highNoteSMFScore(seconds, channels int) []byte {
	var track []byte
	for channel := 0; channel < channels; channel++ {
		track = append(track, 0, byte(0xc0+channel), 16,
			0, byte(0x90+channel), byte(96+channel%12), 100)
	}
	track = append(track, smfVLQ(480*2*seconds)...)
	track = append(track, 0x80, 96, 0)
	for channel := 1; channel < channels; channel++ {
		track = append(track, 0, byte(0x80+channel), byte(96+channel%12), 0)
	}
	return smfFile(0, 480, append(track, smfEndOfTrack()...))
}

func sustainedSMAFScore(seconds int) []byte {
	// VM35: KSR=1 AR=15 DR=0. The attack completes in one sample;
	// the decay then holds its exact level until key-off.
	payload := []byte{0x43, 0x05, 0x01, 0, 0, 0, 1, 0}
	for range 2 {
		payload = append(payload, 0x11, 0x70, 0xf6, 0, 0, 0x10, 0)
	}
	sequence := append([]byte{0, 0xf0}, smfVLQ(len(payload))...)
	sequence = append(sequence, payload...)
	sequence = append(sequence, 0, 0x90, 96, 100)
	sequence = append(sequence, smfVLQ(250*seconds)...)
	sequence = append(sequence, smfVLQ(250*seconds)...)
	return smafScore(append(sequence, 0xff, 0x2f))
}

func TestSMAFLengthProbeAmbiguousAttackHasBoundedWork(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		score     []byte
		rate      uint32
		operators []int
	}{
		{"SMF", highNoteSMFScore(120, 1), 8_000, []int{1}},
		{"SMAF", sustainedSMAFScore(120), 44_100, []int{0, 1}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			makeDecoder := func() *smafDecoder {
				if looksLikeSMF(testCase.score) {
					return decodeSMFEvents(testCase.score, testCase.rate)
				}
				decoder := &smafDecoder{rate: testCase.rate}
				if !decoder.parse(testCase.score) || !decoder.buildEvents() {
					t.Fatal("SMAF fixture did not parse")
				}
				return decoder
			}
			decoder := makeDecoder()
			for _, event := range decoder.events {
				if event.sample != 0 {
					break
				}
				decoder.fire(event)
			}
			for _, index := range testCase.operators {
				envelope := decoder.pool[0].operators[index].envelope
				if envelope.attackStep != 1 {
					t.Fatal("fixture needs a one-sample ambiguous attack")
				}
				var short smafLengthEnvelope
				for _, seconds := range []uint64{10, 120} {
					probe := newSMAFLengthEnvelope(envelope)
					probe.advance(uint64(testCase.rate) * seconds)
					if probe.exactTicks > 1 || probe.phaseSteps > 4 {
						t.Fatalf("operator %d %ds hold walked %d exact ticks and %d phases",
							index, seconds, probe.exactTicks, probe.phaseSteps)
					}
					probe.envelope.keyOff()
					probe.keyOff(probe.envelope)
					probe.advance(uint64(testCase.rate) * 2)
					if seconds == 10 {
						short = probe
					} else if probe.exactTicks != short.exactTicks || probe.phaseSteps != short.phaseSteps ||
						probe.envelope.phase != short.envelope.phase {
						t.Fatalf("long hold added replay work: short %d/%d, long %d/%d",
							short.exactTicks, short.phaseSteps, probe.exactTicks, probe.phaseSteps)
					}
				}
			}
			reference := newSMAFRenderStream(makeDecoder())
			reference.render(nil, reference.end, true)
			lazy := decodeScoreLazyPCM16(testCase.score, testCase.rate)
			if lazy == nil || lazy.duration != durationForFrame(reference.cursor, testCase.rate) {
				t.Fatalf("long constant hold duration differs from exact sample walk: %+v, frames %d", lazy, reference.cursor)
			}
		})
	}
}

func TestScoreLazyNaturalLengthForLongScores(t *testing.T) {
	sequence := []byte{0, 0x90, 69, 100}
	sequence = append(sequence, smfVLQ(7_750)...)
	sequence = append(sequence, smfVLQ(7_750)...)
	sequence = append(sequence, 0xff, 0x2f)
	for _, testCase := range []struct {
		name  string
		score []byte
	}{
		{"SMAF31s", smafScore(sequence)},
		{"SMF27s", longSMFScore(27, 16)},
		{"SMF31s", longSMFScore(31, 1)},
		{"SMF120s", longSMFScore(120, 1)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			decoded := decodeScoreLazyPCM16(testCase.score, 8_000)
			if decoded == nil || decoded.smaf == nil {
				t.Fatal("long score must use incremental synthesis")
			}
			if decoded.smaf.cursor != 0 || len(decoded.samples) != 0 {
				t.Fatal("length probe synthesized or consumed the playback stream")
			}
			declared := decoded.duration
			decoded.ensureFrame(decoded.smaf.end)
			if natural := durationForFrame(uint64(len(decoded.samples)/2), 8_000); declared != natural {
				t.Fatalf("declared duration %s, full render duration %s", declared, natural)
			}
		})
	}
}

func TestScoreLongLoopCompletionAndRestore(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 2
	media, err := NewMedia(NewRegistry(32), limits)
	check(t, err)
	bus := NewEventBus(32, 32)
	clip, err := media.CreateClip(1, "audio/midi", 0)
	check(t, err)
	score := longSMFScore(31, 1)
	_, err = media.Append(1, clip, score)
	check(t, err)
	check(t, media.Play(1, clip, 2))
	info, err := media.Info(1, clip)
	check(t, err)
	const tailFrames = uint64(4)
	check(t, media.Seek(1, clip, info.Duration-durationForFrame(tailFrames, 8_000)))
	saved := media.Snapshot()
	registry := NewRegistry(32)
	check(t, registry.Restore(media.registry.Snapshot()))
	restored, err := NewMedia(registry, limits)
	check(t, err)
	check(t, restored.Restore(saved))
	restoredBus := NewEventBus(32, 32)
	step := durationForFrame(12, 8_000)
	check(t, media.Advance(0, step, bus))
	check(t, restored.Advance(0, step, restoredBus))
	got, restoredPCM := media.Drain().PCM16, restored.Drain().PCM16
	if !slices.Equal(got, restoredPCM) {
		t.Fatal("restoring near a long loop changed PCM")
	}
	full := decodeSMFPCM16(score, 8_000)
	want := append(slices.Clone(full.samples[len(full.samples)-int(tailFrames)*2:]), full.samples[:16]...)
	if !slices.Equal(got, want) {
		t.Fatalf("loop boundary produced %v, want %v", got, want)
	}
	info, err = media.Info(1, clip)
	check(t, err)
	if info.RemainingPlays != 1 || info.Position != time.Millisecond {
		t.Fatalf("loop timeline = %+v", info)
	}
	check(t, media.Seek(1, clip, info.Duration-durationForFrame(tailFrames, 8_000)))
	check(t, media.Advance(step, step+time.Millisecond, bus))
	info, err = media.Info(1, clip)
	check(t, err)
	if info.State != ClipStopped || info.Position != info.Duration || bus.Len() != 1 {
		t.Fatalf("completion state = %+v, events = %+v", info, bus.Snapshot().Events)
	}
	if at := bus.Snapshot().Events[0].At; at != step+durationForFrame(tailFrames, 8_000) {
		t.Fatalf("completion at %s, want natural loop endpoint", at)
	}
}

func TestSMAFLengthEnvelopeMatchesSampleArithmetic(t *testing.T) {
	for index := 0; index < 128; index++ {
		patch := smafOpPatch{
			ar: uint8(index % 16), dr: uint8(index / 2 % 16),
			sr: uint8(index / 3 % 16), rr: uint8(index / 5 % 16),
			sl: uint8(index / 7 % 16), ksr: uint8(index % 2),
			egType: index%3 != 0, xof: index%11 == 0,
		}
		var exact smafEnvelope
		exact.configure(patch, 8_000, index%16)
		exact.keyOn()
		probe := newSMAFLengthEnvelope(exact)
		for part, samples := range []uint64{1, 17, 4_003, 120_001, 190_003} {
			if part == 3 {
				exact.keyOff()
				probe.envelope.keyOff()
				probe.keyOff(probe.envelope)
			}
			idleAt := uint64(0)
			for sample := uint64(0); sample < samples; sample++ {
				if exact.phase != smafEnvelopeIdle {
					idleAt = sample + 1
				}
				exact.advance()
			}
			if got := probe.advance(samples); got != idleAt || probe.envelope.phase != exact.phase {
				t.Fatalf("patch %d part %d: idle sample %d/%d, phase %d/%d", index, part,
					got, idleAt, probe.envelope.phase, exact.phase)
			}
		}
	}
}

func TestSMAFLengthEnvelopeAmbiguousBoundaryReplaysExact(t *testing.T) {
	// Repeated 0.1 additions cross 1 on tick 11, while 10*0.1 equals 1.
	envelope := smafEnvelope{
		phase: smafEnvelopeAttack, attackStep: 0.1,
		decayMultiplier: 1, sustainMultiplier: 1, releaseMultiplier: 1,
	}
	probe := newSMAFLengthEnvelope(envelope)
	probe.advance(10)
	if probe.envelope.phase != smafEnvelopeAttack {
		t.Fatal("closed-form rounding changed the attack boundary")
	}
	probe.advance(1)
	if probe.envelope.phase != smafEnvelopeDecay || probe.envelope.level != 1 {
		t.Fatal("attack failed to cross on the exact sample")
	}
	if math.IsNaN(probe.lower) || math.IsNaN(probe.upper) {
		t.Fatal("probe bounds became NaN")
	}
	// A release that lands exactly on the threshold also needs an exact decision,
	// including replay of the key-off sample after the earlier attack interval.
	envelope.releaseMultiplier = 0.5
	probe = newSMAFLengthEnvelope(envelope)
	probe.advance(11)
	probe.envelope.keyOff()
	probe.keyOff(probe.envelope)
	if got := probe.advance(16); got != 15 || probe.envelope.phase != smafEnvelopeIdle {
		t.Fatalf("exact release boundary = %d phase %d, want idle on sample 15", got, probe.envelope.phase)
	}
}

func TestSMFLengthProbeWithAllNotesOff(t *testing.T) {
	track := []byte{0, 0xc0, 16, 0, 0x90, 60, 100, 0, 0x90, 60, 80, 0, 0x91, 64, 100}
	track = append(track, smfVLQ(480*4)...)
	track = append(track, 0xb0, 123, 0) // all notes off on channel 0
	track = append(track, 0, 0xc1, 40, 0, 0x91, 67, 100)
	track = append(track, smfVLQ(480*4)...)
	track = append(track, 0xb1, 120, 0) // all sound off on channel 1
	score := smfFile(0, 480, append(track, smfEndOfTrack()...))
	for _, rate := range []uint32{22_050, 44_100} {
		probe, reference := newSMAFRenderStream(decodeSMFEvents(score, rate)), newSMAFRenderStream(decodeSMFEvents(score, rate))
		reference.render(nil, reference.end, true)
		if got := probe.probeEnd(); got != reference.cursor {
			t.Fatalf("rate %d all notes off: probe %d, sample walk %d", rate, got, reference.cursor)
		}
	}
}

func TestSMAFEventLengthProbeMatchesSampleWalk(t *testing.T) {
	for _, rate := range []uint32{8_000, 22_050, 44_100} {
		for algorithm := uint8(0); algorithm < 8; algorithm++ {
			var patches []smafParsedVoice
			for program := 0; program < 4; program++ {
				patch := defaultSMAFPatch()
				patch.algorithm, patch.fourOp = algorithm, algorithm >= 2
				for operator := range patch.operators {
					patch.operators[operator] = smafOpPatch{
						multi: 1, ar: uint8(8 + program*2), dr: uint8(4 + operator*3),
						sr: uint8(program), rr: uint8(7 + operator*2), sl: uint8(2 + program*4),
						ksr: uint8(operator % 2), egType: program%2 == 0, xof: program == 3,
					}
				}
				patches = append(patches, smafParsedVoice{patch: patch, key: smafVoiceKey{program: program}})
			}
			makeDecoder := func() *smafDecoder {
				decoder := &smafDecoder{rate: rate, voices: patches}
				for channel := range decoder.channels {
					decoder.channels[channel].volume = 1
					decoder.channels[channel].expression = 1
				}
				// More simultaneous notes than slots forces stealing. Repeated
				// pitches on each channel need distinct gates, including releases
				// of notes whose original slot has already been stolen.
				for note := 0; note < 48; note++ {
					channel := note % 4
					decoder.addEvent(smafEvent{kind: smafProgram, channel: channel, a: note % 4})
					decoder.addGatedNote(uint64(note%3), uint64(rate)*(1+uint64(note%5)), channel, 48+note%7, 100)
				}
				decoder.addEvent(smafEvent{sample: uint64(rate) / 3, kind: smafPitchBend, channel: 1, a: 1_200})
				decoder.addEvent(smafEvent{sample: uint64(rate) / 2, kind: smafVolume, channel: 2, a: 40})
				decoder.addEvent(smafEvent{sample: uint64(rate), kind: smafProgram, channel: 3, a: 1})
				decoder.addGatedNote(uint64(rate), uint64(rate)*6, 3, 69, 100)
				sortSMAFEvents(decoder.events)
				return decoder
			}
			probe, reference := newSMAFRenderStream(makeDecoder()), newSMAFRenderStream(makeDecoder())
			reference.render(nil, reference.end, true)
			if got := probe.probeEnd(); got != reference.cursor {
				t.Fatalf("rate %d algorithm %d: probe %d, sample walk %d", rate, algorithm, got, reference.cursor)
			}
		}
	}
	// AR=0 creates a voice that needs one tick to retire even though all its
	// envelopes are already idle. The following idle sample ends the render.
	makeIdleDecoder := func() *smafDecoder {
		return &smafDecoder{
			rate:   8_000,
			voices: []smafParsedVoice{{patch: smafPatch{}}},
			events: []smafEvent{{kind: smafNoteOn, a: 60, b: 100}},
		}
	}
	if got := newSMAFRenderStream(makeIdleDecoder()).probeEnd(); got != 2 {
		t.Fatalf("idle note-on probe = %d, want two rendered frames", got)
	}
}

func BenchmarkColdPolyphonicSMFPlay(b *testing.B) {
	score := longSMFScore(27, 16)
	b.ReportAllocs()
	for b.Loop() {
		media, err := NewMedia(NewRegistry(32), DefaultMediaLimits())
		check(b, err)
		clip, err := media.CreateClip(1, "audio/midi", 0)
		check(b, err)
		_, err = media.Append(1, clip, score)
		check(b, err)
		check(b, media.Play(1, clip, -1))
	}
}

func BenchmarkColdHighNoteSMFPlay(b *testing.B) {
	score := highNoteSMFScore(120, 16)
	b.ReportAllocs()
	for b.Loop() {
		media, err := NewMedia(NewRegistry(32), DefaultMediaLimits())
		check(b, err)
		clip, err := media.CreateClip(1, "audio/midi", 0)
		check(b, err)
		_, err = media.Append(1, clip, score)
		check(b, err)
		check(b, media.Play(1, clip, -1))
	}
}
