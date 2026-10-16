package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/application/internal/minigame"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

func newInputMinigame(t *testing.T) *Machine {
	return newInputMinigameWithABI(t, false)
}

func newInputMinigameWithABI(t *testing.T, legacy bool) *Machine {
	t.Helper()
	program := []byte{
		0x00, 0xb5, // push {lr}
		0x05, 0x4a, // ldr r2, mailbox
		0x10, 0x60, // str r0, [r2]
		0x51, 0x60, // str r1, [r2, #4]
		0x93, 0x68, // ldr r3, [r2, #8]
		0x01, 0x33, // adds r3, #1
		0x93, 0x60, // str r3, [r2, #8]
		0x04, 0x23, // movs r3, #4
		0x01, 0x3b, // delay: subs r3, #1
		0xfd, 0xd1, // bne delay
		0x00, 0xbd, // pop {pc}
		0x00, 0xbf, // alignment
		0x00, 0x00, 0x00, 0x03, // mailbox = synthetic BSS
	}
	data := append(syntheticEADS()[:0x80+0x30], program...)
	binary.LittleEndian.PutUint32(data[0x80+16:], uint32(len(program)))
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: "synthetic-eads-input.dat", ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	check(t, err)
	t.Cleanup(func() { _ = created.Close() })
	machine := created.(*Machine)
	createRuntime := minigame.NewRuntime
	if legacy {
		createRuntime = minigame.NewLegacyRuntime
	}
	runtime, err := createRuntime(machine.cpu, machine.frame, machine.wipi,
		machine.info.BSSAddress, machine.info.BSSSize, machine.info.EntryPoint)
	check(t, err)
	machine.minigame = runtime
	machine.frameRunBudget = 64
	check(t, machine.Start(context.Background()))
	return machine
}

func TestLegacySelectInputAndPendingReleaseSurviveState(t *testing.T) {
	machine := newInputMinigameWithABI(t, true)
	check(t, machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}))
	check(t, machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false}))
	check(t, machine.StepFrame(context.Background()))
	if got := minigameMailbox(t, machine); got[0] != 4 || got[1] != 0x13 {
		t.Fatalf("legacy select=%+v", got)
	}
	var state bytes.Buffer
	check(t, machine.SaveState(&state))
	check(t, machine.StepFrame(context.Background()))
	first := minigameMailbox(t, machine)
	if first[0] != 6 || first[1] != 0x13 {
		t.Fatalf("legacy release=%+v", first)
	}
	check(t, machine.LoadState(bytes.NewReader(state.Bytes())))
	check(t, machine.StepFrame(context.Background()))
	if got := minigameMailbox(t, machine); got != first {
		t.Fatalf("legacy replay=%+v, want %+v", got, first)
	}
}

func minigameMailbox(t *testing.T, machine *Machine) [3]uint32 {
	t.Helper()
	var data [12]byte
	check(t, machine.cpu.ReadMemory(machine.info.BSSAddress, data[:]))
	return [3]uint32{binary.LittleEndian.Uint32(data[0:]),
		binary.LittleEndian.Uint32(data[4:]), binary.LittleEndian.Uint32(data[8:])}
}

func TestMinigameDirectKeysAndHostRelease(t *testing.T) {
	machine := newInputMinigame(t)
	for _, test := range []struct {
		control string
		code    uint32
	}{{"up", 0xc}, {"select", 0x13}, {"0", 0}, {"clear", 0x12},
		{"left", 0xa}, {"right", 0xb}, {"star", 0xe}, {"hash", 0xf}} {
		check(t, machine.QueueInput(machinecore.InputEvent{Control: test.control, Pressed: true}))
		check(t, machine.StepFrame(context.Background()))
		mailbox := minigameMailbox(t, machine)
		if mailbox[0] != test.code || mailbox[1] != 0 {
			t.Fatalf("%s delivered %+v, want direct key %#x", test.control, mailbox, test.code)
		}
		check(t, machine.QueueInput(machinecore.InputEvent{Control: test.control, Pressed: false}))
		check(t, machine.StepFrame(context.Background()))
		if got := minigameMailbox(t, machine); got[0] != minigame.FrameEvent {
			t.Fatalf("host release became an OEM key: %+v", got)
		}
	}
}

func TestMinigameHeldKeysRepeatAndReleaseStopsThem(t *testing.T) {
	machine := newInputMinigame(t)
	check(t, machine.QueueInput(machinecore.InputEvent{Control: "left", Pressed: true}))
	for range 64 {
		check(t, machine.StepFrame(context.Background()))
	}
	stats, _ := machine.EADSFrameStats()
	if stats.InputDispatchCount < 2 {
		t.Fatal("held native key never repeated")
	}
	check(t, machine.QueueInput(machinecore.InputEvent{Control: "left", Pressed: false}))
	for range 3 {
		check(t, machine.StepFrame(context.Background()))
	}
	released, _ := machine.EADSFrameStats()
	for range 64 {
		check(t, machine.StepFrame(context.Background()))
	}
	after, _ := machine.EADSFrameStats()
	if after.InputDispatchCount != released.InputDispatchCount {
		t.Fatal("native key kept repeating after release")
	}
}

func TestMinigameInputWaitsForItsGuestTimestamp(t *testing.T) {
	machine := newInputMinigame(t)
	at := machine.wipi.Services.Clock.Monotonic() + 2*guest.WIPIFrameDuration
	check(t, machine.QueueInput(machinecore.InputEvent{Control: "num5", Pressed: true, At: at}))
	check(t, machine.StepFrame(context.Background()))
	if got := minigameMailbox(t, machine); got[0] != minigame.FrameEvent {
		t.Fatalf("future input was delivered early: %+v", got)
	}
	check(t, machine.StepFrame(context.Background()))
	if got := minigameMailbox(t, machine); got[0] != 5 || got[1] != 0 {
		t.Fatalf("due input was not delivered: %+v", got)
	}
}

func TestMinigameInputResumesAcrossSaveState(t *testing.T) {
	for _, control := range []string{"5", "0"} {
		t.Run(control, func(t *testing.T) { testMinigameInputResumesAcrossSaveState(t, control) })
	}
}

func testMinigameInputResumesAcrossSaveState(t *testing.T, control string) {
	machine := newInputMinigame(t)
	machine.frameRunBudget = 1
	check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
	check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}))
	check(t, machine.QueueInput(machinecore.InputEvent{Control: "left", Pressed: true}))
	check(t, machine.QueueInput(machinecore.InputEvent{Control: "up", Pressed: true,
		At: machine.wipi.Services.Clock.Monotonic() + 1000*guest.WIPIFrameDuration}))
	check(t, machine.StepFrame(context.Background()))
	if machine.LastResult().Reason != cpu.StopBudget {
		t.Fatal("synthetic key handler did not suspend at its instruction budget")
	}
	var saved bytes.Buffer
	check(t, machine.SaveState(&saved))
	finish := func() [3]uint32 {
		for range 64 {
			check(t, machine.StepFrame(context.Background()))
			if machine.LastResult().Reason == cpu.StopBreakpoint {
				return minigameMailbox(t, machine)
			}
		}
		t.Fatal("key handler failed to complete")
		return [3]uint32{}
	}
	first := finish()
	wantCode, _ := minigame.InputKeyCode(control)
	if first[0] != wantCode || first[1] != 0 || first[2] != 6 {
		t.Fatalf("key handler was replaced, duplicated, or lost: %+v", first)
	}
	check(t, machine.LoadState(bytes.NewReader(saved.Bytes())))
	if replayed := finish(); replayed != first {
		t.Fatalf("restored input diverged: %+v / %+v", replayed, first)
	}
	stats, _ := machine.EADSFrameStats()
	if stats.InputDispatchCount != 1 || stats.InputReleaseCount != 0 {
		t.Fatalf("restored input accounting=%+v", stats)
	}
	if next := finish(); next[0] != 0xa || next[1] != 0 || next[2] != 7 {
		t.Fatalf("restored pending key was lost: %+v", next)
	}
	if len(machine.input) != 1 || machine.input[0].Control != "up" {
		t.Fatal("restored future input was consumed")
	}
}
