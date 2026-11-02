package application

import (
	"context"
	"fmt"
	"image"
	"math"
	"math/rand"
	"strings"
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	machinecore "github.com/mirusu400/aram-core/core"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestExternalRequestSequencesDetachDataAndIgnoreStaleSessionACK(t *testing.T) {
	var sequences externalRequestSequences
	guest := []shared.ExternalRequest{{
		Sequence: 1,
		Kind:     shared.RequestBrowser,
		Target:   "https://example.invalid/",
		Data:     []byte{1, 2, 3},
	}}
	first := sequences.project(guest)
	if len(first) != 1 || first[0].Sequence == 0 {
		t.Fatalf("first projection = %+v", first)
	}
	first[0].Data[0] = 0xff
	if guest[0].Data[0] != 1 {
		t.Fatalf("projected data aliases guest request: %v", guest[0].Data)
	}

	sequences.nextSession()
	second := sequences.project(guest)
	if len(second) != 1 || second[0].Sequence <= first[0].Sequence {
		t.Fatalf("next-session projection = %+v after %+v", second, first)
	}
	if guestThrough, ok := sequences.acknowledge(first[0].Sequence); ok {
		t.Fatalf("stale ACK mapped to current guest sequence %d", guestThrough)
	}
	if guestThrough, ok := sequences.acknowledge(second[0].Sequence); !ok || guestThrough != 1 {
		t.Fatalf("current ACK = (%d, %v), want (1, true)", guestThrough, ok)
	}
}

func TestExternalRequestSequencesAcknowledgeThroughMappedRequest(t *testing.T) {
	var sequences externalRequestSequences
	projected := sequences.project([]shared.ExternalRequest{
		{Sequence: 4, Kind: shared.RequestPhone, Target: "01000000000"},
		{Sequence: 8, Kind: shared.RequestSMS, Target: "01000000000"},
	})
	if len(projected) != 2 {
		t.Fatalf("projection = %+v", projected)
	}
	if guestThrough, ok := sequences.acknowledge(projected[1].Sequence + 1); ok {
		t.Fatalf("synthesized ACK mapped to guest sequence %d", guestThrough)
	}
	guestThrough, ok := sequences.acknowledge(projected[0].Sequence)
	if !ok || guestThrough != 4 {
		t.Fatalf("first ACK = (%d, %v), want (4, true)", guestThrough, ok)
	}
	guestThrough, ok = sequences.acknowledge(projected[1].Sequence)
	if !ok || guestThrough != 8 {
		t.Fatalf("second ACK = (%d, %v), want (8, true)", guestThrough, ok)
	}
}

func TestExternalRequestSequencesPruneDroppedAndEmptyOutbox(t *testing.T) {
	var sequences externalRequestSequences
	first := sequences.project([]shared.ExternalRequest{
		{Sequence: 1, Kind: shared.RequestPhone, Target: "01000000000"},
		{Sequence: 2, Kind: shared.RequestPhone, Target: "01000000001"},
	})
	second := sequences.project([]shared.ExternalRequest{
		{Sequence: 2, Kind: shared.RequestPhone, Target: "01000000001"},
		{Sequence: 3, Kind: shared.RequestPhone, Target: "01000000002"},
	})
	if len(sequences.hostByGuest) != 2 {
		t.Fatalf("mapped request count after drop = %d, want 2", len(sequences.hostByGuest))
	}
	if second[0].Sequence != first[1].Sequence {
		t.Fatalf("live request token changed from %d to %d", first[1].Sequence, second[0].Sequence)
	}
	if guestThrough, ok := sequences.acknowledge(first[0].Sequence); ok {
		t.Fatalf("dropped request ACK mapped to guest sequence %d", guestThrough)
	}

	if projected := sequences.project(nil); len(projected) != 0 {
		t.Fatalf("empty projection = %+v", projected)
	}
	if len(sequences.hostByGuest) != 0 {
		t.Fatalf("empty outbox retained %d mappings", len(sequences.hostByGuest))
	}
	if guestThrough, ok := sequences.acknowledge(second[1].Sequence); ok {
		t.Fatalf("empty-outbox ACK mapped to guest sequence %d", guestThrough)
	}
}

func TestExternalRequestSequencesSortNoncontiguousGuestOutbox(t *testing.T) {
	var sequences externalRequestSequences
	projected := sequences.project([]shared.ExternalRequest{
		{Sequence: 8, Kind: shared.RequestPhone, Target: "eight"},
		{Sequence: 2, Kind: shared.RequestPhone, Target: "two"},
		{Sequence: 5, Kind: shared.RequestPhone, Target: "five"},
	})
	if len(projected) != 3 ||
		projected[0].Target != "two" ||
		projected[1].Target != "five" ||
		projected[2].Target != "eight" ||
		projected[0].Sequence >= projected[1].Sequence ||
		projected[1].Sequence >= projected[2].Sequence {
		t.Fatalf("ordered projection = %+v", projected)
	}
	if guestThrough, ok := sequences.acknowledge(projected[1].Sequence); !ok || guestThrough != 5 {
		t.Fatalf("noncontiguous ACK = (%d, %v), want (5, true)", guestThrough, ok)
	}
	remaining := sequences.project([]shared.ExternalRequest{
		{Sequence: 8, Kind: shared.RequestPhone, Target: "eight"},
	})
	if len(remaining) != 1 || remaining[0].Sequence != projected[2].Sequence {
		t.Fatalf("remaining projection = %+v, want token %d", remaining, projected[2].Sequence)
	}
}

func TestExternalRequestSequencesRemapLateLowerGuestToken(t *testing.T) {
	var sequences externalRequestSequences
	first := sequences.project([]shared.ExternalRequest{{
		Sequence: 8, Kind: shared.RequestPhone, Target: "eight",
	}})
	second := sequences.project([]shared.ExternalRequest{
		{Sequence: 8, Kind: shared.RequestPhone, Target: "eight"},
		{Sequence: 2, Kind: shared.RequestPhone, Target: "two"},
	})
	if len(first) != 1 || len(second) != 2 ||
		second[0].Target != "two" || second[1].Target != "eight" ||
		second[0].Sequence >= second[1].Sequence ||
		second[1].Sequence == first[0].Sequence {
		t.Fatalf("late-lower projection: first=%+v second=%+v", first, second)
	}
	if guestThrough, ok := sequences.acknowledge(first[0].Sequence); ok {
		t.Fatalf("remapped outbox accepted stale token for guest sequence %d", guestThrough)
	}
}

func TestExternalRequestSequencesIgnoreInvalidAndDuplicateGuestTokens(t *testing.T) {
	var sequences externalRequestSequences
	projected := sequences.project([]shared.ExternalRequest{
		{Sequence: 2, Kind: shared.RequestPhone, Target: "first"},
		{Sequence: 0, Kind: shared.RequestPhone, Target: "zero"},
		{Sequence: 2, Kind: shared.RequestPhone, Target: "duplicate"},
	})
	if len(projected) != 1 || projected[0].Target != "first" || projected[0].Sequence == 0 {
		t.Fatalf("filtered projection = %+v", projected)
	}
	if len(sequences.hostByGuest) != 1 {
		t.Fatalf("filtered projection retained %d mappings", len(sequences.hostByGuest))
	}
}

func TestExternalRequestSequencesReplaceReusedGuestTokenWhenRequestChanges(t *testing.T) {
	var sequences externalRequestSequences
	first := sequences.project([]shared.ExternalRequest{{
		Sequence: 7, Kind: shared.RequestBrowser, Target: "https://example.invalid/first",
	}})
	second := sequences.project([]shared.ExternalRequest{{
		Sequence: 7, Kind: shared.RequestBrowser, Target: "https://example.invalid/second",
	}})
	if len(first) != 1 || len(second) != 1 || second[0].Sequence <= first[0].Sequence {
		t.Fatalf("reused guest token projections: first=%+v second=%+v", first, second)
	}
	if guestThrough, ok := sequences.acknowledge(first[0].Sequence); ok {
		t.Fatalf("replaced request accepted stale token for guest sequence %d", guestThrough)
	}
	if guestThrough, ok := sequences.acknowledge(second[0].Sequence); !ok || guestThrough != 7 {
		t.Fatalf("replacement ACK = (%d, %v), want (7, true)", guestThrough, ok)
	}
}

func TestExternalRequestSequencesStopBeforeHostTokenReuse(t *testing.T) {
	sequences := externalRequestSequences{next: math.MaxUint64 - 1}
	guest := []shared.ExternalRequest{
		{Sequence: 1, Kind: shared.RequestPhone, Target: "one"},
		{Sequence: 2, Kind: shared.RequestPhone, Target: "two"},
		{Sequence: 3, Kind: shared.RequestPhone, Target: "three"},
	}
	projected := sequences.project(guest)
	if len(projected) != 2 ||
		projected[0].Sequence != math.MaxUint64-1 ||
		projected[1].Sequence != math.MaxUint64 ||
		!sequences.exhausted {
		t.Fatalf("overflow-boundary projection = %+v, exhausted=%v", projected, sequences.exhausted)
	}
	if repeated := sequences.project(guest); len(repeated) != 2 ||
		repeated[0].Sequence != projected[0].Sequence ||
		repeated[1].Sequence != projected[1].Sequence {
		t.Fatalf("overflow-boundary tokens were unstable: %+v", repeated)
	}
	if guestThrough, ok := sequences.acknowledge(projected[1].Sequence); !ok || guestThrough != 2 {
		t.Fatalf("maximum token ACK = (%d, %v), want (2, true)", guestThrough, ok)
	}
	if after := sequences.project(guest[2:]); len(after) != 0 {
		t.Fatalf("exhausted sequence allocator reused a token: %+v", after)
	}
	sequences.nextSession()
	if after := sequences.project([]shared.ExternalRequest{{
		Sequence: 1, Kind: shared.RequestPhone, Target: "next-session",
	}}); len(after) != 0 {
		t.Fatalf("exhausted sequence allocator wrapped in next session: %+v", after)
	}
}

func TestExternalRequestSequencePropertiesAcrossDropsAndSessions(t *testing.T) {
	random := rand.New(rand.NewSource(0x4152414d))
	var sequences externalRequestSequences
	var live []shared.ExternalRequest
	seenTokens := make(map[uint64]string)
	stableTokens := make(map[string]uint64)
	guestSequence := uint64(1)
	session := 1

	for step := 0; step < 5000; step++ {
		switch random.Intn(7) {
		case 0:
			session++
			guestSequence = 1
			live = nil
			stableTokens = make(map[string]uint64)
			sequences.nextSession()
		default:
			target := fmt.Sprintf("session-%d-request-%d", session, guestSequence)
			live = append(live, shared.ExternalRequest{
				Sequence: guestSequence,
				Kind:     shared.RequestBrowser,
				Target:   target,
			})
			guestSequence += uint64(1 + random.Intn(3))
			if len(live) > 8 {
				live = live[len(live)-8:]
			}
		}

		projected := sequences.project(live)
		if len(projected) != len(live) {
			t.Fatalf("step %d projected %d requests from %d", step, len(projected), len(live))
		}
		for index, request := range projected {
			if request.Sequence == 0 {
				t.Fatalf("step %d exposed zero token", step)
			}
			identity := request.Target
			if previous, exists := seenTokens[request.Sequence]; exists && previous != identity {
				t.Fatalf("step %d reused token %d for %q after %q", step, request.Sequence, identity, previous)
			}
			seenTokens[request.Sequence] = identity
			if previous, exists := stableTokens[identity]; exists && previous != request.Sequence {
				t.Fatalf("step %d changed live token for %q from %d to %d", step, identity, previous, request.Sequence)
			}
			stableTokens[identity] = request.Sequence
			if index != 0 && projected[index-1].Sequence >= request.Sequence {
				t.Fatalf("step %d tokens are not ordered: %+v", step, projected)
			}
		}
		if len(sequences.hostByGuest) > len(live) {
			t.Fatalf("step %d retained %d mappings for %d live requests", step, len(sequences.hostByGuest), len(live))
		}

		if len(projected) != 0 && random.Intn(3) == 0 {
			ackIndex := random.Intn(len(projected))
			guestThrough, ok := sequences.acknowledge(projected[ackIndex].Sequence)
			if !ok {
				t.Fatalf("step %d rejected live token %d", step, projected[ackIndex].Sequence)
			}
			cut := 0
			for cut < len(live) && live[cut].Sequence <= guestThrough {
				delete(stableTokens, live[cut].Target)
				cut++
			}
			live = live[cut:]
		}
	}
}

func TestMachineFailedTimelineChangesPreserveExternalRequestToken(t *testing.T) {
	newMachine := func(t *testing.T) *Machine {
		t.Helper()
		device, err := shared.NewDevice(shared.DeviceConfig{}, shared.DeviceLimits{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := device.Request(
			1, shared.RequestPhone, "01000000000", nil, 0,
		); err != nil {
			t.Fatal(err)
		}
		return &Machine{
			state: machinecore.StateStopped,
			frame: image.NewRGBA(image.Rect(0, 0, 1, 1)),
			ktf:   &ktfrt.Runtime{Services: &shared.Services{Device: device}},
		}
	}
	for _, test := range []struct {
		name   string
		change func(*Machine) error
	}{
		{
			name: "canceled reset",
			change: func(machine *Machine) error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return machine.Reset(ctx)
			},
		},
		{
			name: "invalid load state",
			change: func(machine *Machine) error {
				return machine.LoadState(strings.NewReader("not a save state"))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			machine := newMachine(t)
			before := machine.ExternalRequests()
			if len(before) != 1 {
				t.Fatalf("requests before failed change = %+v", before)
			}
			if err := test.change(machine); err == nil {
				t.Fatal("timeline change unexpectedly succeeded")
			}
			after := machine.ExternalRequests()
			if len(after) != 1 || after[0].Sequence != before[0].Sequence {
				t.Fatalf("failed change replaced request token: before=%+v after=%+v", before, after)
			}
			machine.ClearExternalRequests(before[0].Sequence)
			if requests := machine.ExternalRequests(); len(requests) != 0 {
				t.Fatalf("preserved token did not ACK request: %+v", requests)
			}
		})
	}
}
