package runtime

// The SMAF FM model in this file is an independent Go adaptation of the
// Apache-2.0 Yamaha SMAF Player by Andreas Wendorf and the MIT-licensed fmFM
// mobile Yamaha synthesizer by but80:
// https://github.com/akustikrausch/yamaha-smaf-player
// https://github.com/but80/fmfm
//
// It intentionally contains no Yamaha sample ROM. Built-in instruments use
// compact ROM-free FM approximations; voices embedded in an MMF use their
// authored operator parameters.

import "math"

const smafTwoPi = 2 * math.Pi

const smafSineTableSize = 4096

var smafSineTable = func() [smafSineTableSize + 1]float64 {
	var table [smafSineTableSize + 1]float64
	for index := range table {
		table[index] = math.Sin(
			smafTwoPi * float64(index) / smafSineTableSize,
		)
	}
	return table
}()

func smafSine(phase float64) float64 {
	phase -= math.Floor(phase)
	position := phase * smafSineTableSize
	index := int(position)
	fraction := position - float64(index)
	return smafSineTable[index] +
		(smafSineTable[index+1]-smafSineTable[index])*fraction
}

type smafOpPatch struct {
	multi, tl, ar, dr, sr, rr, sl uint8
	ksl, ksr, wave, dt, fb        uint8
	dvb, dam                      uint8
	am, vib, egType, xof          bool
}

type smafPatch struct {
	fourOp     bool
	algorithm  uint8
	feedback   uint8
	noteShift  int
	panDefault float64
	lfo        uint8
	operators  [4]smafOpPatch
}

func defaultSMAFPatch() smafPatch {
	patch := smafPatch{}
	patch.operators[0] = smafOpPatch{
		multi: 2, tl: 20, ar: 15, dr: 6, sr: 2, rr: 7, sl: 4,
		egType: true,
	}
	patch.operators[1] = smafOpPatch{
		multi: 1, ar: 15, dr: 4, sr: 1, rr: 7, sl: 2,
		egType: true,
	}
	return patch
}

func gmSMAFPatch(program int) smafPatch {
	if program < 0 {
		program = 0
	}
	if program > 127 {
		program = 127
	}
	patch := defaultSMAFPatch()
	switch program / 8 {
	case 0:
		patch.operators[0].multi = 1
		patch.operators[0].dr = 7
	case 1:
		patch.operators[0].multi = 2
		patch.operators[0].tl = 14
	case 2:
		patch.operators[0].multi = 1
		patch.operators[1].sr = 0
		patch.operators[0].dr = 2
	case 3:
		patch.operators[0].multi = 1
		patch.operators[0].dr = 5
	case 4:
		patch.operators[0].multi = 2
		patch.operators[0].dr = 4
	case 5:
		patch.operators[0].multi = 1
		patch.operators[1].sr = 0
		patch.operators[0].dr = 1
	case 6:
		patch.operators[0].multi = 1
		patch.operators[1].sr = 0
	case 7:
		patch.operators[0].multi = 3
		patch.operators[1].sr = 0
	case 8:
		patch.operators[0].multi = 1
		patch.operators[1].sr = 0
		patch.operators[0].tl = 26
	case 9:
		patch.operators[0].multi = 1
		patch.operators[1].sr = 0
		patch.operators[0].tl = 28
	case 10:
		patch.operators[0].multi = 4
		patch.operators[0].wave = 1
	case 11:
		patch.operators[0].multi = 1
		patch.operators[0].wave = 2
		patch.operators[1].sr = 0
	case 12:
		patch.operators[0].multi = 6
		patch.operators[0].wave = 3
	case 13:
		patch.operators[0].multi = 2
		patch.operators[0].dr = 6
	case 14:
		patch.operators[0].multi = 8
		patch.operators[0].dr = 10
		patch.operators[0].wave = 1
	default:
		patch.operators[0].multi = 12
		patch.operators[0].wave = 4
		patch.operators[0].dr = 12
	}
	return patch
}

func drumSMAFPatch(note int) smafPatch {
	patch := defaultSMAFPatch()
	switch {
	case note < 44:
		patch.operators[0] = smafOpPatch{
			multi: 0, tl: 8, ar: 15, dr: 10, rr: 12, sl: 15,
		}
		patch.operators[1] = smafOpPatch{
			multi: 0, ar: 15, dr: 9, rr: 12, sl: 15, fb: 5,
		}
	case note < 52:
		patch.operators[0] = smafOpPatch{
			multi: 11, tl: 4, ar: 15, dr: 9, rr: 11, sl: 15, fb: 7,
		}
		patch.operators[1] = smafOpPatch{
			multi: 1, tl: 2, ar: 15, dr: 8, rr: 11, sl: 15,
		}
	default:
		patch.operators[0] = smafOpPatch{
			multi: 15, tl: 6, ar: 15, dr: 12, rr: 13, sl: 15, fb: 7,
		}
		patch.operators[1] = smafOpPatch{
			multi: 9, tl: 8, ar: 15, dr: 12, rr: 13, sl: 15,
		}
	}
	return patch
}

type smafEnvelope struct {
	phase                              uint8
	level                              float64
	attackStep                         float64
	decayMultiplier, sustainMultiplier float64
	releaseMultiplier, sustainLevel    float64
	ignoreKeyOff                       bool
}

const (
	smafEnvelopeIdle uint8 = iota
	smafEnvelopeAttack
	smafEnvelopeDecay
	smafEnvelopeSustain
	smafEnvelopeRelease
)

var smafAttackSecondsAtRateOne = [2][9]float64{
	{3.07068, 3.07068, 3.07068, 2.45670, 2.45670, 2.04699, 2.04699, 1.75471, 1.75471},
	{3.07082, 2.45660, 1.75489, 1.22816, 0.87737, 0.61414, 0.43876, 0.30714, 0.21935},
}

var smafDecayDBPerSecondAtRateFour = [2][16]float64{
	{17.9342, 17.9342, 17.9342, 17.9342, 17.9342, 22.4116, 22.4116, 22.4116, 22.4116, 26.9076, 26.9076, 26.9076, 26.9076, 31.3661, 31.3661, 31.3661},
	{17.9465, 22.4376, 22.4376, 31.4026, 31.4026, 44.8696, 44.8696, 62.7959, 62.7959, 89.6707, 89.6707, 125.5546, 125.5546, 179.2684, 179.2684, 250.9128},
}

func smafAttackStep(
	rate uint8,
	ksr uint8,
	keyScaleNumber int,
	sampleRate float64,
) float64 {
	if rate == 0 {
		return 0
	}
	index := (keyScaleNumber >> 1) + (keyScaleNumber & 1)
	index = max(0, min(index, 8))
	seconds := smafAttackSecondsAtRateOne[ksr&1][index] /
		float64(uint64(1)<<uint(rate-1))
	return 1 / math.Max(1, seconds*sampleRate)
}

func smafDecayMultiplier(
	rate uint8,
	ksr uint8,
	keyScaleNumber int,
	sampleRate float64,
) float64 {
	if rate == 0 {
		return 1
	}
	keyScaleNumber = max(0, min(keyScaleNumber, 15))
	dbPerSecond := smafDecayDBPerSecondAtRateFour[ksr&1][keyScaleNumber] / 2
	dbPerSample := dbPerSecond *
		float64(uint64(1)<<uint(rate)) / 16 / sampleRate
	return math.Pow(10, -dbPerSample/10)
}

func (envelope *smafEnvelope) configure(
	patch smafOpPatch,
	sampleRate float64,
	keyScaleNumber int,
) {
	envelope.attackStep = smafAttackStep(
		patch.ar,
		patch.ksr,
		keyScaleNumber,
		sampleRate,
	)
	envelope.decayMultiplier = smafDecayMultiplier(
		patch.dr,
		patch.ksr,
		keyScaleNumber,
		sampleRate,
	)
	envelope.sustainMultiplier = smafDecayMultiplier(
		patch.sr,
		patch.ksr,
		keyScaleNumber,
		sampleRate,
	)
	envelope.releaseMultiplier = smafDecayMultiplier(
		patch.rr,
		patch.ksr,
		keyScaleNumber,
		sampleRate,
	)
	if patch.sl >= 15 {
		envelope.sustainLevel = 0
	} else {
		envelope.sustainLevel = math.Pow(10, -3*float64(patch.sl)/20)
	}
	envelope.ignoreKeyOff = patch.xof
	envelope.phase = smafEnvelopeIdle
	envelope.level = 0
}

func (envelope *smafEnvelope) keyOn() {
	if envelope.attackStep <= 0 {
		envelope.phase = smafEnvelopeIdle
		envelope.level = 0
		return
	}
	envelope.phase = smafEnvelopeAttack
}

func (envelope *smafEnvelope) keyOff() {
	if envelope.ignoreKeyOff {
		return
	}
	if envelope.phase != smafEnvelopeIdle {
		envelope.phase = smafEnvelopeRelease
	}
}

func (envelope *smafEnvelope) advance() float64 {
	switch envelope.phase {
	case smafEnvelopeIdle:
		return 0
	case smafEnvelopeAttack:
		envelope.level += envelope.attackStep
		if envelope.level >= 1 {
			envelope.level = 1
			envelope.phase = smafEnvelopeDecay
		}
	case smafEnvelopeDecay:
		envelope.level *= envelope.decayMultiplier
		if envelope.level <= envelope.sustainLevel ||
			envelope.level <= 1.0/32768 {
			envelope.level = envelope.sustainLevel
			envelope.phase = smafEnvelopeSustain
			if envelope.level == 0 {
				envelope.phase = smafEnvelopeIdle
			}
		}
	case smafEnvelopeSustain:
		envelope.level *= envelope.sustainMultiplier
		if envelope.level <= 1.0/32768 {
			envelope.level = 0
			envelope.phase = smafEnvelopeIdle
		}
	case smafEnvelopeRelease:
		envelope.level *= envelope.releaseMultiplier
		if envelope.level <= 1.0/32768 {
			envelope.level = 0
			envelope.phase = smafEnvelopeIdle
		}
	}
	return envelope.level
}

type smafOperator struct {
	patch                 smafOpPatch
	envelope              smafEnvelope
	sampleRate, frequency float64
	phase, phaseIncrement float64
	totalGain, keyGain    float64
	keyScaleNumber        int
	vibrato, tremolo      float64
	nyquistMuted          bool
}

func (operator *smafOperator) configure(
	patch smafOpPatch,
	sampleRate, frequency float64,
) {
	operator.patch = patch
	operator.sampleRate = sampleRate
	if patch.tl >= 63 {
		operator.totalGain = 0
	} else {
		operator.totalGain = math.Pow(10, -0.75*float64(patch.tl)/20)
	}
	vibratoDepth := [...]float64{0.00196, 0.00387, 0.00774, 0.01548}
	tremoloDepth := [...]float64{0.129, 0.242, 0.424, 0.669}
	if patch.vib {
		operator.vibrato = vibratoDepth[patch.dvb&3]
	} else {
		operator.vibrato = 0
	}
	if patch.am {
		operator.tremolo = tremoloDepth[patch.dam&3]
	} else {
		operator.tremolo = 0
	}
	operator.frequency = frequency
	operator.recalculate()
	operator.envelope.configure(
		patch,
		sampleRate,
		operator.keyScaleNumber,
	)
}

var smafMultiplier = [...]float64{
	0.5, 1, 2, 3, 4, 5, 6, 7,
	8, 9, 10, 10, 12, 12, 15, 15,
}

var smafDetuneHz = [8][16]float64{
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, .05, .05, .05, .05, .09, .09, .14, .14, .18, .23, .27, .32, .37, .37},
	{.05, .05, .09, .09, .14, .14, .18, .23, .27, .32, .41, .46, .59, .64, .73, .73},
	{.09, .09, .14, .14, .18, .23, .28, .32, .41, .46, .59, .64, .87, .91, 1, 1},
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{0, 0, -.05, -.05, -.05, -.05, -.09, -.09, -.14, -.14, -.18, -.23, -.27, -.32, -.37, -.37},
	{-.05, -.05, -.09, -.09, -.14, -.14, -.18, -.23, -.27, -.32, -.41, -.46, -.59, -.64, -.73, -.73},
	{-.09, -.09, -.14, -.14, -.18, -.23, -.28, -.32, -.41, -.46, -.59, -.64, -.87, -.91, -1, -1},
}

func smafFrequencyParameters(frequency float64) (int, int, int) {
	note := 69 + 12*math.Log2(math.Max(frequency, 1)/440)
	block := int(math.Floor((note + 3 - 12) / 12))
	block = max(0, min(block, 7))
	const frequencyNumberCoefficient = float64(1<<19) / 48_000 * 0.5
	divisor := math.Pow(2, float64(block-2))
	fnum := int(math.Floor(frequency*frequencyNumberCoefficient/divisor + 0.5))
	for fnum > 1024 && block < 7 {
		block++
		fnum = (fnum + 1) >> 1
	}
	fnum = max(0, min(fnum, 1024))
	keyScaleNumber := block*2 + (fnum >> 9)
	return fnum, block, max(0, min(keyScaleNumber, 15))
}

func smafKeyScaleGain(ksl uint8, block, fnum int) float64 {
	ksl &= 3
	if ksl == 0 {
		return 1
	}
	bases := [...]float64{0, .08, 1.0 / 15, 1.0 / 15}
	blockCoefficients := [...]float64{0, 3, 1.5, 6.01}
	fnumCoefficients := [...]float64{0, .38, .185, .75}
	fnum5 := min(fnum>>5, 15)
	db := bases[ksl] -
		blockCoefficients[ksl]*float64(block-2) -
		fnumCoefficients[ksl]*float64(fnum5-7)
	if block < 2 || db >= 0 {
		return 1
	}
	return math.Pow(10, db/20)
}

func (operator *smafOperator) recalculate() {
	fnum, block, keyScaleNumber := smafFrequencyParameters(operator.frequency)
	operator.keyScaleNumber = keyScaleNumber
	detune := smafDetuneHz[operator.patch.dt&7][keyScaleNumber]
	operator.phaseIncrement = (operator.frequency + detune) *
		smafMultiplier[operator.patch.multi&15] / operator.sampleRate
	operator.keyGain = smafKeyScaleGain(operator.patch.ksl, block, fnum)
	operator.nyquistMuted = operator.phaseIncrement >= 0.5
}

func (operator *smafOperator) noteOn(frequency float64) {
	operator.frequency = frequency
	operator.phase = 0
	operator.recalculate()
	operator.envelope.keyOn()
}

func (operator *smafOperator) noteOff() {
	operator.envelope.keyOff()
}

func (operator *smafOperator) setFrequency(frequency float64) {
	operator.frequency = frequency
	operator.recalculate()
}

func (operator *smafOperator) tick(modulation, lfo float64) float64 {
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
	amplitude := 1.0
	if operator.tremolo != 0 {
		amplitude -= operator.tremolo * (0.5 + 0.5*lfo)
	}
	return output * operator.totalGain * operator.keyGain * envelope * amplitude
}

func smafWaveform(wave uint8, phase float64) float64 {
	x := phase - math.Floor(phase)
	switch wave & 31 {
	case 0:
		return smafSine(x)
	case 1:
		return smafHalfWave(smafSine, x)
	case 2:
		return smafAbsoluteWave(smafSine, x)
	case 3:
		return smafQuarterWave(smafSine, x)
	case 4:
		return smafDoubleHalfWave(smafSine, x)
	case 5:
		return smafDoubleAbsoluteWave(smafSine, x)
	case 6:
		if x < 0.5 {
			return 1
		}
		return -1
	case 7:
		index := int(x * 1024)
		if index < 512 {
			return math.Pow(2, -float64(index)/16)
		}
		return -math.Pow(2, -float64(1024-index)/16)
	case 8:
		return smafClippedSine(x)
	case 9:
		return smafHalfWave(smafClippedSine, x)
	case 10:
		return smafAbsoluteWave(smafClippedSine, x)
	case 11:
		return smafQuarterWave(smafClippedSine, x)
	case 12:
		return smafDoubleHalfWave(smafClippedSine, x)
	case 13:
		return smafDoubleAbsoluteWave(smafClippedSine, x)
	case 14:
		if x < 0.5 {
			return 1
		}
		return 0
	case 16:
		return smafTriangle(x)
	case 17:
		return smafHalfWave(smafTriangle, x)
	case 18:
		return smafAbsoluteWave(smafTriangle, x)
	case 19:
		return smafQuarterWave(smafTriangle, x)
	case 20:
		return smafDoubleHalfWave(smafTriangle, x)
	case 21:
		return smafDoubleAbsoluteWave(smafTriangle, x)
	case 22:
		if x < 0.25 || x >= 0.5 && x < 0.75 {
			return 1
		}
		return 0
	case 24:
		return smafSaw(x)
	case 25:
		return smafHalfWave(smafSaw, x)
	case 26:
		return smafAbsoluteWave(smafSaw, x)
	case 27:
		return smafQuarterWave(smafSaw, x)
	case 28:
		return smafDoubleHalfWave(smafSaw, x)
	case 29:
		return smafDoubleAbsoluteWave(smafSaw, x)
	case 30:
		if x < 0.25 {
			return 1
		}
		if x < 0.5 {
			return -1
		}
		return 0
	default:
		return 0
	}
}

func smafHalfWave(base func(float64) float64, phase float64) float64 {
	if phase < 0.5 {
		return base(phase)
	}
	return 0
}

func smafAbsoluteWave(base func(float64) float64, phase float64) float64 {
	if phase >= 0.5 {
		phase -= 0.5
	}
	return base(phase)
}

func smafQuarterWave(base func(float64) float64, phase float64) float64 {
	if phase >= 0.5 {
		phase -= 0.5
	}
	if phase < 0.25 {
		return base(phase)
	}
	return 0
}

func smafDoubleHalfWave(base func(float64) float64, phase float64) float64 {
	if phase < 0.5 {
		return base(phase * 2)
	}
	return 0
}

func smafDoubleAbsoluteWave(base func(float64) float64, phase float64) float64 {
	if phase >= 0.5 {
		return 0
	}
	if phase >= 0.25 {
		phase -= 0.25
	}
	return base(phase * 2)
}

func smafClippedSine(phase float64) float64 {
	return math.Max(-1, math.Min(1, smafSine(phase)*math.Sqrt2))
}

func smafTriangle(phase float64) float64 {
	phase -= math.Floor(phase)
	scaled := phase * 4
	if scaled < 1 {
		return scaled
	}
	if scaled < 3 {
		return 2 - scaled
	}
	return scaled - 4
}

func smafSaw(phase float64) float64 {
	phase -= math.Floor(phase)
	if phase < 0.5 {
		return phase * 2
	}
	return phase*2 - 2
}

type smafVoice struct {
	patch                  smafPatch
	operators              [4]smafOperator
	feedbackMemory         [4][2]float64
	operatorFeedback       [4]uint8
	sampleRate             float64
	velocity, volume       float64
	lfoPhase, lfoStep      float64
	channel, note, keyNote int
	pan                    float64
	active                 bool
}

func (voice *smafVoice) noteOn(
	patch smafPatch,
	frequency, velocity float64,
) {
	voice.patch = patch
	voice.velocity = math.Max(0, math.Min(1, velocity))
	for index := range voice.operators {
		voice.feedbackMemory[index] = [2]float64{}
		voice.operatorFeedback[index] = patch.operators[index].fb
	}
	if patch.feedback != 0 && voice.operatorFeedback[0] == 0 {
		voice.operatorFeedback[0] = patch.feedback
	}
	lfoRates := [...]float64{1.8, 4, 6, 9.7}
	voice.lfoStep = lfoRates[patch.lfo&3] / voice.sampleRate
	voice.lfoPhase = 0
	operatorCount := 2
	if patch.fourOp {
		operatorCount = 4
	}
	for index := 0; index < operatorCount; index++ {
		voice.operators[index].configure(
			patch.operators[index],
			voice.sampleRate,
			frequency,
		)
		voice.operators[index].noteOn(frequency)
	}
	voice.active = true
}

func (voice *smafVoice) noteOff() {
	count := 2
	if voice.patch.fourOp {
		count = 4
	}
	for index := 0; index < count; index++ {
		voice.operators[index].noteOff()
	}
}

func (voice *smafVoice) setFrequency(frequency float64) {
	count := 2
	if voice.patch.fourOp {
		count = 4
	}
	for index := 0; index < count; index++ {
		voice.operators[index].setFrequency(frequency)
	}
}

func (voice *smafVoice) modOperator(
	index int,
	external, lfo float64,
) float64 {
	feedback := 0.0
	if voice.operatorFeedback[index] != 0 {
		memory := voice.feedbackMemory[index]
		feedbackDepth := [...]float64{
			0, 1.0 / 32, 1.0 / 16, 1.0 / 8,
			1.0 / 4, 1.0 / 2, 1, 2,
		}
		feedback = (memory[0] + memory[1]) * 0.5 *
			feedbackDepth[voice.operatorFeedback[index]&7]
	}
	output := voice.operators[index].tick(external+feedback, lfo)
	voice.feedbackMemory[index][1] = voice.feedbackMemory[index][0]
	voice.feedbackMemory[index][0] = output
	return output
}

func (voice *smafVoice) tick() float64 {
	if !voice.active {
		return 0
	}
	voice.lfoPhase += voice.lfoStep
	if voice.lfoPhase >= 1 {
		voice.lfoPhase--
	}
	lfo := smafSine(voice.lfoPhase)
	// Yamaha's mobile FM engine expresses the modulator output in waveform
	// cycles. Its authored patches assume this four-cycle full-scale depth.
	const modulationDepth = 4.0
	var output float64
	switch voice.patch.algorithm & 7 {
	case 0:
		a := voice.modOperator(0, 0, lfo)
		output = voice.modOperator(1, a*modulationDepth, lfo)
	case 1:
		a := voice.modOperator(0, 0, lfo)
		b := voice.modOperator(1, 0, lfo)
		output = a + b
	case 2:
		a := voice.modOperator(0, 0, lfo)
		b := voice.modOperator(1, 0, lfo)
		c := voice.modOperator(2, 0, lfo)
		d := voice.modOperator(3, 0, lfo)
		output = a + b + c + d
	case 3:
		a := voice.modOperator(0, 0, lfo)
		b := voice.modOperator(1, 0, lfo)
		c := voice.modOperator(2, b*modulationDepth, lfo)
		output = voice.modOperator(3, (a+c)*modulationDepth, lfo)
	case 4:
		a := voice.modOperator(0, 0, lfo)
		b := voice.modOperator(1, a*modulationDepth, lfo)
		c := voice.modOperator(2, b*modulationDepth, lfo)
		output = voice.modOperator(3, c*modulationDepth, lfo)
	case 5:
		a := voice.modOperator(0, 0, lfo)
		b := voice.modOperator(1, a*modulationDepth, lfo)
		c := voice.modOperator(2, 0, lfo)
		d := voice.modOperator(3, c*modulationDepth, lfo)
		output = b + d
	case 6:
		a := voice.modOperator(0, 0, lfo)
		b := voice.modOperator(1, 0, lfo)
		c := voice.modOperator(2, b*modulationDepth, lfo)
		d := voice.modOperator(3, c*modulationDepth, lfo)
		output = a + d
	default:
		a := voice.modOperator(0, 0, lfo)
		b := voice.modOperator(1, 0, lfo)
		c := voice.modOperator(2, b*modulationDepth, lfo)
		d := voice.modOperator(3, 0, lfo)
		output = a + c + d
	}
	count := 2
	if voice.patch.fourOp {
		count = 4
	}
	live := false
	for index := 0; index < count; index++ {
		if voice.operators[index].envelope.phase != smafEnvelopeIdle {
			live = true
			break
		}
	}
	if !live {
		voice.active = false
	}
	return output * voice.velocity * voice.volume * 0.7
}
