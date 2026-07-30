package runtime

import (
	"math"
	"testing"
)

func TestSMAFEnvelopeUsesMobileFMLevelAndZeroRateSemantics(t *testing.T) {
	patch := smafOpPatch{
		ar: 15,
		dr: 15,
		sr: 0,
		rr: 0,
		sl: 6,
	}
	var envelope smafEnvelope
	envelope.configure(patch, 44_100, 8)
	envelope.keyOn()
	for guard := 0; guard < 10_000 &&
		envelope.phase != smafEnvelopeSustain; guard++ {
		envelope.advance()
	}
	if envelope.phase != smafEnvelopeSustain {
		t.Fatalf("phase = %d, want sustain", envelope.phase)
	}
	wantLevel := math.Pow(10, -18.0/20)
	if math.Abs(envelope.level-wantLevel) > 1e-12 {
		t.Fatalf("sustain level = %.12f, want %.12f", envelope.level, wantLevel)
	}
	held := envelope.level
	for range 2_000 {
		envelope.advance()
	}
	if envelope.level != held {
		t.Fatalf("SR=0 level changed from %f to %f", held, envelope.level)
	}
	envelope.keyOff()
	for range 2_000 {
		envelope.advance()
	}
	if envelope.level != held {
		t.Fatalf("RR=0 level changed from %f to %f", held, envelope.level)
	}
}

func TestSMAFEnvelopeAttackRateZeroIsSilent(t *testing.T) {
	var envelope smafEnvelope
	envelope.configure(smafOpPatch{ar: 0}, 44_100, 0)
	envelope.keyOn()
	if got := envelope.advance(); got != 0 ||
		envelope.phase != smafEnvelopeIdle {
		t.Fatalf("AR=0 envelope = %f phase %d, want silent idle", got, envelope.phase)
	}
}

func TestSMAFOperatorUsesMobileMultiplierAndNeutralDetune(t *testing.T) {
	var operator smafOperator
	operator.configure(
		smafOpPatch{multi: 11, ar: 15},
		44_100,
		440,
	)
	want := 440.0 * 10 / 44_100
	if math.Abs(operator.phaseIncrement-want) > 1e-12 {
		t.Fatalf("phase increment = %.12f, want %.12f", operator.phaseIncrement, want)
	}
}

func TestSMAFWaveformsIncludeMobileTriangleAndSawFamilies(t *testing.T) {
	tests := []struct {
		wave  uint8
		phase float64
		want  float64
	}{
		{wave: 8, phase: 0.25, want: 1},
		{wave: 16, phase: 0.25, want: 1},
		{wave: 16, phase: 0.75, want: -1},
		{wave: 24, phase: 0.25, want: 0.5},
		{wave: 24, phase: 0.75, want: -0.5},
		{wave: 15, phase: 0.25, want: 0},
	}
	for _, test := range tests {
		got := smafWaveform(test.wave, test.phase)
		if math.Abs(got-test.want) > 1e-12 {
			t.Errorf(
				"wave %d at %.2f = %.12f, want %.12f",
				test.wave,
				test.phase,
				got,
				test.want,
			)
		}
	}
}
