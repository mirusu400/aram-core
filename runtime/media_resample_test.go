package runtime

import (
	"math"
	"math/cmplx"
	"testing"
	"time"
)

// toneMagnitude reports the amplitude of one frequency in a mono signal.
func toneMagnitude(samples []float64, rate, frequency float64) float64 {
	radians := 2 * math.Pi * frequency / rate
	var accumulator complex128
	for index, value := range samples {
		accumulator += complex(value, 0) *
			cmplx.Exp(complex(0, -radians*float64(index)))
	}
	return cmplx.Abs(accumulator) / float64(len(samples))
}

// renderResampledTone plays a sine recorded at sourceRate through the mixer at
// its default 44.1 kHz output and returns the left channel.
func renderResampledTone(
	t *testing.T,
	sourceRate uint32,
	frequency float64,
) []float64 {
	t.Helper()
	source := make([]int16, int(sourceRate)*2)
	for index := range source {
		source[index] = int16(20_000 * math.Sin(
			2*math.Pi*frequency*float64(index)/float64(sourceRate),
		))
	}
	media, err := NewMedia(NewRegistry(32), DefaultMediaLimits())
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(16, 32)
	clip, err := media.CreateClip(3, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(
		3, clip, pcmWave(sourceRate, 1, source),
	); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(3, clip, 1); err != nil {
		t.Fatal(err)
	}
	if err := media.Advance(0, time.Second, bus); err != nil {
		t.Fatal(err)
	}
	audio := media.Drain()
	if len(audio.PCM16) < 40_000 {
		t.Fatalf("drained %d samples", len(audio.PCM16))
	}
	mono := make([]float64, 0, len(audio.PCM16)/2)
	for index := 0; index+1 < len(audio.PCM16); index += 2 {
		mono = append(mono, float64(audio.PCM16[index])/32768)
	}
	// Skip the filter's start-up so the measurement sees the steady state.
	return mono[1_000:17_384]
}

// TestResamplerSuppressesSourceImages pins the reason media_resample.go exists.
// Holding the nearest stored sample - what the mixer did before - re-emits the
// source spectrum around every multiple of the source rate, and for an 8 kHz
// effect at a 44.1 kHz output those images landed only 17 dB below the tone.
// That is audible as a metallic buzz the handset never produced, because its
// converter reconstructed the same stream through an analogue low pass.
func TestResamplerSuppressesSourceImages(t *testing.T) {
	const tone = 1_000.0
	rendered := renderResampledTone(t, 8_000, tone)
	fundamental := toneMagnitude(rendered, 44_100, tone)
	if fundamental < 0.2 {
		t.Fatalf("fundamental %.4f, want the tone to survive", fundamental)
	}
	// The first two image pairs straddle the source rate and twice it.
	for _, image := range []float64{7_000, 9_000, 15_000, 17_000} {
		magnitude := toneMagnitude(rendered, 44_100, image)
		decibels := 20 * math.Log10(magnitude/fundamental)
		if decibels > -60 {
			t.Errorf(
				"image at %.0f Hz is %.1f dB below the tone, want below -60",
				image, decibels,
			)
		}
	}
}

// TestResamplerPassbandIsFlat checks the reconstruction filter does not dull
// the material it is meant to preserve: everything the 8 kHz source can carry
// up to 3 kHz has to come through untouched.
func TestResamplerPassbandIsFlat(t *testing.T) {
	reference := 0.0
	for _, frequency := range []float64{250, 500, 1_000, 2_000, 3_000} {
		rendered := renderResampledTone(t, 8_000, frequency)
		magnitude := toneMagnitude(rendered, 44_100, frequency)
		if reference == 0 {
			reference = magnitude
			continue
		}
		decibels := 20 * math.Log10(magnitude/reference)
		if math.Abs(decibels) > 0.5 {
			t.Errorf(
				"%.0f Hz came through %.2f dB off, want within 0.5",
				frequency, decibels,
			)
		}
	}
}

// TestMatchingRateReadsStoredSamples pins the fast path. A clip already stored
// at the output rate - every SMAF score is - must reach the mix untouched, so
// the reconstruction filter can never blur a stream that needs no
// reconstruction.
func TestMatchingRateReadsStoredSamples(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputChannels = 1
	media, err := NewMedia(NewRegistry(32), limits)
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(16, 32)
	clip, err := media.CreateClip(3, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	source := []int16{0, 12_000, -12_000, 32_000, -32_000, 500}
	if _, err := media.Append(
		3, clip, pcmWave(limits.OutputSampleRate, 1, source),
	); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(3, clip, 1); err != nil {
		t.Fatal(err)
	}
	span := time.Duration(len(source)+1) * time.Second /
		time.Duration(limits.OutputSampleRate)
	if err := media.Advance(0, span, bus); err != nil {
		t.Fatal(err)
	}
	audio := media.Drain()
	if len(audio.PCM16) < len(source) {
		t.Fatalf("drained %d samples, want at least %d",
			len(audio.PCM16), len(source))
	}
	for index, want := range source {
		if audio.PCM16[index] != want {
			t.Fatalf(
				"sample %d is %d, want the stored %d",
				index, audio.PCM16[index], want,
			)
		}
	}
}

// TestResampleKernelHasUnitGain checks every phase row sums to one. A row that
// did not would put a periodic gain ripple on the signal as the phase walked,
// which is heard as a buzz at the difference of the two rates.
func TestResampleKernelHasUnitGain(t *testing.T) {
	for _, scale := range []float64{1, 0.5, 0.18} {
		kernel := buildResampleKernel(scale)
		for phase := range resamplePhases {
			total := 0.0
			for _, weight := range kernel.weights[phase*kernel.taps : (phase+1)*kernel.taps] {
				total += weight
			}
			if math.Abs(total-1) > 1e-12 {
				t.Fatalf(
					"scale %.2f phase %d sums to %.15f, want 1",
					scale, phase, total,
				)
			}
		}
	}
}

// TestDownsamplingKernelStopsAliases checks the other direction: a clip stored
// above the output rate has to be band limited to the output's Nyquist on the
// way down, or content above it folds back into the audible band.
func TestDownsamplingKernelStopsAliases(t *testing.T) {
	limits := DefaultMediaLimits()
	limits.OutputSampleRate = 8_000
	limits.OutputChannels = 1
	// A 6 kHz tone stored at 44.1 kHz has no home below the 4 kHz output
	// Nyquist; held samples would fold it down to 2 kHz.
	const tone = 6_000.0
	source := make([]int16, 44_100)
	for index := range source {
		source[index] = int16(20_000 * math.Sin(
			2*math.Pi*tone*float64(index)/44_100,
		))
	}
	media, err := NewMedia(NewRegistry(32), limits)
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(16, 32)
	clip, err := media.CreateClip(3, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(3, clip, pcmWave(44_100, 1, source)); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(3, clip, 1); err != nil {
		t.Fatal(err)
	}
	if err := media.Advance(0, time.Second, bus); err != nil {
		t.Fatal(err)
	}
	audio := media.Drain()
	mono := make([]float64, 0, len(audio.PCM16))
	for _, sample := range audio.PCM16 {
		mono = append(mono, float64(sample)/32768)
	}
	if len(mono) < 6_000 {
		t.Fatalf("drained %d samples", len(mono))
	}
	folded := toneMagnitude(mono[1_000:6_000], 8_000, 2_000)
	if folded > 0.01 {
		t.Errorf(
			"6 kHz folded down to %.4f at 2 kHz, want it filtered away",
			folded,
		)
	}
}

// TestFramedAdvanceReadsEverySampleOnce pins the mixer's block continuity.
//
// A clip is mixed one host frame at a time, and the frame count for a block
// comes from an accumulator that carries the fractional frame across blocks.
// The read cursor used to be recomputed from the clip's nanosecond position
// instead, which quantises differently, so the two disagreed by a sample at
// most block boundaries: at a 16 ms frame the stream gained a repeated or
// dropped sample about sixty times a second. That is not a subtle defect - the
// comb it puts either side of every tone in a score sat 19 dB below it - and
// nothing but a sample-exact check catches it, because the audio still plays
// and still lasts the right length.
func TestFramedAdvanceReadsEverySampleOnce(t *testing.T) {
	limits := DefaultMediaLimits()
	rate := limits.OutputSampleRate
	// A ramp that never repeats a value inside the checked span names the
	// stored frame every output sample came from.
	source := make([]int16, rate*2)
	for index := range source {
		source[index] = int16(index%30_000 - 15_000)
	}
	media, err := NewMedia(NewRegistry(32), limits)
	if err != nil {
		t.Fatal(err)
	}
	bus := NewEventBus(16, 32)
	clip, err := media.CreateClip(3, "audio/wav", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := media.Append(3, clip, pcmWave(rate, 1, source)); err != nil {
		t.Fatal(err)
	}
	if err := media.Play(3, clip, 1); err != nil {
		t.Fatal(err)
	}
	var drained []int16
	at := time.Duration(0)
	for range 60 {
		next := at + 16*time.Millisecond
		if err := media.Advance(at, next, bus); err != nil {
			t.Fatal(err)
		}
		drained = append(drained, media.Drain().PCM16...)
		at = next
	}
	if len(drained) < 40_000 {
		t.Fatalf("drained %d samples across a second of frames", len(drained))
	}
	for frame := range min(len(drained)/2, 30_000) {
		got := drained[frame*2]
		if got != source[frame] {
			t.Fatalf(
				"output frame %d is %d, which is stored frame %d:"+
					" the block boundary repeated or dropped a sample",
				frame, got, int(got)+15_000,
			)
		}
	}
}

// TestWaveVoiceSuppressesSourceImages pins the reconstruction of a SMAF wave
// bank. The banks hold Yamaha ADPCM recorded as low as 4 kHz and the score
// plays them well above that, so reading one by drawing a straight line
// between its samples left the source's spectral images barely below the sound
// itself - measured across the local corpus they averaged 14 dB down, which is
// what made percussion in these scores rasp.
func TestWaveVoiceSuppressesSourceImages(t *testing.T) {
	const sourceRate = 8_000
	const renderRate = 44_100
	const tone = 1_000.0
	pcm := make([]int16, sourceRate)
	for index := range pcm {
		pcm[index] = int16(20_000 * math.Sin(
			2*math.Pi*tone*float64(index)/sourceRate,
		))
	}
	voice := smafPCMVoice{
		pcm:    pcm,
		kernel: resampleKernelFor(sourceRate, renderRate),
		step:   float64(sourceRate) / float64(renderRate),
		active: true,
	}
	rendered := make([]float64, 0, 32_768)
	for range 34_000 {
		value := voice.tick()
		if !voice.active {
			break
		}
		rendered = append(rendered, value)
	}
	if len(rendered) < 32_768 {
		t.Fatalf("voice produced %d samples", len(rendered))
	}
	// Skip the window's run-in at the start of the wave.
	measured := rendered[512:33_280]
	fundamental := toneMagnitude(measured, renderRate, tone)
	if fundamental < 0.2 {
		t.Fatalf("fundamental %.4f, want the tone to survive", fundamental)
	}
	for _, image := range []float64{7_000, 9_000, 15_000, 17_000} {
		magnitude := toneMagnitude(measured, renderRate, image)
		decibels := 20 * math.Log10(magnitude/fundamental)
		if decibels > -50 {
			t.Errorf(
				"image at %.0f Hz is %.1f dB below the tone, want below -50",
				image, decibels,
			)
		}
	}
}
