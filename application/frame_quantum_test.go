package application

import (
	"github.com/mirusu400/aram-core/application/internal/guest"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	"testing"
	"time"
)

// TestFrameQuantumMatchesTheAdvanceEachRuntimeApplies pins the reported
// quantum to the guest time StepFrame actually advances. A driver derives its
// call rate from this, so a wrong value runs the guest at the wrong speed.
func TestFrameQuantumMatchesTheAdvanceEachRuntimeApplies(t *testing.T) {
	tests := []struct {
		name    string
		machine *Machine
		want    time.Duration
	}{
		{
			name:    "KTF",
			machine: &Machine{ktf: &ktfrt.Runtime{}},
			want:    ktfrt.FrameDuration,
		},
		{
			name:    "native WIPI",
			machine: &Machine{},
			want:    guest.WIPIFrameDuration,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.machine.FrameQuantum(); got != test.want {
				t.Fatalf("FrameQuantum = %s, want %s", got, test.want)
			}
		})
	}
}

// TestFrameQuantumIsPositiveAndSubSecond keeps a driver from dividing by zero
// or scheduling absurdly, whatever runtime is loaded.
func TestFrameQuantumIsPositiveAndSubSecond(t *testing.T) {
	machines := []interface{ FrameQuantum() time.Duration }{
		&Machine{ktf: &ktfrt.Runtime{}},
		&Machine{},
		&skvmhost.Machine{},
	}
	for _, machine := range machines {
		quantum := machine.FrameQuantum()
		if quantum <= 0 || quantum >= time.Second {
			t.Fatalf("%T reported quantum %s", machine, quantum)
		}
	}
}

// TestQuantumAccumulatorTracksRealTime pins the algorithm a driver has to use.
// A fixed calls-per-second derived as time.Second/quantum truncates: the KTF
// quantum divides into 59 whole calls, which runs the guest 1.7% slow. Keeping
// the remainder in an accumulator instead holds the guest clock to within one
// quantum of real time no matter how the two rates relate.
func TestQuantumAccumulatorTracksRealTime(t *testing.T) {
	tests := []struct {
		name    string
		machine *Machine
	}{
		{name: "KTF", machine: &Machine{ktf: &ktfrt.Runtime{}}},
		{name: "native WIPI", machine: &Machine{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			quantum := test.machine.FrameQuantum()

			// A driver that rounds to whole calls per second is the trap this
			// test exists to document.
			naive := time.Duration(time.Second/quantum) * quantum
			t.Logf("%s: %d whole quanta advance %s", test.name, time.Second/quantum, naive)

			const seconds = 60
			var accumulator, advanced time.Duration
			for range seconds {
				accumulator += time.Second
				for accumulator >= quantum {
					accumulator -= quantum
					advanced += quantum
				}
			}
			drift := advanced - seconds*time.Second
			if drift > quantum || drift < -quantum {
				t.Fatalf(
					"after %d seconds the guest clock drifted %s, more than one %s quantum",
					seconds,
					drift,
					quantum,
				)
			}
		})
	}
}
