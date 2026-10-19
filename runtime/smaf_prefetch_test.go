package runtime

import (
	"fmt"
	"slices"
	"testing"
)

func TestSMAFPrefetchQuantumTracksRenderWork(t *testing.T) {
	for _, test := range []struct {
		work, frames uint64
	}{
		{0, 2048}, {1, 2048}, {2, 2048},
		{4, 1024}, {8, 512}, {32, 128},
		{64, 64}, {128, 64}, {8192, 64},
	} {
		if got := smafPrefetchFramesForWork(test.work); got != test.frames {
			t.Errorf("work %d: prefetch %d frames, want %d", test.work, got, test.frames)
		}
	}
}

func smafHighWorkDecoder(rate uint32, channels int) *smafDecoder {
	patch := defaultSMAFPatch()
	patch.fourOp, patch.algorithm = true, 4
	patch.operators[2], patch.operators[3] = patch.operators[0], patch.operators[1]
	for index := range patch.operators {
		patch.operators[index].rr = 15
	}
	decoder := &smafDecoder{
		rate: rate, timelineEnd: uint64(rate) * 4,
		voices: []smafParsedVoice{{patch: patch}},
	}
	for index := range decoder.channels {
		decoder.channels[index].volume, decoder.channels[index].expression = 1, 1
	}
	pcm := make([]int16, 64*channels)
	for index := range pcm {
		pcm[index] = int16(index*317 - 20000)
	}
	decoder.tracks = []smafTrack{{number: 0, audio: true, waves: []smafWave{{
		number: 1, sampleRate: int(rate), channels: channels, pcm: pcm,
	}}}}
	decoder.addEvent(smafEvent{kind: smafWaveOn, channel: smafATRChannelBase, a: 1})
	// Include all four operators, even when an operator reaches idle before its
	// siblings: the sounding renderer still calls it until the voice retires.
	for note := range len(decoder.pool) {
		decoder.addGatedNote(0, 400, 0, 48+note, 100)
	}
	sortSMAFEvents(decoder.events)
	return decoder
}

func TestSMAFHighWorkPrefetchPreservesPCMAndRequestedFrames(t *testing.T) {
	for _, rate := range []uint32{8000, 48000, 192000} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%dHz/%dch", rate, channels), func(t *testing.T) {
				makeDecoder := func() *smafDecoder { return smafHighWorkDecoder(rate, channels) }
				probe := newSMAFRenderStream(makeDecoder())
				length := probe.probeEnd()
				if want := uint64(32*4 + resampleTaps*channels); probe.peakRenderWork != want {
					t.Fatalf("peak render work %d, want %d", probe.peakRenderWork, want)
				}
				decoded := finishLazyScoreDecode(makeDecoder(), rate, makeDecoder)
				if decoded == nil || decoded.smaf == nil || decoded.smaf.prefetchFrames != 64 {
					t.Fatalf("high-work score did not select bounded lazy prefetch: %+v", decoded)
				}
				if decoded.duration != durationForFrame(length, rate) {
					t.Fatalf("natural duration %s differs from probe endpoint %d", decoded.duration, length)
				}
				whole := newSMAFRenderStream(makeDecoder())
				want := whole.renderUntil(nil, whole.end)
				decoded.ensureFrame(0)
				if frames := len(decoded.samples) / 2; frames != 64 {
					t.Fatalf("first request speculatively rendered %d frames, want 64", frames)
				}
				if !slices.Equal(decoded.samples, want[:len(decoded.samples)]) {
					t.Fatal("first bounded refill differs from whole render")
				}
				for _, frame := range []uint64{1, 63, 64, 127, 733, 4096, 4097, length / 2} {
					decoded.ensureFrame(frame)
					if frame >= uint64(len(decoded.samples)/2) {
						t.Fatalf("work quantum withheld requested frame %d", frame)
					}
					if !slices.Equal(decoded.samples, want[:len(decoded.samples)]) {
						t.Fatalf("bounded refill ending at frame %d differs from whole render", len(decoded.samples)/2)
					}
				}
				decoded.ensureFrame(length)
				if !slices.Equal(decoded.samples, want) || !decoded.smaf.finished {
					t.Fatal("bounded prefetch changed PCM or natural completion")
				}
				// A fresh decoder (the restore path) makes the same work choice;
				// a large forward seek still renders every frame it requests.
				fresh := finishLazyScoreDecode(makeDecoder(), rate, makeDecoder)
				fresh.ensureFrame(length - 1)
				if fresh.smaf.prefetchFrames != decoded.smaf.prefetchFrames || !slices.Equal(fresh.samples, want) {
					t.Fatal("fresh reconstruction changed work quantum or seek PCM")
				}
			})
		}
	}
}
