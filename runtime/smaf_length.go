package runtime

import "math"

// smafLengthEnvelope walks envelope phases between score events. The bounds
// include rounding from the renderer's repeated additions and multiplications.
// A phase boundary is skipped only when both bounds give the same sample. An
// ambiguous boundary replays this operator with the original sample arithmetic.
// Consequently the probe can skip millions of envelope ticks without changing
// the natural length or the renderer's PCM arithmetic.
type smafLengthEnvelope struct {
	envelope, anchor            smafEnvelope
	lower, upper                float64
	elapsed, anchorAt, keyOffAt uint64
	// Deterministic work counters make cold-path regressions testable without
	// depending on host speed or wall-clock thresholds.
	exactTicks, phaseSteps uint64
}

func newSMAFLengthEnvelope(envelope smafEnvelope) smafLengthEnvelope {
	return smafLengthEnvelope{
		envelope: envelope, anchor: envelope,
		lower: envelope.level, upper: envelope.level,
		keyOffAt: math.MaxUint64,
	}
}

func (probe *smafLengthEnvelope) keyOff(envelope smafEnvelope) {
	probe.keyOffAt = probe.elapsed
	probe.envelope = envelope
	if probe.lower == probe.upper {
		probe.saveAnchor()
	}
}

func (probe *smafLengthEnvelope) saveAnchor() {
	probe.anchor, probe.anchorAt = probe.envelope, probe.elapsed
}

// project bounds the level after samples ticks within the current phase.
func (probe *smafLengthEnvelope) project(samples uint64) (float64, float64) {
	if samples == 0 {
		return probe.lower, probe.upper
	}
	// The standard accumulated floating-point error bound is proportional to
	// the number of operations. Extra headroom covers the closed-form evaluation
	// and math.Pow; all live levels here are far above subnormal values.
	errorBound := 16 * 0x1p-52 * float64(samples+1)
	if probe.envelope.phase == smafEnvelopeAttack {
		step := float64(samples) * probe.envelope.attackStep
		lower, upper := probe.lower+step, probe.upper+step
		return lower - errorBound*math.Abs(lower), upper + errorBound*math.Abs(upper)
	}
	multiplier := probe.envelope.releaseMultiplier
	if probe.envelope.phase == smafEnvelopeDecay {
		multiplier = probe.envelope.decayMultiplier
	} else if probe.envelope.phase == smafEnvelopeSustain {
		multiplier = probe.envelope.sustainMultiplier
	}
	if multiplier == 1 {
		return probe.lower, probe.upper
	}
	factor := math.Pow(multiplier, float64(samples))
	return probe.lower * factor * (1 - errorBound),
		probe.upper * factor * (1 + errorBound)
}

// advance returns the sample count at which the envelope becomes idle, or
// samples when it remains live. Idle envelopes consume no per-sample work.
func (probe *smafLengthEnvelope) advance(samples uint64) uint64 {
	remaining, used := samples, uint64(0)
	for remaining != 0 && probe.envelope.phase != smafEnvelopeIdle {
		probe.phaseSteps++
		attack := probe.envelope.phase == smafEnvelopeAttack
		threshold := 1.0 / 32768
		if probe.envelope.phase == smafEnvelopeDecay {
			threshold = math.Max(threshold, probe.envelope.sustainLevel)
		} else if attack {
			threshold = 1
		}
		level := (probe.lower + probe.upper) * 0.5
		candidate := float64(remaining) + 1
		if attack {
			if probe.envelope.attackStep > 0 {
				candidate = math.Ceil((threshold - level) / probe.envelope.attackStep)
			}
		} else {
			multiplier := probe.envelope.releaseMultiplier
			if probe.envelope.phase == smafEnvelopeDecay {
				multiplier = probe.envelope.decayMultiplier
			} else if probe.envelope.phase == smafEnvelopeSustain {
				multiplier = probe.envelope.sustainMultiplier
			}
			if level <= threshold {
				candidate = 1
			} else if multiplier > 0 && multiplier < 1 {
				candidate = math.Ceil(math.Log(threshold/level) / math.Log(multiplier))
			}
		}
		candidate = math.Max(1, candidate)
		if candidate > float64(remaining) {
			lower, upper := probe.project(remaining)
			if attack && upper >= threshold || !attack && lower <= threshold {
				count := probe.advanceExact(remaining)
				used += count
				remaining -= count
				continue
			}
			probe.lower, probe.upper = lower, upper
			probe.envelope.level = (lower + upper) * 0.5
			probe.elapsed += remaining
			return samples
		}
		count := uint64(candidate)
		lower, upper := probe.project(count)
		previousLower, previousUpper := probe.project(count - 1)
		if attack && (lower < threshold || count > 1 && previousUpper >= threshold) ||
			!attack && (upper > threshold || count > 1 && previousLower <= threshold) {
			count := probe.advanceExact(remaining)
			used += count
			remaining -= count
			continue
		}
		probe.elapsed += count
		used += count
		remaining -= count
		switch probe.envelope.phase {
		case smafEnvelopeAttack:
			probe.envelope.level = 1
			probe.envelope.phase = smafEnvelopeDecay
		case smafEnvelopeDecay:
			probe.envelope.level = probe.envelope.sustainLevel
			probe.envelope.phase = smafEnvelopeRelease
			if probe.envelope.sustaining {
				probe.envelope.phase = smafEnvelopeSustain
			}
			if probe.envelope.level == 0 {
				probe.envelope.phase = smafEnvelopeIdle
			}
		default:
			probe.envelope.level = 0
			probe.envelope.phase = smafEnvelopeIdle
		}
		probe.lower, probe.upper = probe.envelope.level, probe.envelope.level
		probe.saveAnchor()
	}
	probe.elapsed += remaining
	return used
}

// advanceExact resolves an ambiguous phase boundary using sample arithmetic,
// then hands the next phase back to the interval walker. The nearest exact
// anchor avoids replaying earlier phases; constant levels need no replay ticks.
func (probe *smafLengthEnvelope) advanceExact(samples uint64) uint64 {
	envelope := probe.anchor
	for sample := probe.anchorAt; sample < probe.elapsed; sample++ {
		if sample == probe.keyOffAt {
			envelope.keyOff()
		}
		if smafEnvelopeLevelConstant(envelope) {
			if probe.keyOffAt > sample && probe.keyOffAt < probe.elapsed {
				sample = probe.keyOffAt - 1
				continue
			}
			break
		}
		envelope.advance()
		probe.exactTicks++
	}
	if probe.elapsed == probe.keyOffAt {
		envelope.keyOff()
	}
	used := uint64(0)
	phase := envelope.phase
	for used < samples && envelope.phase == phase && phase != smafEnvelopeIdle {
		envelope.advance()
		used++
		probe.exactTicks++
	}
	probe.elapsed += used
	probe.envelope = envelope
	probe.lower, probe.upper = envelope.level, envelope.level
	probe.saveAnchor()
	return used
}

func smafEnvelopeLevelConstant(envelope smafEnvelope) bool {
	switch envelope.phase {
	case smafEnvelopeIdle:
		return true
	case smafEnvelopeAttack:
		return envelope.attackStep == 0 && envelope.level < 1
	case smafEnvelopeDecay:
		return envelope.decayMultiplier == 1 &&
			envelope.level > math.Max(envelope.sustainLevel, 1.0/32768)
	case smafEnvelopeSustain:
		return envelope.sustainMultiplier == 1 && envelope.level > 1.0/32768
	case smafEnvelopeRelease:
		return envelope.releaseMultiplier == 1 && envelope.level > 1.0/32768
	}
	return false
}

func (stream *smafRenderStream) probeFMEnd() uint64 {
	decoder := stream.decoder
	var envelopes [32][4]smafLengthEnvelope
	for stream.cursor < stream.end {
		for stream.eventIndex < len(decoder.events) &&
			decoder.events[stream.eventIndex].sample <= stream.cursor {
			event := decoder.events[stream.eventIndex]
			var generations [32]uint64
			var phases [32][4]uint8
			for slot := range decoder.pool {
				voice := &decoder.pool[slot]
				generations[slot] = voice.generation
				for index := range voice.operators {
					phases[slot][index] = voice.operators[index].envelope.phase
				}
			}
			decoder.fire(event)
			// Pedal release, reset, and channel-mode messages can release several
			// envelopes. A key-off under the pedal changes no envelope phase yet.
			for slot := range decoder.pool {
				voice := &decoder.pool[slot]
				if !voice.active {
					continue
				}
				for index := range envelopes[slot] {
					envelope := voice.operators[index].envelope
					if voice.generation != generations[slot] {
						envelopes[slot][index] = newSMAFLengthEnvelope(envelope)
					} else if envelope.phase != phases[slot][index] {
						envelopes[slot][index].keyOff(envelope)
					}
				}
			}
			stream.eventIndex++
		}
		target := stream.end
		if stream.eventIndex < len(decoder.events) && decoder.events[stream.eventIndex].sample < target {
			target = decoder.events[stream.eventIndex].sample
		}
		remaining := target - stream.cursor
		lastActive := uint64(0)
		for slot := range decoder.pool {
			voice := &decoder.pool[slot]
			if !voice.active {
				continue
			}
			// A note-on can create an active voice with idle operators (AR=0).
			// The sample loop still ticks that voice once before retiring it.
			lastActive = max(lastActive, uint64(1))
			count := 2
			if voice.patch.fourOp {
				count = 4
			}
			for index := 0; index < count; index++ {
				lastActive = max(lastActive, envelopes[slot][index].advance(remaining))
				voice.operators[index].envelope = envelopes[slot][index].envelope
			}
			voice.retireIfIdle(count)
		}
		if stream.eventIndex == len(decoder.events) && lastActive < remaining {
			// The renderer emits one idle sample after the last active tick.
			stream.cursor = min(stream.end, max(stream.cursor+lastActive, decoder.timelineEnd)+1)
			stream.finished = true
			return stream.cursor
		}
		stream.cursor = target
	}
	stream.finished = true
	return stream.cursor
}
