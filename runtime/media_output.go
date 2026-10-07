package runtime

import "time"

// mediaAudioSpan describes one continuous part of queuedPCM16. Empty advances
// with no sounding voices leave a gap between spans. Each span has at least one
// frame, so the PCM retention limit also bounds the number of spans.
type mediaAudioSpan struct {
	start      time.Duration
	end        time.Duration
	firstFrame uint64
	samples    int
}

// queueOutput stores mixed samples and their advance-time anchors, retaining
// only complete frames. Prefix removal uses slices rather than copying the
// retained queue each time a slow host falls behind.
func (m *Media) queueOutput(mixed []int64, start, end time.Duration, firstFrame uint64, audible bool) {
	if len(mixed) == 0 {
		// A sub-frame advance can carry its fraction into the next block. It
		// remains continuous only while a decoded, audible voice is active.
		if audible && len(m.queuedAudioSpans) != 0 {
			last := &m.queuedAudioSpans[len(m.queuedAudioSpans)-1]
			if last.end == start {
				last.end = end
			}
		}
		return
	}
	channels := int(m.limits.OutputChannels)
	retention := int(m.retainedFrames()) * channels
	if len(mixed) >= retention {
		m.dropped += uint64(len(m.queuedPCM16) + len(mixed) - retention)
		firstFrame += uint64((len(mixed) - retention) / channels)
		mixed = mixed[len(mixed)-retention:]
		m.queuedPCM16 = m.queuedPCM16[:0]
		m.queuedAudioSpans = m.queuedAudioSpans[:0]
		m.untimedSamples = 0
	} else if overflow := len(m.queuedPCM16) + len(mixed) - retention; overflow > 0 {
		m.dropped += uint64(overflow)
		m.trimAudioSpans(overflow)
		m.queuedPCM16 = m.queuedPCM16[overflow:]
	}
	for _, sample := range mixed {
		m.queuedPCM16 = append(m.queuedPCM16, clampInt16(sample))
	}
	if len(m.queuedAudioSpans) != 0 && firstFrame == 0 {
		last := &m.queuedAudioSpans[len(m.queuedAudioSpans)-1]
		if last.end == start {
			last.end = end
			last.samples += len(mixed)
			return
		}
	}
	m.queuedAudioSpans = append(m.queuedAudioSpans, mediaAudioSpan{
		start: start, end: end, firstFrame: firstFrame, samples: len(mixed),
	})
}

func (m *Media) trimAudioSpans(samples int) {
	untimed := min(samples, m.untimedSamples)
	m.untimedSamples -= untimed
	samples -= untimed
	for samples > 0 && len(m.queuedAudioSpans) != 0 {
		span := &m.queuedAudioSpans[0]
		drop := min(samples, span.samples)
		span.samples -= drop
		span.firstFrame += uint64(drop / int(m.limits.OutputChannels))
		samples -= drop
		if span.samples == 0 {
			m.popAudioSpan()
		}
	}
}

func (m *Media) popAudioSpan() {
	if len(m.queuedAudioSpans) == 1 {
		m.queuedAudioSpans = m.queuedAudioSpans[:0]
	} else {
		m.queuedAudioSpans = m.queuedAudioSpans[1:]
	}
}

// DrainTimed transfers the oldest continuous PCM span and its guest-time
// anchor, recorded when Advance mixed it. The caller owns the returned samples.
// Drain and DrainTimed consume the same queue; use one consumer, not both.
// Restoring a snapshot starts a new output revision and discards untimestamped
// snapshot PCM from this presentation API; legacy Drain still exposes that PCM.
func (m *Media) DrainTimed() (AudioBuffer, time.Duration) {
	if m.untimedSamples != 0 {
		m.consumeQueuedPCM(m.untimedSamples)
		m.untimedSamples = 0
	}
	result := AudioBuffer{
		SampleRate: int(m.limits.OutputSampleRate),
		Channels:   int(m.limits.OutputChannels),
	}
	if len(m.queuedAudioSpans) == 0 {
		return result, 0
	}
	span := m.queuedAudioSpans[0]
	result.PCM16 = append([]int16(nil), m.queuedPCM16[:span.samples]...)
	m.consumeQueuedPCM(span.samples)
	m.popAudioSpan()
	return result, span.start + durationForFrame(span.firstFrame, m.limits.OutputSampleRate)
}

func (m *Media) consumeQueuedPCM(samples int) {
	if samples == len(m.queuedPCM16) {
		m.queuedPCM16 = m.queuedPCM16[:0]
	} else {
		m.queuedPCM16 = m.queuedPCM16[samples:]
	}
}
