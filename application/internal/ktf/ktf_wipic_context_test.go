package ktf

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// newKTFRuntimeWithClient boots a scratch runtime whose client image is the
// caller's code, so a test can point a guest callback at it.
func newKTFRuntimeWithClient(t *testing.T, client []byte) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     client,
	})
	check(t, err)
	check(t, runtime.SetTraceMode(KTFTraceFull))
	t.Cleanup(func() { _ = runtime.CPU.Close() })
	check(t, runtime.MapImageAndHost())
	return runtime
}

// MC_grpSetContext and MC_grpGetContext have to agree with the member offsets
// a title writes by hand. 컴투스포춘골프3D services the transparent pixel, the
// alpha, the drawing offset and the pixel-op procedure itself and forwards the
// rest, so a mismatch shows up as an unrelated member changing value.
func TestKTFWIPICContextMembersRoundTripThroughTheAPI(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	address := heapAlloc(t, runtime, ktfWIPICContextSize, true)
	setKTFWIPICCallArguments(t, runtime, []uint32{address})
	if _, err := ktfWIPICGraphicsInitContext(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	for index, offset := range ktfWIPICContextScalarOffsets {
		value := 0x1000 + index
		setKTFWIPICCallArguments(t, runtime, []uint32{address, index, value})
		if _, err := ktfWIPICGraphicsSetContext(
			context.Background(),
			runtime,
		); err != nil {
			t.Fatal(err)
		}
		stored := readU32(t, runtime, address+offset)
		if stored != value {
			t.Fatalf(
				"index %d wrote 0x%08x to +0x%02x, want 0x%08x",
				index,
				stored,
				offset,
				value,
			)
		}
		output := heapAlloc(t, runtime, 4, true)
		setKTFWIPICCallArguments(t, runtime, []uint32{address, index, output})
		if _, err := ktfWIPICGraphicsGetContext(
			context.Background(),
			runtime,
		); err != nil {
			t.Fatal(err)
		}
		readBack := readU32(t, runtime, output)
		if readBack != value {
			t.Fatalf("index %d read back 0x%08x, want 0x%08x", index, readBack, value)
		}
	}

	// The clip is a four-word array behind the enable word, and installing it
	// turns clipping on.
	rectangle := heapAlloc(t, runtime, 16, true)
	check(t, runtime.writeWords(rectangle, []uint32{3, 4, 30, 40}))
	setKTFWIPICCallArguments(t, runtime, []uint32{address, 0, rectangle})
	if _, err := ktfWIPICGraphicsSetContext(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	state, err := runtime.wipicGraphicsContext(address)
	check(t, err)
	if !state.clipEnabled || state.left != 3 || state.top != 4 ||
		state.right != 30 || state.bottom != 40 {
		t.Fatalf("clip read back as %+v", state)
	}
}

// A title that fills its own MC_GrpContext can leave the enable word set over
// a rectangle with no area. Honouring it discarded every host draw the title
// made, which is why 컴투스포춘골프3D showed a frozen title screen where its
// menu belonged (issue #86).
func TestKTFWIPICContextIgnoresAnEmptyClip(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	address := heapAlloc(t, runtime, ktfWIPICContextSize, true)
	// clip_enabled set, clip = (0,0,0,240): the shape the title leaves behind.
	check(t, runtime.writeWords(address, []uint32{1, 0, 0, 0, 240}))
	state, err := runtime.wipicGraphicsContext(address)
	check(t, err)
	if state.clipEnabled {
		t.Fatal("an empty clip rectangle must not clip")
	}
}

// MC_grpDrawImage has to run the context's MC_GrpPixelOpProc. The rotating
// main menu of 컴투스포춘골프3D fades its entries that way; ignoring the
// procedure painted every entry opaque on top of the others.
func TestKTFWIPICDrawImageRunsTheContextPixelOp(t *testing.T) {
	// Thumb: r0 = srcpxl & param1; return. A stand-in for the blend a title
	// installs, chosen so the result depends on both arguments the interface
	// has to pass through.
	client := []byte{0x10, 0x40, 0x70, 0x47}
	runtime := newKTFRuntimeWithClient(t, client)

	source, err := runtime.createWIPICFramebuffer(2, 1, false)
	check(t, err)
	destination, err := runtime.createWIPICFramebuffer(2, 1, false)
	check(t, err)
	sourcePixels := []byte{0xff, 0xff, 0xff, 0xff}
	check(t, runtime.CPU.WriteMemory(
		runtime.wipicFramebuffers[source].pixels,
		sourcePixels,
	))
	imageObject := heapAlloc(t, runtime, 4, true)
	runtime.wipicImages[imageObject] = &ktfWIPICImage{
		object:      imageObject,
		framebuffer: source,
		alpha:       ktfWIPICImageAlpha{key: -1},
	}

	address := heapAlloc(t, runtime, ktfWIPICContextSize, true)
	words := make([]uint32, ktfWIPICContextSize/4)
	words[ktfWIPICContextPixelOp/4] = ImageBase | 1
	words[ktfWIPICContextPixelParam/4] = 0x0f0f
	check(t, runtime.writeWords(address, words))

	setKTFWIPICCallArguments(t, runtime, []uint32{
		destination, 0, 0, 2, 1, imageObject, 0, 0, address,
	})
	if _, err := ktfWIPICGraphicsDrawImage(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	painted := make([]byte, 4)
	check(t, runtime.CPU.ReadMemory(
		runtime.wipicFramebuffers[destination].pixels,
		painted,
	))
	for pixel := range 2 {
		got := binary.LittleEndian.Uint16(painted[pixel*2:])
		if got != 0x0f0f {
			t.Fatalf("pixel %d = 0x%04x, want the procedure's 0x0f0f", pixel, got)
		}
	}
	if runtime.brokenWIPICPixelOps[ImageBase|1] {
		t.Fatal("the procedure was retired even though it returned normally")
	}
	if len(runtime.wipicPixelOpResults) == 0 {
		t.Fatal("the pixel-op result cache stayed empty")
	}
}

// A procedure address outside the loaded client image is leftover data, not a
// callback: running it would fault the machine on a title that never installed
// one.
func TestKTFWIPICPixelOpRejectsAddressesOutsideTheImage(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	for _, procedure := range []uint32{0, ImageBase - 4, 0x7fff0000} {
		state := ktfWIPICGraphicsContext{pixelOp: procedure}
		if runtime.usableWIPICPixelOp(state, 16) {
			t.Fatalf("procedure 0x%08x should not run", procedure)
		}
	}
	// A rectangle larger than the guest-call budget also declines.
	state := ktfWIPICGraphicsContext{pixelOp: ImageBase}
	if runtime.usableWIPICPixelOp(state, ktfWIPICPixelOpMaxPixels+1) {
		t.Fatal("a full-screen rectangle should not run a per-pixel callback")
	}
}

// A procedure that faults is retired once and the rest of the session falls
// back to a plain copy instead of faulting on every pixel.
func TestKTFWIPICPixelOpRetiresAfterAFault(t *testing.T) {
	// Thumb: load from r0 with r0 = 0xffff, an address no region maps.
	client := []byte{0x00, 0x68, 0x70, 0x47}
	runtime := newKTFRuntimeWithClient(t, client)
	source, err := runtime.createWIPICFramebuffer(2, 1, false)
	check(t, err)
	destination, err := runtime.createWIPICFramebuffer(2, 1, false)
	check(t, err)
	check(t, runtime.CPU.WriteMemory(
		runtime.wipicFramebuffers[source].pixels,
		[]byte{0x34, 0x12, 0x34, 0x12},
	))
	imageObject := heapAlloc(t, runtime, 4, true)
	runtime.wipicImages[imageObject] = &ktfWIPICImage{
		object:      imageObject,
		framebuffer: source,
		alpha:       ktfWIPICImageAlpha{key: -1},
	}
	address := heapAlloc(t, runtime, ktfWIPICContextSize, true)
	words := make([]uint32, ktfWIPICContextSize/4)
	words[ktfWIPICContextPixelOp/4] = ImageBase | 1
	check(t, runtime.writeWords(address, words))
	setKTFWIPICCallArguments(t, runtime, []uint32{
		destination, 0, 0, 2, 1, imageObject, 0, 0, address,
	})
	if _, err := ktfWIPICGraphicsDrawImage(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatalf("a faulting pixel op must not fail the draw: %v", err)
	}
	if !runtime.brokenWIPICPixelOps[ImageBase|1] {
		t.Fatal("a faulting procedure should have been retired")
	}
	painted := make([]byte, 4)
	check(t, runtime.CPU.ReadMemory(
		runtime.wipicFramebuffers[destination].pixels,
		painted,
	))
	if binary.LittleEndian.Uint16(painted) != 0x1234 {
		t.Fatalf("fallback copy wrote 0x%04x, want the source pixel", painted)
	}
	// A later draw with the same procedure must not try again.
	before, err := runtime.CPU.ReadRegister(cpu.RegisterPC)
	check(t, err)
	setKTFWIPICCallArguments(t, runtime, []uint32{
		destination, 0, 0, 2, 1, imageObject, 0, 0, address,
	})
	if _, err := ktfWIPICGraphicsDrawImage(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	after, err := runtime.CPU.ReadRegister(cpu.RegisterPC)
	check(t, err)
	if before != after {
		t.Fatal("the retired procedure was entered again")
	}
}
