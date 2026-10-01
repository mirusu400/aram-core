package runtime

import "testing"

func TestSMAFEnvelopeDecayModeReleasesWithoutKeyOff(t *testing.T) {
	patch := smafOpPatch{ar: 15, dr: 15, rr: 15, sl: 8}
	var envelope smafEnvelope
	envelope.configure(patch, 44_100, 0)
	envelope.keyOn()

	seenRelease := false
	for range 44_100 {
		envelope.advance()
		if envelope.phase == smafEnvelopeRelease {
			seenRelease = true
		}
		if envelope.phase == smafEnvelopeIdle && seenRelease {
			break
		}
	}
	if !seenRelease || envelope.phase != smafEnvelopeIdle {
		t.Fatalf("decay mode stopped at phase %d, level %g; want automatic release and silence",
			envelope.phase, envelope.level)
	}
}

func TestSMAFEnvelopeSustainModeWaitsForKeyOff(t *testing.T) {
	patch := smafOpPatch{ar: 15, dr: 15, rr: 15, sl: 8, egType: true}
	var envelope smafEnvelope
	envelope.configure(patch, 44_100, 0)
	envelope.keyOn()
	for range 44_100 {
		envelope.advance()
		if envelope.phase == smafEnvelopeSustain {
			break
		}
	}
	if envelope.phase != smafEnvelopeSustain {
		t.Fatalf("sustain mode stopped at phase %d; want sustain", envelope.phase)
	}
	envelope.keyOff()
	if envelope.phase != smafEnvelopeRelease {
		t.Fatalf("key-off stopped at phase %d; want release", envelope.phase)
	}
}
