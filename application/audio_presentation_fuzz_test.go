package application

import (
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

// FuzzAudioPublicationSchedule checks the frontend timeline independently of
// codec details. Arbitrary sub-frame partitions must stay contiguous, while a
// discontinuity command must start a new generation at sample zero.
func FuzzAudioPublicationSchedule(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7})
	f.Add([]byte{1, 13, 2, 26, 3, 4, 39, 5})
	f.Fuzz(func(t *testing.T, schedule []byte) {
		if len(schedule) > 256 {
			schedule = schedule[:256]
		}
		const rate = 44_100
		machine := &Machine{audioGeneration: 1}
		var elapsed time.Duration
		var remainder uint64
		var next uint64
		var nextValid bool
		for _, command := range schedule {
			if command != 0 && command%13 == 0 {
				machine.beginAudioGeneration(elapsed)
				remainder = 0
				next = 0
				nextValid = false
			}
			delta := time.Duration(command%17+1) * 333_333 * time.Nanosecond
			numerator := remainder + uint64(delta)*rate
			frames := numerator / uint64(time.Second)
			remainder = numerator % uint64(time.Second)
			machine.publishAudioBuffer(shared.AudioBuffer{
				SampleRate: rate,
				Channels:   2,
				PCM16:      make([]int16, frames*2),
			}, elapsed)
			elapsed += delta
			chunk := machine.DrainPublishedAudio()
			if frames == 0 {
				continue
			}
			if chunk.Generation != machine.audioGeneration || len(chunk.PCM16) != int(frames*2) {
				t.Fatalf("invalid published chunk: %+v frames=%d", chunk, frames)
			}
			if nextValid && chunk.StartSample != next {
				t.Fatalf("publication gap/overlap: got=%d want=%d", chunk.StartSample, next)
			}
			next = chunk.StartSample + frames
			nextValid = true
		}
	})
}
