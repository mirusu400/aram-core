package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	shared "github.com/mirusu400/aram-core/runtime"
)

// QueueInput must reach the HAL import a native Clet's name widget calls,
// rather than relying on MC_uicHandleEvent or a direct call to the automata.
func TestRaptorQueuedInputComposesThroughGuestHALImport(t *testing.T) {
	m := newSyntheticMachine(t)
	r := &raptorrt.Runtime{CPU: m.cpu, Public: m.wipi}
	check(t, r.RestoreImage())
	check(t, r.InstallInterfaces())
	r.Started = true
	m.raptor = r
	m.frameRunBudget = 10000
	const callback = uint32(0x04000000)
	check(t, m.cpu.Map(callback, 4096, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	memory, err := m.wipi.Heap.Allocate(128, true)
	check(t, err)
	resolve, err := m.wipi.ReadU32(raptorrt.DletBase + 4)
	check(t, err)
	for reg, value := range map[uint32]uint32{cpu.RegisterR0: 507, cpu.RegisterR1: 304, cpu.RegisterLR: guest.ReturnSentinel | 1} {
		check(t, m.cpu.WriteRegister(reg, value))
	}
	handled, err := r.DispatchTrap(context.Background(), resolve&^1)
	check(t, err)
	if !handled {
		t.Fatal("IME import did not resolve")
	}
	stub, err := m.cpu.ReadRegister(cpu.RegisterR0)
	check(t, err)
	check(t, m.cpu.WriteRegister(cpu.RegisterR0, 3))
	_, _, _, err = r.DispatchPrivateImport(302)
	check(t, err)
	// A synthetic Thumb name-widget callback forwards key/type in r0/r1,
	// output pointers in r2/r3 and the preedit pair on the caller's stack.
	words := []uint16{
		0xb5f0, 0xb083, // push r4-r7,lr; reserve three stack words
		0x1c04, 0x1c0d, // preserve event/key
		0x4e0a,                 // ldr r6, buffer literal at +52
		0x2710, 0x6437, 0x6477, // two 16-byte output capacities
		0x1c30, 0x3020, 0x9000, // preedit buffer at sp
		0x1c30, 0x3044, 0x9001, // preedit length pointer at sp+4
		0x1c28, 0x1c21, 0x1c32, 0x1c33, 0x3340,
		0x4f04, 0x47b8, // ldr r7, import literal at +56; blx r7
		0xb003, 0xbdf0, // release stack and return
		0x46c0, 0x46c0, 0x46c0,
	}
	code := make([]byte, 60)
	for i, word := range words {
		binary.LittleEndian.PutUint16(code[i*2:], word)
	}
	binary.LittleEndian.PutUint32(code[52:], memory)
	binary.LittleEndian.PutUint32(code[56:], stub)
	check(t, m.cpu.WriteMemory(callback, code))
	r.Clet.HandleEvent = callback | 1
	press := func(control string, want string) {
		t.Helper()
		check(t, m.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
		check(t, m.StepFrame(context.Background()))
		size, err := m.wipi.ReadU32(memory + 68)
		check(t, err)
		expected, err := m.wipi.Services.Text.Encode(want, shared.EncodingEUCKR)
		check(t, err)
		if size != uint32(len(expected)) {
			t.Fatalf("%s: preedit size=%d, want %d", control, size, len(expected))
		}
		got := make([]byte, size)
		check(t, m.cpu.ReadMemory(memory+32, got))
		if !bytes.Equal(got, expected) {
			t.Fatalf("%s: preedit=%x, want %x", control, got, expected)
		}
		check(t, m.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}))
		check(t, m.StepFrame(context.Background()))
	}
	press("num4", "ㄱ")
	var saved bytes.Buffer
	check(t, m.SaveState(&saved))
	press("num1", "기")
	press("num2", "가")
	check(t, m.LoadState(bytes.NewReader(saved.Bytes())))
	press("num1", "기")
	press("num2", "가")
}
