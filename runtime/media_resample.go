package runtime

import (
	"math"
	"sync"
)

// A clip whose stored sample rate differs from the mixer's output rate has to
// be resampled on the way into the mix. Holding the nearest stored sample for
// the whole output frame - what this path used to do - is a zero-order hold,
// and a zero-order hold does not just lose the sample values between the
// stored ones: it re-emits the whole source spectrum around every multiple of
// the source rate, attenuated only by the hold's own sinc envelope. An 8 kHz
// effect played out at 44.1 kHz came out with its images 17 dB below the
// fundamental, which is the metallic buzz that made low-rate effects sound far
// worse here than on the handset. The handset's converter reconstructed the
// same 8 kHz stream through an analogue low pass, so those images were never
// audible on the device; suppressing them is restoring the original sound, not
// colouring it.
//
// The reconstruction filter is a windowed sinc evaluated on a phase table, so
// one output frame costs resampleTaps multiply-adds per channel and no
// transcendental at all.
const (
	resampleTaps   = 24
	resampleHalf   = resampleTaps / 2
	resamplePhases = 512
	// resampleMaxTaps bounds the widening a steep downsample asks for.
	resampleMaxTaps = 512
	// resampleCutoff places the passband edge slightly below the Nyquist
	// frequency that has to be preserved, buying the finite tap count a
	// transition band without audibly dulling the source.
	resampleCutoff = 0.95
)

// resampleKernel holds resamplePhases rows of resampleTaps weights. Row p
// reconstructs the sample sitting p/resamplePhases of the way between two
// stored samples.
type resampleKernel struct {
	// taps is resampleTaps when the clip is being upsampled. Downsampling
	// pushes the cutoff down by the rate ratio, and a filter's transition
	// band narrows only with its length, so the kernel widens by the same
	// ratio to keep the stopband where the shorter one could not reach.
	taps    int
	half    int
	weights []float64
}

var (
	resampleKernelsMutex sync.Mutex
	resampleKernels      = map[int]*resampleKernel{}
)

// resampleKernelFor returns the kernel for a source-to-output rate pair. When
// the clip is being upsampled the filter only has to stop the images, so it
// cuts at the source Nyquist; when it is being downsampled it has to stop the
// aliases too, so it cuts at the output Nyquist instead and widens in the
// source domain by the same factor.
func resampleKernelFor(sourceRate, outputRate uint32) *resampleKernel {
	scale := 1.0
	if outputRate < sourceRate {
		scale = float64(outputRate) / float64(sourceRate)
	}
	key := int(math.Round(scale * 4096))
	resampleKernelsMutex.Lock()
	defer resampleKernelsMutex.Unlock()
	if kernel, ok := resampleKernels[key]; ok {
		return kernel
	}
	kernel := buildResampleKernel(float64(key) / 4096)
	resampleKernels[key] = kernel
	return kernel
}

func buildResampleKernel(scale float64) *resampleKernel {
	if scale <= 0 || scale > 1 {
		scale = 1
	}
	taps := int(math.Ceil(float64(resampleTaps)/scale/2)) * 2
	taps = min(max(taps, resampleTaps), resampleMaxTaps)
	half := taps / 2
	cutoff := scale * resampleCutoff
	weights := make([]float64, resamplePhases*taps)
	for phase := range resamplePhases {
		fraction := float64(phase) / resamplePhases
		row := weights[phase*taps : (phase+1)*taps]
		total := 0.0
		for tap := range taps {
			// Tap 0 is the sample half-1 before the one the output frame
			// sits on, so the kernel is centred on the fraction.
			offset := float64(tap-(half-1)) - fraction
			value := sinc(cutoff*offset) * cutoff *
				blackman(offset/float64(half))
			row[tap] = value
			total += value
		}
		// Normalising each row removes the gain ripple the finite window
		// would otherwise put on the signal as the phase walks.
		if total != 0 {
			for tap := range row {
				row[tap] /= total
			}
		}
	}
	return &resampleKernel{taps: taps, half: half, weights: weights}
}

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	scaled := math.Pi * x
	return math.Sin(scaled) / scaled
}

// blackman evaluates the Blackman window over [-1,1] and is zero outside it.
func blackman(x float64) float64 {
	if x <= -1 || x >= 1 {
		return 0
	}
	position := (x + 1) / 2
	return 0.42 - 0.5*math.Cos(2*math.Pi*position) +
		0.08*math.Cos(4*math.Pi*position)
}

// resampleAt reconstructs one stereo output frame from a decoded clip whose
// stored rate differs from the mixer's. frame is the stored frame the output
// sits on and fraction is how far past it the output falls; samples outside the
// stored range are held at the edge, which keeps the filter from ringing
// against silence at the start and end of a clip.
func (decoded *decodedPCM) resampleAt(
	kernel *resampleKernel,
	frame uint64,
	fraction float64,
	frames uint64,
) (int16, int16) {
	phase := int(fraction * resamplePhases)
	if phase < 0 {
		phase = 0
	} else if phase >= resamplePhases {
		phase = resamplePhases - 1
	}
	row := kernel.weights[phase*kernel.taps : (phase+1)*kernel.taps]
	stereo := decoded.channels == 2
	left, right := 0.0, 0.0
	base := int64(frame) - int64(kernel.half-1)
	last := int64(frames) - 1
	for tap, weight := range row {
		index := base + int64(tap)
		if index < 0 {
			index = 0
		} else if index > last {
			index = last
		}
		if stereo {
			left += float64(decoded.samples[index*2]) * weight
			right += float64(decoded.samples[index*2+1]) * weight
		} else {
			left += float64(decoded.samples[index]) * weight
		}
	}
	if !stereo {
		value := clampInt16(int64(math.Round(left)))
		return value, value
	}
	return clampInt16(int64(math.Round(left))),
		clampInt16(int64(math.Round(right)))
}
