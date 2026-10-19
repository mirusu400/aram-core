package runtime

import (
	"fmt"
	"math"
	"testing"
)

// smafGeneralOperatorTick is the pre-fast-path tick, deliberately using the
// general waveform dispatcher for every wave. Keep the order of every float
// operation literal: this is a bit oracle, not an approximate audio comparison.
func smafGeneralOperatorTick(operator *smafOperator, modulation, lfo float64) float64 {
	envelope := operator.envelope.advance()
	if operator.nyquistMuted {
		return 0
	}
	increment := operator.phaseIncrement
	if operator.vibrato != 0 {
		increment *= 1 + operator.vibrato*lfo
	}
	operator.phase += increment
	operator.phase -= math.Floor(operator.phase)
	output := smafWaveform(operator.patch.wave, operator.phase+modulation)
	value := output * operator.totalGain * operator.keyGain * envelope
	if operator.tremolo != 0 {
		value *= 1 - operator.tremolo*(0.5+0.5*lfo)
	}
	return value
}

// All fields of the operator and its nested envelope are represented here.
// Comparing float bits also distinguishes negative zero and NaN payloads,
// which a struct equality or reflect.DeepEqual comparison cannot do.
type smafOperatorBitState struct {
	patch          smafOpPatch
	phase          uint8
	ignoreKeyOff   bool
	sustaining     bool
	keyScaleNumber int
	nyquistMuted   bool
	floats         [14]uint64
}

func smafOperatorBits(operator *smafOperator) smafOperatorBitState {
	return smafOperatorBitState{
		patch: operator.patch, phase: operator.envelope.phase,
		ignoreKeyOff: operator.envelope.ignoreKeyOff, sustaining: operator.envelope.sustaining,
		keyScaleNumber: operator.keyScaleNumber, nyquistMuted: operator.nyquistMuted,
		floats: [14]uint64{
			math.Float64bits(operator.envelope.level),
			math.Float64bits(operator.envelope.attackStep),
			math.Float64bits(operator.envelope.decayMultiplier),
			math.Float64bits(operator.envelope.sustainMultiplier),
			math.Float64bits(operator.envelope.releaseMultiplier),
			math.Float64bits(operator.envelope.sustainLevel),
			math.Float64bits(operator.sampleRate),
			math.Float64bits(operator.frequency),
			math.Float64bits(operator.phase),
			math.Float64bits(operator.phaseIncrement),
			math.Float64bits(operator.totalGain),
			math.Float64bits(operator.keyGain),
			math.Float64bits(operator.vibrato),
			math.Float64bits(operator.tremolo),
		},
	}
}

func smafOperatorOraclePatch(wave, flags int) smafOpPatch {
	return smafOpPatch{
		wave: uint8(wave), multi: uint8(wave & 15), tl: uint8(wave & 63),
		ar: 15, dr: 12, sr: 3, rr: 9, sl: 6,
		ksl: uint8(wave & 3), ksr: uint8(wave & 1), dt: uint8(wave & 7),
		fb: uint8(wave>>3) & 7, dvb: 3, dam: 3,
		am: flags&1 != 0, vib: flags&2 != 0, egType: true, sus: true,
	}
}

func TestSMAFOperatorDirectSineMatchesGeneralTick(t *testing.T) {
	modulations := []float64{0, math.Copysign(0, -1), -1e-300, 0.25, -0.25, 1, -1, 1e100, -1e100}
	for _, rate := range []float64{8000, 44100, 48000, 192000} {
		for flags := range 4 {
			t.Run(fmt.Sprintf("%gHz/modulation%d", rate, flags), func(t *testing.T) {
				// All 32 waves and every masked uint8 alias, not just sine 0.
				for wave := range 256 {
					var got smafOperator
					got.configure(smafOperatorOraclePatch(wave, flags), rate, 440)
					got.noteOn(440)
					want := got
					for sample := range 96 {
						switch sample {
						case 16, 48:
							got.noteOff()
							want.noteOff()
						case 24:
							got.setFrequency(rate * 2) // Nyquist mute, with live envelope.
							want.setFrequency(rate * 2)
						case 32:
							got.noteOn(0.01) // Negative detune can produce a negative increment.
							want.noteOn(0.01)
						case 64:
							got.noteOn(440)
							want.noteOn(440)
						case 80:
							got.setFrequency(33)
							want.setFrequency(33)
						}
						modulation := modulations[sample%len(modulations)]
						lfo := float64(sample%17-8) / 8
						value, reference := got.tick(modulation, lfo), smafGeneralOperatorTick(&want, modulation, lfo)
						if math.Float64bits(value) != math.Float64bits(reference) || smafOperatorBits(&got) != smafOperatorBits(&want) {
							t.Fatalf("wave %d sample %d: output bits %016x/%016x, state %+v/%+v", wave, sample,
								math.Float64bits(value), math.Float64bits(reference), smafOperatorBits(&got), smafOperatorBits(&want))
						}
					}
				}
			})
		}
	}
}

func TestSMAFOperatorDirectSineExtremeFloatState(t *testing.T) {
	negativeZero := math.Copysign(0, -1)
	payloadNaN := math.Float64frombits(0x7ff8abcd01234567)
	negativeNaN := math.Float64frombits(0xfff8abcd01234567)
	for _, rate := range []float64{8000, 44100, 48000, 192000} {
		for flags := range 4 {
			t.Run(fmt.Sprintf("%gHz/modulation%d", rate, flags), func(t *testing.T) {
				for wave := range 256 {
					for _, edge := range []struct {
						name                              string
						phase, increment, modulation, lfo float64
					}{
						{"zero", 0, 0, 0, 0},
						{"negative_zero", negativeZero, negativeZero, negativeZero, negativeZero},
						{"tiny_negative_modulation", 0, 0, -math.SmallestNonzeroFloat64, 1},
						{"negative_increment", 0, -1e-8, 0, -1},
						{"phase_wrap", math.Nextafter(1, 0), 0.125, -0.5, 0.25},
						{"large_phase", math.MaxFloat64, math.MaxFloat64, 0, 0},
						{"large_modulation", 0.25, 0, math.MaxFloat64, 1},
						{"negative_large_modulation", 0.25, 0, -math.MaxFloat64, -1},
						{"infinite_modulation", 0.25, 0, math.Inf(1), 0},
						{"negative_infinite_modulation", 0.25, 0, math.Inf(-1), 0},
						{"nan_modulation", 0.25, 0, payloadNaN, 0},
						{"infinite_lfo", 0.25, 0.125, 0.25, math.Inf(1)},
						{"negative_infinite_lfo", 0.25, 0.125, 0.25, math.Inf(-1)},
						{"nan_lfo", 0.25, 0.125, 0.25, negativeNaN},
						{"nan_phase", payloadNaN, 0.125, 0, 0},
					} {
						for _, phase := range []uint8{smafEnvelopeIdle, smafEnvelopeAttack, smafEnvelopeDecay, smafEnvelopeSustain, smafEnvelopeRelease} {
							var got smafOperator
							got.configure(smafOperatorOraclePatch(wave, flags), rate, 440)
							got.phase, got.phaseIncrement, got.nyquistMuted = edge.phase, edge.increment, false
							got.envelope.phase, got.envelope.level = phase, 0.5
							want := got
							value, reference := got.tick(edge.modulation, edge.lfo), smafGeneralOperatorTick(&want, edge.modulation, edge.lfo)
							if math.Float64bits(value) != math.Float64bits(reference) || smafOperatorBits(&got) != smafOperatorBits(&want) {
								t.Fatalf("wave %d %s envelope %d: output bits %016x/%016x, state %+v/%+v", wave, edge.name, phase,
									math.Float64bits(value), math.Float64bits(reference), smafOperatorBits(&got), smafOperatorBits(&want))
							}
						}
					}
				}
			})
		}
	}
}

var smafOperatorOracleBenchmarkSink float64

// The additional common-sine branch must also be checked on the other 31
// waveforms. Both variants use the same persistent state and phase sequence;
// the general variant is the literal pre-optimization tick above.
func BenchmarkSMAFOperatorWaveDispatch(b *testing.B) {
	makeOperator := func(wave int) smafOperator {
		var operator smafOperator
		patch := smafOperatorOraclePatch(wave, 0)
		patch.dr, patch.sr, patch.sl = 0, 0, 0
		operator.configure(patch, 44100, 440)
		operator.noteOn(440)
		return operator
	}
	for wave := range 32 {
		b.Run(fmt.Sprintf("wave%02d/direct", wave), func(b *testing.B) {
			operator := makeOperator(wave)
			var output float64
			for b.Loop() {
				output = operator.tick(0.25, 0.5)
			}
			smafOperatorOracleBenchmarkSink = output
		})
		b.Run(fmt.Sprintf("wave%02d/general", wave), func(b *testing.B) {
			operator := makeOperator(wave)
			var output float64
			for b.Loop() {
				output = smafGeneralOperatorTick(&operator, 0.25, 0.5)
			}
			smafOperatorOracleBenchmarkSink = output
		})
	}
}
