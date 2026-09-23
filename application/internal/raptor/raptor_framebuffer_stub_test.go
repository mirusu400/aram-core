package raptor

import (
	"context"
	"encoding/binary"
	"image"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

func runRaptorFramebufferPixelsStub(t *testing.T, r *Runtime, handle uint32) (uint32, cpu.Result) {
	t.Helper()
	check(t, r.CPU.WriteRegister(cpu.RegisterR0, handle))
	check(t, r.CPU.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1))
	result := r.CPU.Run(
		context.Background(),
		raptorFramebufferPixelsStub,
		cpu.ModeThumb,
		64,
	)
	value, err := r.CPU.ReadRegister(cpu.RegisterR0)
	check(t, err)
	return value, result
}

func TestRaptorFramebufferPixelsImportRunsValidDescriptorsInGuest(t *testing.T) {
	public := newPublicRuntime(t)
	public.Frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	stub, err := runtime.resolvedImportStub(raptorFramebufferPixelsImport)
	check(t, err)
	if stub != raptorFramebufferPixelsStub {
		t.Fatalf("resolved framebuffer helper = 0x%08x, want 0x%08x", stub, raptorFramebufferPixelsStub)
	}

	handle, err := public.EnsureScreenFramebuffer()
	check(t, err)
	screen := public.Framebuffers[handle]
	got, result := runRaptorFramebufferPixelsStub(t, runtime, handle)
	want := screen.Pixels + uint32(raptorScreenOriginY*screen.Width*screen.BitsPerPixel/8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != guest.ReturnSentinel+2 {
		t.Fatalf("screen helper stopped at %#v", result)
	}
	if got != want {
		t.Fatalf("screen helper pixels = 0x%08x, want 0x%08x", got, want)
	}

	pixels, err := public.Heap.Allocate(16, true)
	check(t, err)
	offscreen, err := public.Heap.Allocate(24, true)
	check(t, err)
	var descriptor [24]byte
	for index, value := range []uint32{pixels, 2, 2, 8, 32, 1} {
		binary.LittleEndian.PutUint32(descriptor[index*4:], value)
	}
	check(t, public.CPU.WriteMemory(offscreen, descriptor[:]))
	got, result = runRaptorFramebufferPixelsStub(t, runtime, offscreen)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != guest.ReturnSentinel+2 {
		t.Fatalf("offscreen helper stopped at %#v", result)
	}
	if got != pixels {
		t.Fatalf("offscreen helper pixels = 0x%08x, want 0x%08x", got, pixels)
	}
}

func TestRaptorFramebufferPixelsImportFallsBackForRawPointers(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	_, err := runtime.resolvedImportStub(raptorFramebufferPixelsImport)
	check(t, err)
	hostStub := raptorImportStubBase + runtime.importSlotByKey[raptorFramebufferPixelsImport]*4

	for _, handle := range []uint32{0, guest.HeapBase} {
		_, result := runRaptorFramebufferPixelsStub(t, runtime, handle)
		if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != hostStub+2 {
			t.Fatalf("fallback for 0x%08x stopped at %#v, want host stub 0x%08x", handle, result, hostStub)
		}
	}
}

func TestRaptorConfiguredPrimaryFramebufferPixelsKeepPhysicalOrigin(t *testing.T) {
	public := newPublicRuntime(t)
	public.Frame = image.NewRGBA(image.Rect(0, 0, 240, 320))
	runtime := &Runtime{
		CPU:                      public.CPU,
		Public:                   public,
		primaryFramebufferHeight: 320,
		importSlotByKey:          make(map[raptorImportKey]uint32),
	}
	_, err := runtime.resolvedImportStub(raptorFramebufferPixelsImport)
	check(t, err)
	handle, err := public.EnsureScreenFramebuffer()
	check(t, err)

	got, result := runRaptorFramebufferPixelsStub(t, runtime, handle)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != guest.ReturnSentinel+2 {
		t.Fatalf("configured screen helper stopped at %#v", result)
	}
	if want := public.Framebuffers[handle].Pixels; got != want {
		t.Fatalf("configured screen helper pixels = 0x%08x, want 0x%08x", got, want)
	}
}

func TestRaptorFramebufferPixelsHelperIsReinstalledWithSavedImportSlots(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	state := &SavedState{
		resolvedImports: map[raptorImportKey]uint64{raptorFramebufferPixelsImport: 7},
		importSlots:     []raptorImportKey{raptorFramebufferPixelsImport},
	}
	check(t, public.CPU.WriteMemory(raptorFramebufferPixelsStub, make([]byte, 0x44)))
	check(t, RestoreState(runtime, public.CPU, state))

	_, result := runRaptorFramebufferPixelsStub(t, runtime, 0)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.PC != raptorImportStubBase+2 {
		t.Fatalf("restored helper fallback stopped at %#v", result)
	}
}
