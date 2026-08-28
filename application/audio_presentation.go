package application

import (
	"math"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

const publishedAudioRetention = 500 * time.Millisecond

// audioCursorSlack is how far a chunk's guest-time anchor may sit from the
// running output cursor and still count as the same continuous stream.
//
// The mixer decides how many frames an advance produces with a remainder
// accumulator, while the chunk's start index is a truncated guest-time to
// sample conversion. The two answers differ by a sample whenever an advance
// boundary falls between two output frames, which happens constantly once a
// title splits its presentation quantum around a timer. A host that trusts the
// index then sees the stream jump forward or backward by one sample tens of
// times a second and resynchronizes - dropping or re-buffering real audio for
// what is only a rounding difference. Anything inside this window is treated as
// contiguous and given the cursor's index; anything beyond it is a real gap and
// keeps its own anchor.
const audioCursorSlack = time.Millisecond

// DrainPublishedAudio transfers one immutable PCM chunk without taking the
// machine lifecycle lock. The emulation goroutine is the only Media owner; it
// drains Media at a committed service advance and publishes ownership here.
func (m *Machine) DrainPublishedAudio() machinecore.AudioChunk {
	m.audioMu.Lock()
	defer m.audioMu.Unlock()
	if m.publishedAudioHead >= len(m.publishedAudio) {
		return machinecore.AudioChunk{}
	}
	chunk := m.publishedAudio[m.publishedAudioHead]
	m.publishedAudio[m.publishedAudioHead] = machinecore.AudioChunk{}
	m.publishedAudioHead++
	m.publishedAudioSamples -= len(chunk.PCM16)
	if m.publishedAudioHead == len(m.publishedAudio) {
		m.publishedAudio = m.publishedAudio[:0]
		m.publishedAudioHead = 0
	} else if m.publishedAudioHead >= 32 && m.publishedAudioHead*2 >= len(m.publishedAudio) {
		copy(m.publishedAudio, m.publishedAudio[m.publishedAudioHead:])
		m.publishedAudio = m.publishedAudio[:len(m.publishedAudio)-m.publishedAudioHead]
		m.publishedAudioHead = 0
	}
	return chunk
}

// publishAudioFromMedia is called only by the emulation goroutine after a
// successful Services.Advance transaction.
func (m *Machine) publishAudioFromMedia(media *shared.Media, start time.Duration) {
	if media == nil {
		return
	}
	m.publishAudioBuffer(media.Drain(), start)
}

func (m *Machine) publishAudioBuffer(audio shared.AudioBuffer, start time.Duration) {
	if audio.SampleRate <= 0 || audio.Channels <= 0 ||
		len(audio.PCM16) == 0 || len(audio.PCM16)%audio.Channels != 0 {
		return
	}
	if start < 0 {
		start = 0
	}

	m.audioMu.Lock()
	defer m.audioMu.Unlock()
	m.ensureAudioGenerationLocked()
	startNS := int64(start)
	if startNS < m.audioEpochGuestNS {
		m.nextAudioGenerationLocked(start)
	}
	chunk := machinecore.AudioChunk{
		SampleRate:   audio.SampleRate,
		Channels:     audio.Channels,
		PCM16:        audio.PCM16,
		StartGuestNS: startNS,
		StartSample: m.audioStartSampleLocked(
			time.Duration(startNS-m.audioEpochGuestNS),
			audio.SampleRate,
		),
		Generation: m.audioGeneration,
	}
	m.retainPublishedAudioLocked(&chunk)
	if len(chunk.PCM16) == 0 {
		return
	}
	m.audioCursorSample = chunk.StartSample +
		uint64(len(chunk.PCM16)/chunk.Channels)
	m.audioCursorValid = true
	m.publishedAudio = append(m.publishedAudio, chunk)
	m.publishedAudioSamples += len(chunk.PCM16)
}

// audioStartSampleLocked places a chunk on the published sample timeline. It
// keeps the running cursor whenever the guest-time anchor agrees with it to
// within audioCursorSlack, so mixer rounding cannot punch a hole in an
// otherwise unbroken stream.
func (m *Machine) audioStartSampleLocked(
	elapsed time.Duration,
	sampleRate int,
) uint64 {
	anchor := sampleCursor(elapsed, sampleRate)
	if !m.audioCursorValid {
		return anchor
	}
	slack := sampleCursor(audioCursorSlack, sampleRate)
	if anchor >= m.audioCursorSample {
		if anchor-m.audioCursorSample <= slack {
			return m.audioCursorSample
		}
		return anchor
	}
	if m.audioCursorSample-anchor <= slack {
		return m.audioCursorSample
	}
	return anchor
}

func (m *Machine) retainPublishedAudioLocked(chunk *machinecore.AudioChunk) {
	limitFrames := int64(chunk.SampleRate) * int64(publishedAudioRetention) / int64(time.Second)
	if limitFrames < 1 {
		limitFrames = 1
	}
	limitSamples64 := limitFrames * int64(chunk.Channels)
	if limitSamples64 > int64(math.MaxInt) {
		limitSamples64 = int64(math.MaxInt)
	}
	limitSamples := int(limitSamples64)
	if len(chunk.PCM16) > limitSamples {
		dropSamples := len(chunk.PCM16) - limitSamples
		dropSamples -= dropSamples % chunk.Channels
		if dropSamples > 0 {
			m.advanceChunkStart(chunk, dropSamples)
			chunk.PCM16 = chunk.PCM16[dropSamples:]
			m.publishedAudioDropped += uint64(dropSamples)
		}
	}
	for m.publishedAudioHead < len(m.publishedAudio) &&
		m.publishedAudioSamples+len(chunk.PCM16) > limitSamples {
		dropped := m.publishedAudio[m.publishedAudioHead]
		m.publishedAudio[m.publishedAudioHead] = machinecore.AudioChunk{}
		m.publishedAudioHead++
		m.publishedAudioSamples -= len(dropped.PCM16)
		m.publishedAudioDropped += uint64(len(dropped.PCM16))
	}
}

func (m *Machine) advanceChunkStart(chunk *machinecore.AudioChunk, samples int) {
	frames := samples / chunk.Channels
	chunk.StartSample += uint64(frames)
	chunk.StartGuestNS += int64(time.Duration(int64(frames) * int64(time.Second) / int64(chunk.SampleRate)))
}

func (m *Machine) beginAudioGeneration(epoch time.Duration) {
	if epoch < 0 {
		epoch = 0
	}
	m.audioMu.Lock()
	defer m.audioMu.Unlock()
	m.nextAudioGenerationLocked(epoch)
}

func (m *Machine) setAudioGenerationEpoch(epoch time.Duration) {
	if epoch < 0 {
		epoch = 0
	}
	m.audioMu.Lock()
	defer m.audioMu.Unlock()
	m.ensureAudioGenerationLocked()
	m.audioEpochGuestNS = int64(epoch)
	m.audioCursorSample = 0
	m.audioCursorValid = false
}

func (m *Machine) nextAudioGenerationLocked(epoch time.Duration) {
	if m.audioGeneration == 0 || m.audioGeneration == math.MaxUint64 {
		m.audioGeneration = 1
	} else {
		m.audioGeneration++
	}
	m.audioEpochGuestNS = int64(epoch)
	m.audioCursorSample = 0
	m.audioCursorValid = false
	for index := m.publishedAudioHead; index < len(m.publishedAudio); index++ {
		m.publishedAudio[index] = machinecore.AudioChunk{}
	}
	m.publishedAudio = m.publishedAudio[:0]
	m.publishedAudioHead = 0
	m.publishedAudioSamples = 0
}

func (m *Machine) ensureAudioGenerationLocked() {
	if m.audioGeneration == 0 {
		m.audioGeneration = 1
	}
}

func (m *Machine) audioGenerationValue() uint64 {
	m.audioMu.Lock()
	defer m.audioMu.Unlock()
	m.ensureAudioGenerationLocked()
	return m.audioGeneration
}

func sampleCursor(elapsed time.Duration, sampleRate int) uint64 {
	if elapsed <= 0 || sampleRate <= 0 {
		return 0
	}
	seconds := uint64(elapsed / time.Second)
	remainder := uint64(elapsed % time.Second)
	return seconds*uint64(sampleRate) +
		remainder*uint64(sampleRate)/uint64(time.Second)
}

// guestTimeLocked returns the current deterministic guest clock. Callers hold
// m.mu; the audio publication lock is independent.
func (m *Machine) guestTimeLocked() time.Duration {
	switch {
	case m.ktf != nil && m.ktf.Services != nil:
		return m.ktf.Services.Clock.Monotonic()
	case m.wipi != nil && m.wipi.Services != nil:
		return m.wipi.Services.Clock.Monotonic()
	default:
		return 0
	}
}
