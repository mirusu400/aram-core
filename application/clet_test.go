package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"image/color"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/daf"
	"github.com/mirusu400/aram-core/loader/daf/daftest"
)

// syntheticClet is an ARM Clet that exercises what a native SKT WIPI-C title
// needs from its platform. Its data, from the import pointer:
//
//	+0x08  screen framebuffer handle
//	+0x0c  memory ID returned by MC_knlCalloc
//	+0x10  parameter the timer callback received
//	+0x14  type of the last event
//	+0x18  key of the last event
//	+0x20  the timer
func syntheticClet() []byte {
	text := daftest.Words(
		// startClet
		0xe92d4030, // push {r4, r5, lr}
		0xe59f40d4, // ldr r4, =import pointer
		0xe5945000, // ldr r5, [r4]            ; import root
		0xe5950004, // ldr r0, [r5, #4]        ; MC_GRP
		0xe5901008, // ldr r1, [r0, #8]        ; MC_grpGetScreenFrameBuffer
		0xe3a00000, // mov r0, #0
		0xe12fff31, // blx r1
		0xe5840008, // str r0, [r4, #8]
		0xe5901000, // ldr r1, [r0]            ; descriptor
		0xe5912010, // ldr r2, [r1, #16]       ; pixel memory ID
		0xe5922000, // ldr r2, [r2]            ; pixels
		0xe3a03b3e, // mov r3, #0xf800         ; red in RGB565
		0xe1c230b0, // strh r3, [r2]
		0xe5950004, // ldr r0, [r5, #4]
		0xe590c054, // ldr r12, [r0, #0x54]    ; MC_grpFlushLcd
		0xe24dd008, // sub sp, sp, #8
		0xe3a000f0, // mov r0, #240
		0xe58d0000, // str r0, [sp]
		0xe3a00d05, // mov r0, #320
		0xe58d0004, // str r0, [sp, #4]
		0xe3a00000, // mov r0, #0
		0xe5941008, // ldr r1, [r4, #8]
		0xe3a02000, // mov r2, #0
		0xe3a03000, // mov r3, #0
		0xe12fff3c, // blx r12
		0xe28dd008, // add sp, sp, #8
		0xe5950000, // ldr r0, [r5]            ; MC_KNL
		0xe5901054, // ldr r1, [r0, #0x54]     ; MC_knlCalloc
		0xe3a00010, // mov r0, #16
		0xe12fff31, // blx r1
		0xe584000c, // str r0, [r4, #12]
		0xe5901000, // ldr r1, [r0]            ; MC_GETDPTR
		0xe3a02034, // mov r2, #0x34
		0xe5812000, // str r2, [r1]
		0xe5950000, // ldr r0, [r5]
		0xe5902064, // ldr r2, [r0, #0x64]     ; MC_knlDefTimer
		0xe2840020, // add r0, r4, #0x20
		0xe59f1048, // ldr r1, =timer callback
		0xe12fff32, // blx r2
		0xe5950000, // ldr r0, [r5]
		0xe590c0b4, // ldr r12, [r0, #0xb4]    ; timer arm, timeout in r1:r2
		0xe2840020, // add r0, r4, #0x20
		0xe3a01032, // mov r1, #50
		0xe3a02000, // mov r2, #0
		0xe3a03007, // mov r3, #7
		0xe12fff3c, // blx r12
		0xe3a00000, // mov r0, #0
		0xe8bd8030, // pop {r4, r5, pc}
		// timer callback (0xc0)
		0xe59f2020, // ldr r2, =import pointer
		0xe5821010, // str r1, [r2, #0x10]
		0xe12fff1e, // bx lr
		// handleCletEvent (0xcc)
		0xe59f3018, // ldr r3, =import pointer
		0xe5830014, // str r0, [r3, #0x14]
		0xe5831018, // str r1, [r3, #0x18]
		0xe12fff1e, // bx lr
		// pauseClet, resumeClet, destroyClet, paintClet (0xdc)
		0xe12fff1e, // bx lr
		daf.LinkedDataBase,
		0x000000c0,
		daf.LinkedDataBase,
		daf.LinkedDataBase,
	)
	return daftest.Build(daftest.Spec{
		Text:    text,
		Data:    daftest.Words(daf.ImportPointerMarker),
		BSSSize: 0x5c,
		Entries: daf.Entries{Start: 0, Pause: 0xdc, Resume: 0xdc, Destroy: 0xdc, Paint: 0xdc, HandleEvent: 0xcc},
		TextRelocations: []daftest.Relocation{
			{Offset: 0xe0, Target: daf.TargetData},
			{Offset: 0xe4, Target: daf.TargetText},
			{Offset: 0xe8, Target: daf.TargetData},
			{Offset: 0xec, Target: daf.TargetData},
		},
		Resources: []daf.Resource{{Name: "note.txt", Data: []byte("resource")}},
	})
}

func newSyntheticClet(t *testing.T) *Machine {
	t.Helper()
	archive := testZIP(t, map[string][]byte{
		"0000000001.daf": syntheticClet(),
		"0000000001.mod": []byte("installer record"),
		"savedata.dat":   []byte("bundled save"),
	})
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "clet.zip",
		ReaderAt: bytes.NewReader(archive),
		Size:     int64(len(archive)),
	})
	check(t, err)
	machine := created.(*Machine)
	t.Cleanup(func() { _ = machine.Close() })
	return machine
}

func cletWord(t *testing.T, machine *Machine, offset uint32) uint32 {
	t.Helper()
	var word [4]byte
	check(t, machine.cpu.ReadMemory(machine.clet.dataBase+offset, word[:]))
	return binary.LittleEndian.Uint32(word[:])
}

func stepClet(t *testing.T, machine *Machine, frames int) {
	t.Helper()
	for range frames {
		check(t, machine.StepFrame(context.Background()))
	}
}

// An SKT native WIPI-C archive loads as a Clet: startClet reaches the WIPI-C
// tables through the import pointer, memory IDs are cells holding the data
// pointer, the screen it draws to is presented, and its timer fires.
func TestCletStartsDrawsAndRunsItsTimer(t *testing.T) {
	machine := newSyntheticClet(t)
	if info := machine.ImageInfo(); info.SourceKind != loader.KindDAF || info.ProfileID != CletProfileID {
		t.Fatalf("image info = %+v", info)
	}
	check(t, machine.Start(context.Background()))
	if state := machine.State(); state != machinecore.StatePaused {
		t.Fatalf("state after startClet = %s", state)
	}

	id := cletWord(t, machine, 0x0c)
	if id == 0 {
		t.Fatal("MC_knlCalloc returned no memory ID")
	}
	var cell [8]byte
	check(t, machine.cpu.ReadMemory(id, cell[:4]))
	pointer := binary.LittleEndian.Uint32(cell[:4])
	check(t, machine.cpu.ReadMemory(pointer, cell[4:]))
	if pointer == 0 || pointer == id || binary.LittleEndian.Uint32(cell[4:]) != 0x34 {
		t.Fatalf("memory ID %#x holds pointer %#x with value %#x", id, pointer, binary.LittleEndian.Uint32(cell[4:]))
	}

	red := color.RGBAModel.Convert(machine.Framebuffer().At(0, 0)).(color.RGBA)
	if red.R < 0xf0 || red.G != 0 || red.B != 0 {
		t.Fatalf("presented pixel = %+v, want the red the Clet wrote", red)
	}

	if got := cletWord(t, machine, 0x10); got != 0 {
		t.Fatalf("timer fired before its timeout: parameter %d", got)
	}
	stepClet(t, machine, int(60*time.Millisecond/guest.WIPIFrameDuration)+1)
	if got := cletWord(t, machine, 0x10); got != 7 {
		t.Fatalf("timer callback parameter = %d, want 7", got)
	}
	if names := machine.WIPIUnimplementedAPIs(); len(names) != 0 {
		t.Fatalf("unimplemented calls: %v", names)
	}
}

// Keys reach handleCletEvent as the standard press and release events, a
// reset starts the Clet over, and a saved state resumes it without running
// startClet again.
func TestCletInputResetAndSavedState(t *testing.T) {
	machine := newSyntheticClet(t)
	check(t, machine.Start(context.Background()))

	check(t, machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: true}))
	stepClet(t, machine, 2)
	key := cletWord(t, machine, 0x18)
	if got := cletWord(t, machine, 0x14); got != cletKeyPressEvent || key == 0 {
		t.Fatalf("press event = type %d key %#x", got, key)
	}
	if got := cletWord(t, machine, 0x10); got != 0 {
		t.Fatalf("timer fired before its timeout: parameter %d", got)
	}

	var saved bytes.Buffer
	check(t, machine.SaveState(&saved))
	check(t, machine.QueueInput(machinecore.InputEvent{Control: "ok", Pressed: false}))
	stepClet(t, machine, 8)
	if got := cletWord(t, machine, 0x14); got != cletKeyReleaseEvent || cletWord(t, machine, 0x18) != key {
		t.Fatalf("release event = type %d key %#x", got, cletWord(t, machine, 0x18))
	}
	if got := cletWord(t, machine, 0x10); got != 7 {
		t.Fatalf("timer parameter before restore = %d", got)
	}
	check(t, machine.LoadState(bytes.NewReader(saved.Bytes())))
	if got := cletWord(t, machine, 0x10); got != 0 {
		t.Fatalf("restored state already ran the timer: %d", got)
	}
	if got := cletWord(t, machine, 0x14); got != cletKeyPressEvent {
		t.Fatalf("restored state lost the last event: %d", got)
	}
	stepClet(t, machine, 8)
	if got := cletWord(t, machine, 0x10); got != 7 {
		t.Fatalf("timer did not fire after restore: %d", got)
	}

	check(t, machine.Reset(context.Background()))
	if got := cletWord(t, machine, 0x14); got != 0 {
		t.Fatalf("reset kept Clet data: event type %d", got)
	}
	check(t, machine.Start(context.Background()))
	if cletWord(t, machine, 0x0c) == 0 {
		t.Fatal("startClet did not run again after reset")
	}
	stepClet(t, machine, 8)
	if got := cletWord(t, machine, 0x10); got != 7 {
		t.Fatalf("timer did not fire after reset: %d", got)
	}
}
