package runtime

import (
	"slices"
	"testing"
	"time"
)

func TestScoreControllerLengthsMatchIncrementalPCM(t *testing.T) {
	for _, control := range []byte{64, 120, 121, 123} {
		track := []byte{0, 0xc0, 16, 0, 0xb0, 64, 127}
		sequence := []byte{0, 0xb0, 64, 127}
		for _, note := range []byte{60, 60, 64, 67} {
			track = append(track, 0, 0x90, note, 100)
			sequence = append(sequence, 0, 0x90, note, 100, 25)
		}
		track = append(track, 96, 0xb0, 123, 0) // key release under pedal at 100 ms
		track = append(track, smfVLQ(3744)...)
		track = append(track, 0xb0, control, 0)
		track = append(track, smfEndOfTrack()...)
		sequence = append(sequence, smfVLQ(1000)...)
		sequence = append(sequence, 0xb0, control, 0, 0, 0xff, 0x2f, 0)
		fm := audioControlsFixedPanFM(15)
		fm[12] &^= 0x20
		fm[20] |= 2 // carrier SUS only; the other operator releases normally
		pcm := smafTestPCMVoice(1, 0, 7999)
		pcm[14] |= 2
		pcm[15] = 0x70
		for _, test := range []struct {
			name string
			file []byte
		}{
			{"SMF", smfFile(0, 480, track)},
			{"SMAF-FM", smafStreamTestFile(smafTestSampledTrack(0, fm, sequence, nil))},
			{"SMAF-PCM", smafStreamTestFile(smafTestSampledTrack(0, pcm, sequence, smafTestPCM16Wave(8000, 1)))},
		} {
			for _, rate := range []uint32{8000, 44100} {
				full := decodeSMAFPCM16(test.file, rate)
				if looksLikeSMF(test.file) {
					full = decodeSMFPCM16(test.file, rate)
				}
				lazy := decodeScoreLazyPCM16(test.file, rate)
				if full == nil || lazy == nil || lazy.smaf == nil {
					t.Fatalf("%s CC%d %d Hz: long score did not decode incrementally", test.name, control, rate)
				}
				if full.duration != lazy.duration {
					t.Fatalf("%s CC%d %d Hz: full duration %s, probed %s", test.name, control, rate, full.duration, lazy.duration)
				}
				for frame := uint64(9973); frame < uint64(len(full.samples)/2); frame += 9973 {
					lazy.ensureFrame(frame)
				}
				lazy.ensureFrame(uint64(rate) * 8)
				if !slices.Equal(full.samples, lazy.samples) {
					t.Fatalf("%s CC%d %d Hz: incremental PCM or endpoint differs", test.name, control, rate)
				}
			}
		}
	}
}

func TestSMFEndOfFileLiftsUnreleasedPedal(t *testing.T) {
	track := []byte{0, 0xc0, 16, 0, 0xb0, 64, 127, 0, 0x90, 60, 100, 96, 0x80, 60, 0}
	track = append(track, smfVLQ(3744)...)
	track = append(track, 0xff, 0x2f, 0)
	file := smfFile(0, 480, track)
	full, lazy := decodeSMFPCM16(file, 8000), decodeSMFLazyPCM16(file, 8000)
	if full == nil || lazy == nil || full.duration < 4*time.Second || full.duration > 5*time.Second || full.duration != lazy.duration {
		t.Fatal("EOF left the pedal tail held until the render ceiling")
	}
	lazy.ensureFrame(8000 * 6)
	if !slices.Equal(full.samples, lazy.samples) {
		t.Fatal("EOF pedal release differs between full and incremental render")
	}
}

func TestSMFEndTimesMergeAcrossTracksAndTempo(t *testing.T) {
	first := append([]byte{0, 0x90, 60, 100}, smfEndOfTrack()...)
	second := append(smfVLQ(960), 0xff, 0x51, 3, 3, 0xd0, 0x90) // 1 s, then 240 BPM
	second = append(second, smfVLQ(2880)...)
	second = append(second, 0xff, 0x2f, 0) // overall end = 2.5 s
	d := decodeSMFEvents(smfFile(1, 480, first, second), 8000)
	if d == nil {
		t.Fatal("multitrack end fixture did not parse")
	}
	found := false
	for _, event := range d.events {
		if event.kind == smafNoteOff {
			found = true
			if event.sample != 20_000 {
				t.Fatalf("track-local end or rest tempo released a shared-channel note at %d, want 20000", event.sample)
			}
		}
	}
	if !found {
		t.Fatal("held note was not released at the overall end")
	}
}

func TestScoreTrailingNOPAndSilentTracksPreserveDuration(t *testing.T) {
	mobile := append([]byte{0, 0x90, 60, 100, 25}, smfVLQ(1000)...)
	mobile = append(mobile, 0xff, 0)
	handy := []byte{0, 0x20, 25, 0x86, 0x68, 0xff, 0} // HPS 1000 * 4 ms NOP
	body := appendSMAFChunk([]byte{0, 0, 2, 2, 0, 0}, []byte("Mtsq"), handy)
	for _, file := range [][]byte{
		smafScore(mobile),
		smafStreamTestFile(appendSMAFChunk(nil, []byte{'M', 'T', 'R', 0}, body)),
		smafScore(append(smfVLQ(1000), 0xff, 0x2f, 0)),
		smfFile(0, 480, append(smfVLQ(3840), 0xff, 0x2f, 0)),
	} {
		assertScoreTrailingRest(t, file)
	}
}

func TestSMFRejectsInvalidSMPTEDivisions(t *testing.T) {
	track := append([]byte{0, 0x90, 60, 100, 96, 0x80, 60, 0}, smfEndOfTrack()...)
	for _, division := range []int{0xe300, 0xef50, 0x8050, 0xff50} {
		if decodeSMFEvents(smfFile(0, division, track), 8000) != nil {
			t.Fatalf("accepted invalid SMPTE division %#x", division)
		}
	}
}

func TestMediaScoreLoopsAfterTrailingRest(t *testing.T) {
	track := append([]byte{0, 0x90, 60, 100, 96, 0x80, 60, 0}, smfVLQ(3744)...)
	track = append(track, 0xff, 0x2f, 0)
	sequence := append([]byte{0, 0x90, 60, 100, 25}, smfVLQ(1000)...)
	sequence = append(sequence, 0xff, 0x2f, 0)
	for _, file := range [][]byte{smfFile(0, 480, track), smafScore(sequence)} {
		limits := DefaultMediaLimits()
		limits.OutputSampleRate, limits.OutputChannels = 8000, 2
		media, err := NewMedia(NewRegistry(32), limits)
		check(t, err)
		clip, err := media.CreateClip(1, "audio/midi", 0)
		check(t, err)
		_, err = media.Append(1, clip, file)
		check(t, err)
		check(t, media.Play(1, clip, 2))
		bus := NewEventBus(32, 32)
		check(t, media.Advance(0, 2*time.Second, bus))
		media.Drain()
		check(t, media.Advance(2*time.Second, 3*time.Second, bus))
		info, err := media.Info(1, clip)
		check(t, err)
		if info.Position != 3*time.Second || info.RemainingPlays != 2 || bus.Len() != 0 {
			t.Fatal("loop restarted before the end rest elapsed")
		}
		for _, sample := range media.Drain().PCM16 {
			if sample != 0 {
				t.Fatal("last retained second of the rest contains a premature loop")
			}
		}
		full := decodeSMAFPCM16(file, 8000)
		if looksLikeSMF(file) {
			full = decodeSMFPCM16(file, 8000)
		}
		check(t, media.Seek(1, clip, info.Duration-durationForFrame(4, 8000)))
		check(t, media.Advance(3*time.Second, 3*time.Second+durationForFrame(12, 8000), bus))
		want := append(slices.Clone(full.samples[len(full.samples)-8:]), full.samples[:16]...)
		if !slices.Equal(media.Drain().PCM16, want) {
			t.Fatal("loop boundary omitted rest frames or restarted the wrong samples")
		}
	}
}
