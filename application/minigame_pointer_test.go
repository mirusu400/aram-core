package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/application/internal/minigame"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"testing"
)

func nativeTouchPacket(t *testing.T, machine *Machine) [8]byte {
	t.Helper()
	mailbox := minigameMailbox(t, machine)
	if mailbox[0] != 0x50e {
		t.Fatalf("pointer event=%#x, want 0x50e", mailbox[0])
	}
	var packet [8]byte
	check(t, machine.cpu.ReadMemory(mailbox[1], packet[:]))
	return packet
}

func TestNativePointerUsesGuestTimestampAndPacket(t *testing.T) {
	machine := newInputMinigame(t)
	if machine.SupportsPointer() {
		t.Fatal("keypad-only runtime advertised pointer input")
	}
	machine.minigame.EnablePointerInput()
	at := machine.wipi.Services.Clock.Monotonic() + 2*guest.WIPIFrameDuration
	check(t, machine.QueuePointer(machinecore.PointerInputEvent{X: 31, Y: 47, Pressed: true, At: at}))
	check(t, machine.StepFrame(context.Background()))
	if got := minigameMailbox(t, machine); got[0] != minigame.FrameEvent {
		t.Fatal("future pointer was delivered early")
	}
	check(t, machine.StepFrame(context.Background()))
	packet := nativeTouchPacket(t, machine)
	if packet[0] != 1 || binary.LittleEndian.Uint16(packet[2:]) != 31 || binary.LittleEndian.Uint16(packet[4:]) != 47 {
		t.Fatalf("press packet=%v", packet)
	}
	check(t, machine.QueuePointer(machinecore.PointerInputEvent{X: 35, Y: 52, Pressed: false}))
	check(t, machine.StepFrame(context.Background()))
	if packet := nativeTouchPacket(t, machine); packet[0] != 2 || binary.LittleEndian.Uint16(packet[2:]) != 35 {
		t.Fatalf("release packet=%v", packet)
	}
	if err := machine.QueuePointer(machinecore.PointerInputEvent{X: machine.frame.Bounds().Dx(), Y: 0, Pressed: true}); err == nil {
		t.Fatal("out-of-screen pointer was accepted")
	}
}

func TestNativePointerResumesAcrossBudgetAndState(t *testing.T) {
	machine := newInputMinigame(t)
	machine.minigame.EnablePointerInput()
	machine.frameRunBudget = 1
	check(t, machine.QueuePointer(machinecore.PointerInputEvent{X: 12, Y: 34, Pressed: true}))
	check(t, machine.StepFrame(context.Background()))
	var state bytes.Buffer
	check(t, machine.SaveState(&state))
	finish := func() [8]byte {
		for range 64 {
			check(t, machine.StepFrame(context.Background()))
			if machine.LastResult().Reason == cpu.StopBreakpoint {
				return nativeTouchPacket(t, machine)
			}
		}
		t.Fatal("pointer callback failed to complete")
		return [8]byte{}
	}
	first := finish()
	check(t, machine.LoadState(bytes.NewReader(state.Bytes())))
	if replay := finish(); replay != first {
		t.Fatalf("restored pointer=%v, want %v", replay, first)
	}
}

func TestPendingPointerStatePreservesScheduleAndKeyOrdering(t *testing.T) {
	machine := newInputMinigame(t)
	machine.minigame.EnablePointerInput()
	at := machine.wipi.Services.Clock.Monotonic() + 3*guest.WIPIFrameDuration
	check(t, machine.QueuePointer(machinecore.PointerInputEvent{X: 31, Y: 47, Pressed: true, At: at}))
	check(t, machine.QueueInput(machinecore.InputEvent{Control: "left", Pressed: true}))
	check(t, machine.QueueInput(machinecore.InputEvent{Control: "left"}))
	var state bytes.Buffer
	check(t, machine.SaveState(&state))
	for replay := 0; replay < 2; replay++ {
		if replay != 0 {
			check(t, machine.LoadState(bytes.NewReader(state.Bytes())))
		}
		check(t, machine.StepFrame(context.Background()))
		if got := minigameMailbox(t, machine); got[0] != 0x0a {
			t.Fatalf("future pointer blocked current key: %v", got)
		}
		check(t, machine.StepFrame(context.Background()))
		if got := minigameMailbox(t, machine); got[0] == 0x50e {
			t.Fatal("restored pointer delivered early")
		}
		check(t, machine.StepFrame(context.Background()))
		if got := nativeTouchPacket(t, machine); got[0] != 1 || binary.LittleEndian.Uint16(got[2:]) != 31 {
			t.Fatalf("restored packet=%v", got)
		}
	}
}
