package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/mirusu400/aram-core/application/internal/guest"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"image"
	"testing"

	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	machinecore "github.com/mirusu400/aram-core/core"
)

func TestRaptorTimerExpiryPreservesAdjacentGuestMemory(t *testing.T) {
	machine := newSyntheticMachine(t)
	const callback = uint32(0x04000000)
	if err := machine.cpu.Map(
		callback,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(callback, []byte{
		0x2a, 0x22, // movs r2, #42
		0x0a, 0x60, // str r2, [r1]
		0x70, 0x47, // bx lr
	}); err != nil {
		t.Fatal(err)
	}
	timer, err := machine.wipi.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := machine.wipi.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	const sentinel = uint32(0xfeedface)
	if err := machine.wipi.WriteU32(timer, callback|1); err != nil {
		t.Fatal(err)
	}
	if err := machine.wipi.WriteU32(timer+24, sentinel); err != nil {
		t.Fatal(err)
	}
	if result, err := machine.wipi.SetTimer(timer, 16, marker, false); err != nil ||
		result != (guest.WIPIReturn{}) {
		t.Fatalf("set timer = %#v, %v", result, err)
	}
	machine.raptor = &raptorrt.Runtime{
		CPU:     machine.cpu,
		Public:  machine.wipi,
		Started: true,
	}
	if _, stopped, err := machine.pumpWIPICallbacks(
		context.Background(),
		guest.WIPIFrameDuration,
	); err != nil || stopped {
		t.Fatalf("pump callbacks: stopped=%t, err=%v", stopped, err)
	}
	if len(machine.raptor.CallbackTasks) != 1 {
		t.Fatalf(
			"queued Raptor callback tasks = %d, want 1",
			len(machine.raptor.CallbackTasks),
		)
	}
	for frame := 0; len(machine.raptor.CallbackTasks) != 0 && frame < 16; frame++ {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(machine.raptor.CallbackTasks) != 0 {
		t.Fatal("Raptor timer callback did not return")
	}
	if got, err := machine.wipi.ReadU32(marker); err != nil || got != 42 {
		t.Fatalf("callback marker = %d, %v", got, err)
	}
	if got, err := machine.wipi.ReadU32(timer + 24); err != nil || got != sentinel {
		t.Fatalf("timer expiry overwrote adjacent word = 0x%08x, %v", got, err)
	}
}

func TestRaptorTimerImportsUseFourByteLGTABI(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &raptorrt.Runtime{
		CPU:    public.CPU,
		Public: public,
	}
	timer, err := public.Heap.Allocate(32, true)
	if err != nil || timer == 0 {
		t.Fatalf("allocate timer = 0x%08x, %v", timer, err)
	}
	const (
		callback  = uint32(0x12345)
		timeout   = uint32(47)
		parameter = uint32(0x89abcdef)
		sentinel  = uint32(0xfeedface)
	)
	if err := public.WriteU32(timer+4, sentinel); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{timer, callback} {
		if err := runtime.CPU.WriteRegister(uint32(register), value); err != nil {
			t.Fatal(err)
		}
	}
	result, name, handled, err := runtime.DispatchPrivateImport(122)
	if err != nil || !handled || name != "RAPTOR.knlDefTimer" ||
		result != (guest.WIPIReturn{}) {
		t.Fatalf(
			"define timer = %#v, %q, handled=%t, err=%v",
			result,
			name,
			handled,
			err,
		)
	}
	if got, err := public.ReadU32(timer); err != nil || got != callback {
		t.Fatalf("timer callback = 0x%08x, %v", got, err)
	}
	if got, err := public.ReadU32(timer + 4); err != nil || got != sentinel {
		t.Fatalf("define timer overwrote adjacent word = 0x%08x, %v", got, err)
	}
	for register, value := range []uint32{timer, timeout, 0, parameter} {
		if err := runtime.CPU.WriteRegister(uint32(register), value); err != nil {
			t.Fatal(err)
		}
	}
	result, name, handled, err = runtime.DispatchPrivateImport(123)
	if err != nil || !handled || name != "RAPTOR.knlSetTimer" ||
		result != (guest.WIPIReturn{}) {
		t.Fatalf(
			"set timer = %#v, %q, handled=%t, err=%v",
			result,
			name,
			handled,
			err,
		)
	}
	got, ok := public.Timers[timer]
	if !ok || got.Callback != callback || got.Parameter != parameter ||
		got.Deadline != uint64(timeout) {
		t.Fatalf("timer = %+v, present=%t", got, ok)
	}
	if got, err := public.ReadU32(timer + 4); err != nil || got != sentinel {
		t.Fatalf("set timer overwrote adjacent word = 0x%08x, %v", got, err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, timer); err != nil {
		t.Fatal(err)
	}
	result, name, handled, err = runtime.DispatchPrivateImport(124)
	if err != nil || !handled || name != "RAPTOR.knlUnsetTimer" ||
		result != (guest.WIPIReturn{}) {
		t.Fatalf(
			"unset timer = %#v, %q, handled=%t, err=%v",
			result,
			name,
			handled,
			err,
		)
	}
	if _, ok := public.Timers[timer]; ok {
		t.Fatal("timer remains active after unset")
	}
	if got, err := public.ReadU32(timer + 4); err != nil || got != sentinel {
		t.Fatalf("unset timer overwrote adjacent word = 0x%08x, %v", got, err)
	}
}

func TestRaptorCallbacksResumeAcrossFrameBudgets(t *testing.T) {
	machine := newSyntheticMachine(t)
	const callback = uint32(0x04000000)
	if err := machine.cpu.Map(
		callback,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(callback, []byte{
		0x00, 0x20, // movs r0, #0
		0x01, 0x30, // loop: adds r0, #1
		0x0a, 0x28, // cmp r0, #10
		0xfc, 0xd1, // bne loop
		0x70, 0x47, // bx lr
	}); err != nil {
		t.Fatal(err)
	}
	machine.frameRunBudget = 3
	machine.raptor = &raptorrt.Runtime{
		CPU:     machine.cpu,
		Public:  machine.wipi,
		Started: true,
		Clet:    raptorrt.Clet{Paint: callback | 1},
	}

	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	if machine.State() == machinecore.StateFaulted {
		t.Fatal("long Raptor callback faulted at its first frame budget")
	}
	if len(machine.raptor.CallbackTasks) != 1 ||
		len(machine.raptor.CallbackTasks[0].Context) == 0 {
		t.Fatalf(
			"Raptor callback task after first slice = %#v",
			machine.raptor.CallbackTasks,
		)
	}
	if result := machine.LastResult(); result.Reason != cpu.StopBudget ||
		result.Instructions != machine.frameRunBudget {
		t.Fatalf("first callback slice = %+v", result)
	}

	frames := drainRaptorCallbackTasks(t, machine)
	if frames < 2 {
		t.Fatalf("long Raptor callback completed in %d continuation frames", frames)
	}
	if machine.State() != machinecore.StatePaused {
		t.Fatalf("state after Raptor callback return = %s", machine.State())
	}
}

func TestRaptorFramebufferImportsExposeLGTGeometry(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &raptorrt.Runtime{
		CPU:    public.CPU,
		Public: public,
	}
	handle, err := public.EnsureScreenFramebuffer()
	if err != nil {
		t.Fatal(err)
	}
	framebuffer := public.Framebuffers[handle]
	tests := []struct {
		ordinal uint32
		input   uint32
		want    uint32
		name    string
	}{
		{50, handle, framebuffer.Pixels, "RAPTOR.grpGetFrameBufferPixels"},
		{51, handle, uint32(framebuffer.Width), "RAPTOR.grpGetFrameBufferWidth"},
		{52, handle, uint32(framebuffer.Height), "RAPTOR.grpGetFrameBufferHeight"},
		{
			53,
			handle,
			uint32(framebuffer.Width * framebuffer.BitsPerPixel / 8),
			"RAPTOR.grpGetFrameBufferBytesPerLine",
		},
		{
			54,
			framebuffer.Pixels,
			uint32(framebuffer.BitsPerPixel),
			"RAPTOR.grpGetFrameBufferBitsPerPixel",
		},
	}
	for _, test := range tests {
		if err := runtime.CPU.WriteRegister(cpu.RegisterR0, test.input); err != nil {
			t.Fatal(err)
		}
		got, name, handled, err := runtime.DispatchPrivateImport(test.ordinal)
		if err != nil || !handled || name != test.name || got.Low != test.want {
			t.Errorf(
				"ordinal %d = %#v, %q, handled=%t, err=%v; want 0x%08x, %q",
				test.ordinal,
				got,
				name,
				handled,
				err,
				test.want,
				test.name,
			)
		}
	}
}

func TestRaptorCallbackTaskSurvivesSaveState(t *testing.T) {
	machine := newSyntheticMachine(t)
	const callback = uint32(0x04000000)
	if err := machine.cpu.Map(
		callback,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(callback, []byte{
		0x00, 0x20, // movs r0, #0
		0x01, 0x30, // loop: adds r0, #1
		0x0a, 0x28, // cmp r0, #10
		0xfc, 0xd1, // bne loop
		0x70, 0x47, // bx lr
	}); err != nil {
		t.Fatal(err)
	}
	machine.frameRunBudget = 3
	machine.raptor = &raptorrt.Runtime{
		CPU:     machine.cpu,
		Public:  machine.wipi,
		Started: true,
		Clet:    raptorrt.Clet{Paint: callback | 1},
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantContext := append([]byte(nil), machine.raptor.CallbackTasks[0].Context...)
	var saved bytes.Buffer
	if err := machine.SaveState(&saved); err != nil {
		t.Fatal(err)
	}
	wantFrames := drainRaptorCallbackTasks(t, machine)

	if err := machine.LoadState(bytes.NewReader(saved.Bytes())); err != nil {
		t.Fatal(err)
	}
	if len(machine.raptor.CallbackTasks) != 1 ||
		!bytes.Equal(machine.raptor.CallbackTasks[0].Context, wantContext) {
		t.Fatalf(
			"restored Raptor callback task = %#v",
			machine.raptor.CallbackTasks,
		)
	}
	if got := drainRaptorCallbackTasks(t, machine); got != wantFrames {
		t.Fatalf("replayed callback frames = %d, want %d", got, wantFrames)
	}
}

func TestRaptorVolumeImportsExposeLGTVolumeRoots(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &raptorrt.Runtime{
		CPU:    public.CPU,
		Public: public,
	}
	if err := runtime.InstallInterfaces(); err != nil {
		t.Fatal(err)
	}

	count, name, handled, err := runtime.DispatchPrivateImport(300)
	if err != nil || !handled || name != "RAPTOR.fsGetVolumeCount" ||
		count.Low != 2 {
		t.Fatalf(
			"volume count = %#v, %q, handled=%t, err=%v",
			count,
			name,
			handled,
			err,
		)
	}
	list, name, handled, err := runtime.DispatchPrivateImport(301)
	if err != nil || !handled || name != "RAPTOR.fsGetVolumeList" ||
		list.Low != raptorrt.VolumeTable {
		t.Fatalf(
			"volume list = %#v, %q, handled=%t, err=%v",
			list,
			name,
			handled,
			err,
		)
	}
	var pointers [8]byte
	if err := runtime.CPU.ReadMemory(list.Low, pointers[:]); err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{"/L", "/S"} {
		address := binary.LittleEndian.Uint32(pointers[index*4:])
		got, err := public.ReadCString(address)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("volume %d = %q, want %q", index, got, want)
		}
	}

	selected, name, handled, err := runtime.DispatchPrivateImport(302)
	if err != nil || !handled || name != "RAPTOR.fsSelectVolume" ||
		selected != (guest.WIPIReturn{}) {
		t.Fatalf(
			"select volume = %#v, %q, handled=%t, err=%v",
			selected,
			name,
			handled,
			err,
		)
	}
}

func drainRaptorCallbackTasks(t *testing.T, machine *Machine) int {
	t.Helper()
	frames := 0
	for len(machine.raptor.CallbackTasks) != 0 && frames < 64 {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatal(err)
		}
		frames++
	}
	if len(machine.raptor.CallbackTasks) != 0 {
		t.Fatal("Raptor callback did not return within 64 frame slices")
	}
	return frames
}

// A loading-length callback resumes across hundreds of frames. Every resumed
// slice enqueues lifecycle service events, so the frame pump must keep
// draining the event bus while the callback is in progress or 제노니아1's
// data-loading screen faults with "event queue reached 1024".
func TestRaptorResumedCallbackKeepsDrainingServiceEvents(t *testing.T) {
	machine := newSyntheticMachine(t)
	const callback = uint32(0x04000000)
	if err := machine.cpu.Map(
		callback,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := machine.cpu.WriteMemory(callback, []byte{
		0x00, 0x20, // movs r0, #0
		0x01, 0x30, // loop: adds r0, #1
		0xc8, 0x28, // cmp r0, #200
		0xfc, 0xd1, // bne loop
		0x70, 0x47, // bx lr
	}); err != nil {
		t.Fatal(err)
	}
	machine.frameRunBudget = 1
	machine.raptor = &raptorrt.Runtime{
		CPU:     machine.cpu,
		Public:  machine.wipi,
		Started: true,
		Clet:    raptorrt.Clet{Paint: callback | 1},
	}
	if err := machine.StepFrame(context.Background()); err != nil {
		t.Fatal(err)
	}
	frames := 0
	for len(machine.raptor.CallbackTasks) != 0 && frames < 1500 {
		if err := machine.StepFrame(context.Background()); err != nil {
			t.Fatalf("resumed callback frame %d: %v", frames, err)
		}
		if queued := machine.wipi.Services.Events.Len(); queued > 16 {
			t.Fatalf(
				"service event queue grew to %d during resumed callback frame %d",
				queued,
				frames,
			)
		}
		frames++
	}
	if len(machine.raptor.CallbackTasks) != 0 {
		t.Fatalf("Raptor callback still in progress after %d frames", frames)
	}
	if frames < 500 {
		t.Fatalf(
			"callback completed in %d resumed frames; too short to cover the queue limit",
			frames,
		)
	}
	if machine.State() == machinecore.StateFaulted {
		t.Fatalf("resumed callback faulted: %+v", machine.LastResult())
	}
}

func newPublicRuntime(t *testing.T) *wipirt.Runtime {
	t.Helper()
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := wipirt.MapRuntimeMemory(backend); err != nil {
		t.Fatal(err)
	}
	runtime, err := wipirt.NewRuntime(backend, image.NewRGBA(image.Rect(0, 0, 16, 12)))
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Map(guest.DefaultStackBase, guest.DefaultStackSize, cpu.PermissionRead|cpu.PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterSP, guest.DefaultStackBase+guest.DefaultStackSize-0x100); err != nil {
		t.Fatal(err)
	}
	return runtime
}
