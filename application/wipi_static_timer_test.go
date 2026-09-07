package application

import (
	"bytes"
	"testing"
)

// A WIPI timer is a caller-owned 28-byte M_TIMER, and real titles keep it in
// their own BSS rather than on the public heap. A save state that recorded
// such a timer must load again; the check that rejected it as "not
// allocated" assumed every timer was a heap block.
func TestSaveStateRoundTripKeepsTimerOutsideHeap(t *testing.T) {
	machine := newSyntheticMachine(t)
	timer := machine.info.BSSAddress
	if machine.info.BSSSize < 32 {
		t.Fatalf("synthetic BSS too small for a timer: %d", machine.info.BSSSize)
	}
	dispatchPublicAPI(t, machine.wipi, "MC_knlDefTimer", timer, 0x02000001)
	if result := dispatchPublicAPI(
		t, machine.wipi, "MC_knlSetTimer", timer, 0, 500, 0, 0,
	); result.Low != 0 {
		t.Fatalf("MC_knlSetTimer = %d", int32(result.Low))
	}
	if machine.wipi.TimerServices[timer] == 0 {
		t.Fatal("timer service was not defined")
	}

	var saved bytes.Buffer
	check(t, machine.SaveState(&saved))
	for address := range machine.wipi.Timers {
		delete(machine.wipi.Timers, address)
	}
	for address := range machine.wipi.TimerServices {
		delete(machine.wipi.TimerServices, address)
	}

	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatalf("a timer in BSS must survive a save state: %v", err)
	}
	if _, ok := machine.wipi.Timers[timer]; !ok {
		t.Fatalf("restored timers = %+v", machine.wipi.Timers)
	}
	if machine.wipi.TimerServices[timer] == 0 {
		t.Fatalf("restored timer services = %+v", machine.wipi.TimerServices)
	}
}

// A timer descriptor that claims to sit on the public heap must still be a
// live heap allocation; relaxing the check for BSS must not relax it there.
func TestLoadStateRejectsTimerInFreeHeap(t *testing.T) {
	machine := newSyntheticMachine(t)
	timer, err := machine.wipi.Heap.Allocate(28, true)
	check(t, err)
	dispatchPublicAPI(t, machine.wipi, "MC_knlDefTimer", timer, 0x02000001)
	if result := dispatchPublicAPI(
		t, machine.wipi, "MC_knlSetTimer", timer, 0, 500, 0, 0,
	); result.Low != 0 {
		t.Fatalf("MC_knlSetTimer = %d", int32(result.Low))
	}
	// Free the block underneath the registered timer, then save: the state
	// now records a timer service at an unallocated heap address.
	if result := dispatchPublicAPI(t, machine.wipi, "MC_knlFree", timer); result.Low != 0 {
		t.Fatalf("MC_knlFree = %d", int32(result.Low))
	}
	var saved bytes.Buffer
	check(t, machine.SaveState(&saved))
	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err == nil {
		t.Fatal("a timer at a freed heap address loaded")
	}
}
