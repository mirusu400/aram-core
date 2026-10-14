package runtime

import (
	"fmt"
	"slices"
	"testing"
)

func TestShortScoresDeferSynthesisUntilPlayback(t *testing.T) {
	for _, rate := range []uint32{8_000, 44_100, 192_000} {
		for _, fixture := range []struct {
			name string
			data []byte
		}{
			{"SMAF-FM", smafScore([]byte{0, 0x90, 69, 100, 25, 25, 0xff, 0x2f})},
			{"SMF-FM", smfFile(0, 480, []byte{0, 0x90, 69, 100, 120, 0x80, 69, 0, 0, 0xff, 0x2f, 0})},
			{"SMAF-PCM", smafStreamTestFile(smafTestATR(1,
				[]byte{0, 0, 1, 0x30, 2, 2},
				[]byte{0, 1, 125, 0, 0, 0, 0}, smafTestPCM16(512, 1)))},
		} {
			t.Run(fmt.Sprintf("%s/%dHz", fixture.name, rate), func(t *testing.T) {
				lazy := decodeScoreLazyPCM16(fixture.data, rate)
				if lazy == nil || lazy.smaf == nil || lazy.smaf.cursor != 0 || len(lazy.samples) != 0 {
					t.Fatal("opening a short score synthesized PCM before playback requested it")
				}
				var whole *decodedPCM
				if looksLikeSMF(fixture.data) {
					whole = decodeSMFPCM16(fixture.data, rate)
				} else {
					whole = decodeSMAFPCM16(fixture.data, rate)
				}
				if whole == nil || lazy.duration != whole.duration {
					t.Fatal("deferring synthesis changed the natural duration")
				}
				// Include sub-frame-sized requests and ordinary 60 Hz playback
				// refills, then a seek to the tail and a replay of cached PCM.
				lazy.ensureFrame(0)
				for frame := uint64(1); frame < uint64(len(whole.samples)/2); frame += uint64(rate) / 60 {
					lazy.ensureFrame(frame)
				}
				lazy.ensureFrame(uint64(len(whole.samples) / 2))
				lazy.ensureFrame(0)
				if !slices.Equal(lazy.samples, whole.samples) || !lazy.smaf.finished {
					t.Fatal("incremental short-score PCM differs from whole rendering")
				}
			})
		}
	}
}
