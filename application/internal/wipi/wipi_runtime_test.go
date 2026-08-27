package wipi

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	wipicatalog "github.com/mirusu400/aram-core/wipi"
)

func newPublicRuntime(t *testing.T) *Runtime {
	t.Helper()
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	if err := MapRuntimeMemory(backend); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(backend, image.NewRGBA(image.Rect(0, 0, 16, 12)))
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

func dispatchPublicAPI(t *testing.T, runtime *Runtime, name string, args ...uint32) guest.WIPIReturn {
	t.Helper()
	for index := 0; index < 4; index++ {
		value := uint32(0)
		if index < len(args) {
			value = args[index]
		}
		if err := runtime.CPU.WriteRegister(uint32(index), value); err != nil {
			t.Fatal(err)
		}
	}
	sp := guest.DefaultStackBase + guest.DefaultStackSize - 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, sp); err != nil {
		t.Fatal(err)
	}
	for index := 4; index < len(args); index++ {
		var encoded [4]byte
		binary.LittleEndian.PutUint32(encoded[:], args[index])
		if err := runtime.CPU.WriteMemory(sp+uint32(index-4)*4, encoded[:]); err != nil {
			t.Fatal(err)
		}
	}
	const link = uint32(0x02000001)
	if err := runtime.CPU.WriteRegister(cpu.RegisterLR, link); err != nil {
		t.Fatal(err)
	}
	stub, ok := runtime.Layout.StubByName[name]
	if !ok {
		t.Fatalf("%s has no stub", name)
	}
	handled, err := runtime.dispatchTrap(context.Background(), stub&^1)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatalf("%s trap was not handled", name)
	}
	low, err := runtime.CPU.ReadRegister(cpu.RegisterR0)
	if err != nil {
		t.Fatal(err)
	}
	high, err := runtime.CPU.ReadRegister(cpu.RegisterR1)
	if err != nil {
		t.Fatal(err)
	}
	if pc, _ := runtime.CPU.ReadRegister(cpu.RegisterPC); pc != link&^1 {
		t.Fatalf("%s returned to PC 0x%08x", name, pc)
	}
	return guest.WIPIReturn{Low: low, High: high}
}

func TestWIPIRuntimeInstallsAllPublicImports(t *testing.T) {
	runtime := newPublicRuntime(t)
	var encoded [4]byte
	if err := runtime.CPU.ReadMemory(wipicatalog.ImportPointerAddress, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded[:]); got != wipicatalog.ProcessHolderAddress {
		t.Fatalf("import pointer = 0x%08x", got)
	}
	if len(runtime.Layout.APIByStub) != 239 {
		t.Fatalf("installed API stubs = %d", len(runtime.Layout.APIByStub))
	}
	coverage := runtime.Coverage()
	if coverage.Cataloged != 239 || coverage.DispatchWired != 239 ||
		coverage.SemanticallyModeled != 239 {
		t.Fatalf("coverage = %+v", coverage)
	}
}

func TestWIPIDispatchArgumentCountsMatchCatalogPrototypes(t *testing.T) {
	for _, api := range wipicatalog.APIs() {
		expected, variadic, ok := prototypeABIWordCount(api.Prototype)
		if !ok {
			t.Fatalf("%s has an unparsable prototype %q", api.Name, api.Prototype)
		}
		if variadic {
			continue
		}
		var (
			actual  int
			modeled bool
		)
		switch api.Family {
		case "MC_FS":
			actual = filesystemArgumentCount(api.Name)
			modeled = true
		case "MC_DB":
			actual = databaseArgumentCount(api.Name)
			modeled = true
		case "MC_GRP":
			actual, modeled = graphicsArgumentCount(api.Name)
		case "MC_UIC":
			actual, modeled = uicArgumentCount(api.Name)
		case "MC_MDA":
			actual = mediaArgumentCount(api.Name)
			modeled = true
		case "MC_NET":
			actual, modeled = networkArgumentCount(api.Name)
		case "MC_HTTP":
			actual, modeled = httpArgumentCount(api.Name)
		default:
			continue
		}
		if !modeled {
			t.Errorf("%s has no argument-count dispatch entry", api.Name)
		} else if actual != expected {
			t.Errorf("%s ABI word count = %d, prototype requires %d", api.Name, actual, expected)
		}
	}
}

func prototypeABIWordCount(prototype string) (count int, variadic bool, ok bool) {
	open := strings.IndexByte(prototype, '(')
	close := strings.LastIndexByte(prototype, ')')
	if open < 0 || close <= open {
		return 0, false, false
	}
	parameters := strings.TrimSpace(prototype[open+1 : close])
	if parameters == "" || parameters == "void" {
		return 0, false, true
	}
	var fields []string
	depth := 0
	start := 0
	for index, character := range parameters {
		switch character {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return 0, false, false
			}
			depth--
		case ',':
			if depth == 0 {
				fields = append(fields, strings.TrimSpace(parameters[start:index]))
				start = index + 1
			}
		}
	}
	if depth != 0 {
		return 0, false, false
	}
	fields = append(fields, strings.TrimSpace(parameters[start:]))
	for _, field := range fields {
		if field == "..." {
			variadic = true
			continue
		}
		isPointer := strings.Contains(field, "*")
		isWide := !isPointer &&
			(strings.Contains(field, "M_Int64") ||
				strings.Contains(field, "M_Uint64") ||
				strings.Contains(field, "time_t") ||
				strings.Contains(field, "double"))
		if isWide {
			if count&1 != 0 {
				count++
			}
			count += 2
		} else {
			count++
		}
	}
	return count, variadic, true
}

func TestWIPIRuntimeCStdlibAndKernelPrimitives(t *testing.T) {
	runtime := newPublicRuntime(t)
	const source = guest.HeapBase + 0x100
	if err := runtime.CPU.WriteMemory(source, []byte("wipi\x00")); err != nil {
		t.Fatal(err)
	}
	if result := dispatchPublicAPI(t, runtime, "strlen", source); result.Low != 4 {
		t.Fatalf("strlen = %d", result.Low)
	}
	allocation := dispatchPublicAPI(t, runtime, "MC_knlCalloc", 64).Low
	if allocation == 0 {
		t.Fatal("MC_knlCalloc returned null")
	}
	memory := make([]byte, 64)
	if err := runtime.CPU.ReadMemory(allocation, memory); err != nil {
		t.Fatal(err)
	}
	for index, value := range memory {
		if value != 0 {
			t.Fatalf("calloc byte %d = 0x%02x", index, value)
		}
	}
	dispatchPublicAPI(t, runtime, "MC_knlFree", allocation)
	if runtime.Stats.ImplementedCalls != 3 || runtime.Stats.UnimplementedCalls != 0 {
		t.Fatalf("stats = %+v", runtime.Stats)
	}
}

func TestWIPIRuntimeReadsCStringEndingAtMappingBoundary(t *testing.T) {
	runtime := newPublicRuntime(t)
	const address = uint32(0x04000000)
	if err := runtime.CPU.Map(
		address,
		4,
		cpu.PermissionRead|cpu.PermissionWrite,
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(address, []byte{'A', 'R', 'M', 0}); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.ReadCString(address)
	if err != nil || string(value) != "ARM" {
		t.Fatalf("boundary C string = %q, %v", value, err)
	}
}

func TestWIPIRuntimeKernelPrintfFormatsGuestVarargs(t *testing.T) {
	runtime := newPublicRuntime(t)
	allocateString := func(value string) uint32 {
		t.Helper()
		address, err := runtime.Heap.Allocate(uint32(len(value)+1), true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.writeCString(address, []byte(value), -1); err != nil {
			t.Fatal(err)
		}
		return address
	}

	name := allocateString("wipi-app")
	printkFormat := allocateString("name=%s score=%+05d hex=%#x big=%lld pct=%%")
	big := uint64(0x0000000200000001)
	printk := dispatchPublicAPI(
		t,
		runtime,
		"MC_knlPrintk",
		printkFormat,
		name,
		^uint32(41),
		42,
		uint32(big),
		uint32(big>>32),
	)
	expectedLog := "name=wipi-app score=-0042 hex=0x2a big=8589934593 pct=%"
	if int(printk.Low) != len(expectedLog) ||
		len(runtime.Logs) != 1 ||
		runtime.Logs[0] != expectedLog {
		t.Fatalf("MC_knlPrintk = %d/%q", printk.Low, runtime.Logs)
	}

	output, err := runtime.Heap.Allocate(128, true)
	if err != nil {
		t.Fatal(err)
	}
	sprintkFormat := allocateString("%*.*s %.2f")
	floatBits := math.Float64bits(3.5)
	sprintk := dispatchPublicAPI(
		t,
		runtime,
		"MC_knlSprintk",
		output,
		sprintkFormat,
		8,
		4,
		name,
		0, // AAPCS padding before the aligned double.
		uint32(floatBits),
		uint32(floatBits>>32),
	)
	expectedOutput := "    wipi 3.50"
	formatted, err := runtime.ReadCString(output)
	if err != nil {
		t.Fatal(err)
	}
	if int(sprintk.Low) != len(expectedOutput) || string(formatted) != expectedOutput {
		t.Fatalf("MC_knlSprintk = %d/%q", printk.Low, formatted)
	}
}

func TestWIPIRuntimeKernelResources(t *testing.T) {
	runtime := newPublicRuntime(t)
	payload := []byte{0x89, 'P', 'N', 'G'}
	resourceID := runtime.RegisterResource("images/title.png", payload)
	if resourceID < 1 {
		t.Fatalf("registered resource ID = %d", resourceID)
	}
	name, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(name, []byte("images/title.png"), -1); err != nil {
		t.Fatal(err)
	}
	size, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_knlGetResourceID",
		name,
		size,
	).Low); got != resourceID {
		t.Fatalf("MC_knlGetResourceID = %d", got)
	}
	resourceSize, err := runtime.ReadU32(size)
	if err != nil || resourceSize != uint32(len(payload)) {
		t.Fatalf("resource size = %d, %v", resourceSize, err)
	}
	output, err := runtime.Heap.Allocate(resourceSize, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_knlGetResource",
		uint32(resourceID),
		output,
		resourceSize-1,
	).Low); got != guest.WIPIShortBuffer {
		t.Fatalf("short MC_knlGetResource = %d", got)
	}
	if got := dispatchPublicAPI(
		t,
		runtime,
		"MC_knlGetResource",
		uint32(resourceID),
		output,
		resourceSize,
	).Low; got != 0 {
		t.Fatalf("MC_knlGetResource = %d", int32(got))
	}
	restored := make([]byte, resourceSize)
	if err := runtime.CPU.ReadMemory(output, restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, payload) {
		t.Fatalf("resource payload = %x", restored)
	}
}

func TestWIPIRuntimeKernelProgramLifecycle(t *testing.T) {
	runtime := newPublicRuntime(t)
	execName, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(execName, []byte("wipi-app"), -1); err != nil {
		t.Fatal(err)
	}
	output, err := runtime.Heap.Allocate(64, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_knlGetExecNames",
		0,
		0,
		0,
		output,
		64,
	).Low); got != 1 {
		t.Fatalf("MC_knlGetExecNames = %d", got)
	}
	list, err := runtime.ReadCString(output)
	if err != nil || string(list) != "wipi-app" {
		t.Fatalf("executable list = %q, %v", list, err)
	}
	if got := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_knlGetExecNames",
		0,
		0,
		0,
		output,
		4,
	).Low); got != guest.WIPIShortBuffer {
		t.Fatalf("short MC_knlGetExecNames = %d", got)
	}
	var shortList [4]byte
	if err := runtime.CPU.ReadMemory(output, shortList[:]); err != nil {
		t.Fatal(err)
	}
	if shortList[3] != 0 {
		t.Fatalf("short executable list is not terminated: %q", shortList)
	}

	firstArgument, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	secondArgument, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(firstArgument, []byte("--level"), -1); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(secondArgument, []byte("3"), -1); err != nil {
		t.Fatal(err)
	}
	childID := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_knlExecute",
		execName,
		2,
		firstArgument,
		secondArgument,
	).Low)
	if childID != 2 || runtime.LastExecuted != childID ||
		runtime.LastExecuteName != "wipi-app" ||
		len(runtime.LastExecuteArgs) != 2 ||
		runtime.LastExecuteArgs[0] != "--level" ||
		runtime.LastExecuteArgs[1] != "3" {
		t.Fatalf(
			"executed child = %d, last %d/%q/%v",
			childID,
			runtime.LastExecuted,
			runtime.LastExecuteName,
			runtime.LastExecuteArgs,
		)
	}
	child := runtime.Programs[childID]
	if child == nil || !child.Running || child.ParentID != 1 {
		t.Fatalf("child program = %+v", child)
	}

	info, err := runtime.Heap.Allocate(12, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_knlGetProgramInfo",
		info,
		12,
	).Low); got != 2 {
		t.Fatalf("MC_knlGetProgramInfo = %d", got)
	}
	if firstType, _ := runtime.ReadU32(info + 4); int32(firstType) != ProgramTypeCApp {
		t.Fatalf("current program type = %d", firstType)
	}
	if childType, _ := runtime.ReadU32(info + 8); int32(childType) != ProgramTypeCApp {
		t.Fatalf("child program type = %d", childType)
	}
	if got := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_knlProgramStop",
		uint32(childID),
	).Low); got != 0 {
		t.Fatalf("MC_knlProgramStop = %d", got)
	}
	if child.Running {
		t.Fatal("child program is still running")
	}
	if got := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_knlProgramStop",
		1,
	).Low); got != wipiAccessDenied {
		t.Fatalf("stopping current program = %d", got)
	}
	if runtime.Stats.UnimplementedCalls != 0 {
		t.Fatalf("program API stats = %+v", runtime.Stats)
	}
}

func TestWIPIRuntimeGraphicsEventQueueIsBounded(t *testing.T) {
	runtime := newPublicRuntime(t)
	for index := 0; index < wipiMaxGraphicsEvents; index++ {
		result := dispatchPublicAPI(
			t,
			runtime,
			"MC_grpPostEvent",
			uint32(index),
			uint32(index+1),
			uint32(index+2),
			uint32(index+3),
		)
		if result.Low != 0 {
			t.Fatalf("MC_grpPostEvent %d = %d", index, int32(result.Low))
		}
	}
	if got := runtime.GraphicsEvents[7]; got != (GraphicsEvent{
		ID: 7, Kind: 8, Param1: 9, Param2: 10,
	}) {
		t.Fatalf("queued event = %+v", got)
	}
	if got := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_grpPostEvent",
		100,
		101,
		102,
		103,
	).Low); got != guest.WIPINoMemory {
		t.Fatalf("full MC_grpPostEvent = %d", got)
	}
}

func TestWIPIRuntimeGraphicsPresentsGuestFramebuffer(t *testing.T) {
	runtime := newPublicRuntime(t)
	screen := dispatchPublicAPI(t, runtime, "MC_grpGetScreenFrameBuffer", 0).Low
	if screen == 0 {
		t.Fatal("screen framebuffer is null")
	}
	context, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpInitContext", context)
	dispatchPublicAPI(t, runtime, "MC_grpSetContext", context, 1, 0x00123456)
	dispatchPublicAPI(t, runtime, "MC_grpFillRect", screen, 2, 3, 4, 5, context)
	dispatchPublicAPI(t, runtime, "MC_grpFlushLcd", 0, screen, 0, 0, 16, 12)

	if got := runtime.Frame.RGBAAt(3, 4); got != (color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}) {
		t.Fatalf("presented pixel = %#v", got)
	}
	if runtime.Stats.PresentCount != 1 {
		t.Fatalf("present count = %d", runtime.Stats.PresentCount)
	}
}

// Some titles (e.g. 데몬헌터) draw into an offscreen back buffer and call
// MC_grpFlushLcd on that buffer directly. The presentation service only lets the
// screen surface present, so the flush must be redirected onto the screen rather
// than failing the single-owner check.
func TestWIPIRuntimeFlushLcdRedirectsOffscreenBuffer(t *testing.T) {
	runtime := newPublicRuntime(t)
	screen := dispatchPublicAPI(t, runtime, "MC_grpGetScreenFrameBuffer", 0).Low
	if screen == 0 {
		t.Fatal("screen framebuffer is null")
	}
	offscreen := dispatchPublicAPI(
		t, runtime, "MC_grpCreateOffScreenFrameBuffer", 16, 12).Low
	if offscreen == 0 || offscreen == screen {
		t.Fatalf("offscreen framebuffer = 0x%08x (screen 0x%08x)", offscreen, screen)
	}
	context, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpInitContext", context)
	dispatchPublicAPI(t, runtime, "MC_grpSetContext", context, 1, 0x00abcdef)
	dispatchPublicAPI(t, runtime, "MC_grpFillRect", offscreen, 0, 0, 16, 12, context)
	// Flush the offscreen buffer, not the screen handle.
	dispatchPublicAPI(t, runtime, "MC_grpFlushLcd", 0, offscreen, 0, 0, 16, 12)
	if got := runtime.Frame.RGBAAt(5, 5); got != (color.RGBA{R: 0xab, G: 0xcd, B: 0xef, A: 0xff}) {
		t.Fatalf("presented pixel = %#v, want the offscreen fill colour", got)
	}
	if runtime.Stats.PresentCount != 1 {
		t.Fatalf("present count = %d", runtime.Stats.PresentCount)
	}
}

// A context colour is already a device pixel: a Clet converts once with
// MC_grpGetPixelFromRGB and hands that value to MC_grpSetContext. 영웅서기3
// spells white as MC_grpGetPixelFromRGB(255, 255, 255) and 블레이드마스터4 sets
// each palette entry the same way, so converting again on the way to the
// framebuffer would re-encode an already-encoded pixel.
func TestWIPIRuntimeContextColourIsADevicePixel(t *testing.T) {
	runtime := newPublicRuntime(t)
	runtime.framebufferBits = 16
	screen := dispatchPublicAPI(t, runtime, "MC_grpGetScreenFrameBuffer", 0).Low
	framebuffer := runtime.Framebuffers[screen]
	context, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	colour := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpGetPixelFromRGB",
		0x4b,
		0xa8,
		0x1c,
	).Low
	dispatchPublicAPI(t, runtime, "MC_grpInitContext", context)
	dispatchPublicAPI(t, runtime, "MC_grpSetContext", context, 1, colour)
	dispatchPublicAPI(t, runtime, "MC_grpPutPixel", screen, 1, 1, context)

	var raw [2]byte
	if err := runtime.CPU.ReadMemory(
		framebuffer.Pixels+uint32(1*framebuffer.Width+1)*2,
		raw[:],
	); err != nil {
		t.Fatal(err)
	}
	got := binary.LittleEndian.Uint16(raw[:])
	if want := uint16(0x4d43); got != want {
		t.Fatalf("stored pixel = 0x%04x, want 0x%04x", got, want)
	}
	dispatchPublicAPI(t, runtime, "MC_grpFlushLcd", 0, screen, 0, 0, 16, 12)
	if presented := runtime.Frame.RGBAAt(1, 1); presented != (color.RGBA{
		R: 0x4a,
		G: 0xaa,
		B: 0x18,
		A: 0xff,
	}) {
		t.Fatalf("presented pixel = %#v, want the requested colour", presented)
	}
}

func TestWIPIRuntimeGraphicsSupportsRGB565Framebuffer(t *testing.T) {
	runtime := newPublicRuntime(t)
	runtime.framebufferBits = 16
	screen := dispatchPublicAPI(t, runtime, "MC_grpGetScreenFrameBuffer", 0).Low
	framebuffer := runtime.Framebuffers[screen]
	if framebuffer.BitsPerPixel != 16 {
		t.Fatalf("framebuffer bits = %d", framebuffer.BitsPerPixel)
	}
	var descriptor [24]byte
	if err := runtime.CPU.ReadMemory(screen, descriptor[:]); err != nil {
		t.Fatal(err)
	}
	if stride := binary.LittleEndian.Uint32(descriptor[12:16]); stride != 32 {
		t.Fatalf("framebuffer stride = %d", stride)
	}
	if bits := binary.LittleEndian.Uint32(descriptor[16:20]); bits != 16 {
		t.Fatalf("framebuffer descriptor bits = %d", bits)
	}

	// Colours reach a context through MC_grpGetPixelFromRGB, which spells them
	// the way this screen stores them.
	red := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpGetPixelFromRGB",
		255,
		0,
		0,
	).Low
	if red != 0xf800 {
		t.Fatalf("device red = 0x%06x", red)
	}
	context, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpInitContext", context)
	dispatchPublicAPI(t, runtime, "MC_grpSetContext", context, 1, red)
	dispatchPublicAPI(t, runtime, "MC_grpPutPixel", screen, 2, 3, context)

	var raw [2]byte
	if err := runtime.CPU.ReadMemory(
		framebuffer.Pixels+uint32(3*framebuffer.Width+2)*2,
		raw[:],
	); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(raw[:]); got != 0xf800 {
		t.Fatalf("raw RGB565 pixel = 0x%04x", got)
	}
	dispatchPublicAPI(t, runtime, "MC_grpFlushLcd", 0, screen, 0, 0, 16, 12)
	if got := runtime.Frame.RGBAAt(2, 3); got != (color.RGBA{
		R: 0xff,
		A: 0xff,
	}) {
		t.Fatalf("presented RGB565 pixel = %#v", got)
	}

	displayInfo, err := runtime.Heap.Allocate(36, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpGetDisplayInfo", 0, displayInfo)
	var encoded [36]byte
	if err := runtime.CPU.ReadMemory(displayInfo, encoded[:]); err != nil {
		t.Fatal(err)
	}
	values := make([]uint32, 9)
	for index := range values {
		values[index] = binary.LittleEndian.Uint32(encoded[index*4:])
	}
	if values[0] != 16 || values[1] != 16 || values[4] != 32 ||
		values[6] != 0xf800 || values[7] != 0x001f || values[8] != 0x07e0 {
		t.Fatalf("RGB565 display info = %#v", values)
	}
}

func TestWIPIRuntimeGraphicsContextAcceptsImmediateScalarValues(t *testing.T) {
	runtime := newPublicRuntime(t)
	contextAddress, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpInitContext", contextAddress)

	dispatchPublicAPI(t, runtime, "MC_grpSetContext", contextAddress, 1, 0x00ffff00)
	context, err := runtime.context(contextAddress)
	if err != nil {
		t.Fatal(err)
	}
	if context.foreground != 0x00ffff00 {
		t.Fatalf("immediate foreground = 0x%08x", context.foreground)
	}

	dispatchPublicAPI(t, runtime, "MC_grpSetContext", contextAddress, 1, 0)
	context, err = runtime.context(contextAddress)
	if err != nil {
		t.Fatal(err)
	}
	if context.foreground != 0 {
		t.Fatalf("zero immediate foreground = 0x%08x", context.foreground)
	}
}

func TestWIPIRuntimeGraphicsPixelOperationUsesCallbackResult(t *testing.T) {
	runtime := newPublicRuntime(t)
	screen := dispatchPublicAPI(t, runtime, "MC_grpGetScreenFrameBuffer", 0).Low
	contextAddress, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpInitContext", contextAddress)

	setContextValue := func(index uint32, value uint32) {
		t.Helper()
		dispatchPublicAPI(t, runtime, "MC_grpSetContext", contextAddress, index, value)
	}
	setContextValue(1, 0x00123456)
	setContextValue(5, 0x02000001)
	setContextValue(6, ^uint32(6))

	var callback GuestCallback
	runtime.InvokeSync = func(_ context.Context, current GuestCallback) (uint32, error) {
		callback = current
		return 0x00abcdef, nil
	}
	dispatchPublicAPI(t, runtime, "MC_grpPutPixel", screen, 2, 3, contextAddress)

	if callback.Procedure != 0x02000001 ||
		callback.Args[0] != 0x00123456 ||
		callback.Args[1] != 0 ||
		int32(callback.Args[2]) != -7 {
		t.Fatalf("pixel callback = %+v", callback)
	}
	framebuffer := runtime.Framebuffers[screen]
	pixel, err := runtime.ReadU32(framebuffer.Pixels + uint32(3*framebuffer.Width+2)*4)
	if err != nil {
		t.Fatal(err)
	}
	if pixel != 0x00abcdef {
		t.Fatalf("pixel callback result = 0x%08x", pixel)
	}
}

func TestWIPIRuntimeTreatsMalformedModeledCallsAsImplemented(t *testing.T) {
	runtime := newPublicRuntime(t)
	result := dispatchPublicAPI(t, runtime, "MC_grpCreateImage")
	if int32(result.Low) != guest.WIPIInvalid {
		t.Fatalf("malformed graphics return = %d", int32(result.Low))
	}
	if runtime.Stats.ImplementedCalls != 1 || runtime.Stats.UnimplementedCalls != 0 {
		t.Fatalf("stats = %+v", runtime.Stats)
	}
	if names := runtime.UnimplementedNames(); len(names) != 0 {
		t.Fatalf("unimplemented names = %v", names)
	}
}

func TestWIPIRuntimeImageDecodeDrawEncodeAndDestroy(t *testing.T) {
	runtime := newPublicRuntime(t)
	source := image.NewRGBA(image.Rect(0, 0, 2, 2))
	source.SetRGBA(0, 0, color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff})
	source.SetRGBA(1, 0, color.RGBA{R: 0x78, G: 0x9a, B: 0xbc, A: 0xff})
	var payload bytes.Buffer
	if err := png.Encode(&payload, source); err != nil {
		t.Fatal(err)
	}
	buffer, err := runtime.Heap.Allocate(uint32(payload.Len()), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(buffer, payload.Bytes()); err != nil {
		t.Fatal(err)
	}
	output, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	if result := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpCreateImage",
		output,
		buffer,
		0,
		uint32(payload.Len()),
	); result.Low != uint32(guest.WIPIImageDone) {
		t.Fatalf("MC_grpCreateImage = %d", int32(result.Low))
	}
	handle, err := runtime.ReadU32(output)
	if err != nil || handle == 0 {
		t.Fatalf("image handle = 0x%08x, %v", handle, err)
	}
	if _, exists := runtime.Heap.Allocations[buffer]; exists {
		t.Fatal("static image source buffer was not released")
	}
	for property, expected := range map[uint32]uint32{1: 0, 4: 2, 5: 2, 6: 32} {
		if got := dispatchPublicAPI(
			t,
			runtime,
			"MC_grpGetImageProperty",
			handle,
			property,
		).Low; got != expected {
			t.Fatalf("image property %d = %d", property, got)
		}
	}
	imageFramebuffer := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpGetImageFrameBuffer",
		handle,
	).Low
	screen := dispatchPublicAPI(t, runtime, "MC_grpGetScreenFrameBuffer", 0).Low
	dispatchPublicAPI(
		t,
		runtime,
		"MC_grpDrawImage",
		screen,
		3,
		4,
		2,
		2,
		handle,
		0,
		0,
		0,
	)
	dispatchPublicAPI(t, runtime, "MC_grpFlushLcd", 0, screen, 0, 0, 16, 12)
	if got := runtime.Frame.RGBAAt(3, 4); got != (color.RGBA{
		R: 0x12,
		G: 0x34,
		B: 0x56,
		A: 0xff,
	}) {
		t.Fatalf("drawn image pixel = %#v", got)
	}

	length, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	encoded := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpEncodeImage",
		imageFramebuffer,
		0,
		0,
		2,
		2,
		length,
	).Low
	encodedLength, err := runtime.ReadU32(length)
	if err != nil || encoded == 0 || encodedLength < 54 {
		t.Fatalf("encoded BMP = 0x%08x/%d, %v", encoded, encodedLength, err)
	}
	bmp := make([]byte, encodedLength)
	if err := runtime.CPU.ReadMemory(encoded, bmp); err != nil {
		t.Fatal(err)
	}
	decodedBMP, err := decodeWIPIBMP(bmp)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodedBMP.RGBAAt(0, 0); got != (color.RGBA{
		R: 0x12,
		G: 0x34,
		B: 0x56,
		A: 0xff,
	}) {
		t.Fatalf("encoded BMP pixel = %#v", got)
	}

	descriptor, ok, err := runtime.readImage(handle)
	if err != nil || !ok {
		t.Fatalf("image descriptor missing before destroy: %v, %v", ok, err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpDestroyImage", handle)
	if _, exists := runtime.Heap.Allocations[handle]; exists {
		t.Fatal("image descriptor was not released")
	}
	if _, exists := runtime.Framebuffers[descriptor.framebuffer]; exists {
		t.Fatal("image framebuffer was not released")
	}
}

func TestWIPIRuntimeAnimatedGIFAdvancesFrames(t *testing.T) {
	runtime := newPublicRuntime(t)
	palette := color.Palette{color.Black, color.RGBA{R: 0xff, A: 0xff}, color.RGBA{G: 0xff, A: 0xff}}
	first := image.NewPaletted(image.Rect(0, 0, 2, 1), palette)
	second := image.NewPaletted(image.Rect(0, 0, 2, 1), palette)
	first.Pix[0], first.Pix[1] = 1, 1
	second.Pix[0], second.Pix[1] = 2, 2
	var payload bytes.Buffer
	if err := gif.EncodeAll(&payload, &gif.GIF{
		Image:     []*image.Paletted{first, second},
		Delay:     []int{7, 9},
		LoopCount: 2,
		Config: image.Config{
			ColorModel: palette,
			Width:      2,
			Height:     1,
		},
	}); err != nil {
		t.Fatal(err)
	}
	buffer, err := runtime.Heap.Allocate(uint32(payload.Len()), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(buffer, payload.Bytes()); err != nil {
		t.Fatal(err)
	}
	output, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	if result := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpCreateImage",
		output,
		buffer,
		0,
		uint32(payload.Len()),
	); result.Low != uint32(guest.WIPIImageDone) {
		t.Fatalf("animated MC_grpCreateImage = %d", int32(result.Low))
	}
	handle, _ := runtime.ReadU32(output)
	for property, expected := range map[uint32]uint32{1: 1, 2: 70, 3: 2} {
		if got := dispatchPublicAPI(
			t,
			runtime,
			"MC_grpGetImageProperty",
			handle,
			property,
		).Low; got != expected {
			t.Fatalf("animated image property %d = %d", property, got)
		}
	}
	if result := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpDecodeNextImage",
		handle,
	); result.Low != uint32(guest.WIPIImageDone) {
		t.Fatalf("MC_grpDecodeNextImage = %d", int32(result.Low))
	}
	if delay := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpGetImageProperty",
		handle,
		2,
	).Low; delay != 90 {
		t.Fatalf("second-frame delay = %d", delay)
	}
	framebuffer := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpGetImageFrameBuffer",
		handle,
	).Low
	pixel, err := runtime.ReadU32(runtime.Framebuffers[framebuffer].Pixels)
	if err != nil || pixel != 0x0000ff00 {
		t.Fatalf("second-frame pixel = 0x%08x, %v", pixel, err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpDestroyImage", handle)
	if _, exists := runtime.Heap.Allocations[buffer]; exists {
		t.Fatal("animated image source buffer was not released on destroy")
	}
}

func TestWIPIRuntimeAdvancedGraphicsPrimitives(t *testing.T) {
	runtime := newPublicRuntime(t)
	framebuffer := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpCreateOffScreenFrameBuffer",
		16,
		12,
	).Low
	context, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpInitContext", context)
	dispatchPublicAPI(t, runtime, "MC_grpSetContext", context, 1, 0x00a1b2c3)

	dispatchPublicAPI(
		t,
		runtime,
		"MC_grpDrawArc",
		framebuffer,
		2,
		2,
		8,
		8,
		0,
		360,
		context,
	)
	dispatchPublicAPI(
		t,
		runtime,
		"MC_grpFillArc",
		framebuffer,
		2,
		2,
		8,
		8,
		0,
		360,
		context,
	)

	text, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(text, []byte("A \x00")); err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(
		t,
		runtime,
		"MC_grpDrawString",
		framebuffer,
		0,
		10,
		text,
		^uint32(0),
		context,
	)

	xCoordinates, err := runtime.Heap.Allocate(12, true)
	if err != nil {
		t.Fatal(err)
	}
	yCoordinates, err := runtime.Heap.Allocate(12, true)
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range []uint32{8, 14, 8} {
		if err := runtime.WriteU32(xCoordinates+uint32(index*4), value); err != nil {
			t.Fatal(err)
		}
	}
	for index, value := range []uint32{1, 6, 10} {
		if err := runtime.WriteU32(yCoordinates+uint32(index*4), value); err != nil {
			t.Fatal(err)
		}
	}
	dispatchPublicAPI(
		t,
		runtime,
		"MC_grpDrawPolygon",
		framebuffer,
		xCoordinates,
		yCoordinates,
		3,
		context,
	)
	dispatchPublicAPI(
		t,
		runtime,
		"MC_grpDrawFillPolygon",
		framebuffer,
		xCoordinates,
		yCoordinates,
		3,
		context,
	)

	pixels := runtime.Framebuffers[framebuffer].Pixels
	for _, coordinate := range [][2]int{{2, 6}, {6, 6}, {0, 0}, {9, 6}} {
		pixel, err := runtime.ReadU32(
			pixels + uint32(coordinate[1]*16+coordinate[0])*4,
		)
		if err != nil || pixel != 0x00a1b2c3 {
			t.Fatalf("pixel %v = 0x%08x, %v", coordinate, pixel, err)
		}
	}
}

func TestWIPIRuntimeFilesystemRoundTripAndListing(t *testing.T) {
	runtime := newPublicRuntime(t)
	pathAddress, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(pathAddress, []byte("save/game.dat"), -1); err != nil {
		t.Fatal(err)
	}
	fd := int32(dispatchPublicAPI(t, runtime, "MC_fsOpen", pathAddress, 8, 0).Low)
	if fd < 3 {
		t.Fatalf("MC_fsOpen descriptor = %d", fd)
	}
	dataAddress, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(dataAddress, []byte("ARAM")); err != nil {
		t.Fatal(err)
	}
	if got := dispatchPublicAPI(t, runtime, "MC_fsWrite", uint32(fd), dataAddress, 4).Low; got != 4 {
		t.Fatalf("MC_fsWrite = %d", got)
	}
	if got := dispatchPublicAPI(t, runtime, "MC_fsSeek", uint32(fd), 0, 0).Low; got != 0 {
		t.Fatalf("MC_fsSeek = %d", got)
	}
	outputAddress, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatchPublicAPI(t, runtime, "MC_fsRead", uint32(fd), outputAddress, 4).Low; got != 4 {
		t.Fatalf("MC_fsRead = %d", got)
	}
	var output [4]byte
	if err := runtime.CPU.ReadMemory(outputAddress, output[:]); err != nil {
		t.Fatal(err)
	}
	if string(output[:]) != "ARAM" {
		t.Fatalf("read data = %q", output)
	}

	directoryAddress, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(directoryAddress, []byte("save"), -1); err != nil {
		t.Fatal(err)
	}
	listAddress, err := runtime.Heap.Allocate(64, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatchPublicAPI(t, runtime, "MC_fsList", directoryAddress, listAddress, 64, 0).Low; got != 0 {
		t.Fatalf("MC_fsList = 0x%08x", got)
	}
	listing := make([]byte, len("game.dat")+2)
	if err := runtime.CPU.ReadMemory(listAddress, listing); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(listing, []byte("game.dat\x00\x00")) {
		t.Fatalf("directory listing = %q", listing)
	}
}

func TestWIPIRuntimeDatabaseRecords(t *testing.T) {
	runtime := newPublicRuntime(t)
	nameAddress, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(nameAddress, []byte("scores"), -1); err != nil {
		t.Fatal(err)
	}
	database := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_dbOpenDataBase",
		nameAddress,
		8,
		1,
		0,
	).Low)
	if database < 1 {
		t.Fatalf("database handle = %d", database)
	}
	recordAddress, err := runtime.Heap.Allocate(8, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(recordAddress, []byte("score-1")); err != nil {
		t.Fatal(err)
	}
	recordID := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_dbInsertRecord",
		uint32(database),
		recordAddress,
		7,
	).Low)
	if recordID != 1 {
		t.Fatalf("record ID = %d", recordID)
	}
	output, err := runtime.Heap.Allocate(8, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatchPublicAPI(
		t,
		runtime,
		"MC_dbSelectRecord",
		uint32(database),
		uint32(recordID),
		output,
		8,
	).Low; got != 0 {
		t.Fatalf("MC_dbSelectRecord = 0x%08x", got)
	}
	var restored [8]byte
	if err := runtime.CPU.ReadMemory(output, restored[:]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored[:7], []byte("score-1")) || restored[7] != 0 {
		t.Fatalf("selected record = %q", restored)
	}
}

func TestWIPIRuntimeDatabaseSortUsesGuestCompareAndFilter(t *testing.T) {
	runtime := newPublicRuntime(t)
	nameAddress, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(nameAddress, []byte("sorted"), -1); err != nil {
		t.Fatal(err)
	}
	database := dispatchPublicAPI(
		t,
		runtime,
		"MC_dbOpenDataBase",
		nameAddress,
		4,
		1,
		0,
	).Low
	recordAddress, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	for index, value := range []byte{'a', 'b', 'c'} {
		if err := runtime.CPU.WriteMemory(recordAddress, []byte{value, 0, 0, 0}); err != nil {
			t.Fatal(err)
		}
		recordID := dispatchPublicAPI(
			t,
			runtime,
			"MC_dbInsertRecord",
			database,
			recordAddress,
			1,
		).Low
		if recordID != uint32(index+1) {
			t.Fatalf("record %q ID = %d", value, recordID)
		}
	}

	const (
		compare = uint32(0x02000101)
		filter  = uint32(0x02000201)
	)
	runtime.InvokeSync = func(_ context.Context, callback GuestCallback) (uint32, error) {
		readByte := func(address uint32) byte {
			var value [1]byte
			if err := runtime.CPU.ReadMemory(address, value[:]); err != nil {
				t.Fatal(err)
			}
			return value[0]
		}
		switch callback.Procedure {
		case filter:
			if readByte(callback.Args[0]) == 'b' {
				return 0, nil
			}
			return 1, nil
		case compare:
			left := readByte(callback.Args[0])
			right := readByte(callback.Args[1])
			return uint32(int32(right) - int32(left)), nil
		default:
			t.Fatalf("unexpected database callback 0x%08x", callback.Procedure)
			return 0, nil
		}
	}
	output, err := runtime.Heap.Allocate(12, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatchPublicAPI(
		t,
		runtime,
		"MC_dbSortRecords",
		database,
		output,
		12,
		compare,
		filter,
	).Low; got != 2 {
		t.Fatalf("MC_dbSortRecords = %d", int32(got))
	}
	var encoded [8]byte
	if err := runtime.CPU.ReadMemory(output, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if first, second := binary.LittleEndian.Uint32(encoded[0:4]),
		binary.LittleEndian.Uint32(encoded[4:8]); first != 3 || second != 1 {
		t.Fatalf("sorted record IDs = [%d %d]", first, second)
	}
	if got := int32(dispatchPublicAPI(
		t,
		runtime,
		"MC_dbListRecords",
		database,
		output,
		4,
	).Low); got != wipiDBShortBuffer {
		t.Fatalf("short MC_dbListRecords = %d", got)
	}
}

func TestWIPIRuntimeUICComponentState(t *testing.T) {
	runtime := newPublicRuntime(t)
	context := dispatchPublicAPI(t, runtime, "MC_uicCreateApplicationContext").Low
	if context == 0 {
		t.Fatal("application context is null")
	}
	className, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(className, []byte("Label"), -1); err != nil {
		t.Fatal(err)
	}
	class := dispatchPublicAPI(t, runtime, "MC_uicGetClass", className).Low
	component := dispatchPublicAPI(t, runtime, "MC_uicCreate", context, class).Low
	if class == 0 || component == 0 {
		t.Fatalf("class = 0x%08x, component = 0x%08x", class, component)
	}
	label, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(label, []byte("Hello"), -1); err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_uicSetLabel", component, label)
	labelResult := dispatchPublicAPI(t, runtime, "MC_uicGetLabel", component).Low
	got, err := runtime.ReadCString(labelResult)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "Hello" {
		t.Fatalf("component label = %q", got)
	}

	text, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(text, []byte("ARAM")); err != nil {
		t.Fatal(err)
	}
	if got := dispatchPublicAPI(t, runtime, "MC_uicInsertText", component, 0, text, 4).Low; got != 4 {
		t.Fatalf("MC_uicInsertText = %d", got)
	}
	if got := dispatchPublicAPI(t, runtime, "MC_uicGetTextSize", component).Low; got != 4 {
		t.Fatalf("MC_uicGetTextSize = %d", got)
	}
}

func TestWIPIRuntimeUICHandleEventUsesSynchronousCallbacks(t *testing.T) {
	runtime := newPublicRuntime(t)
	applicationContext := dispatchPublicAPI(
		t,
		runtime,
		"MC_uicCreateApplicationContext",
	).Low
	className, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(className, []byte("Text"), -1); err != nil {
		t.Fatal(err)
	}
	class := dispatchPublicAPI(t, runtime, "MC_uicGetClass", className).Low
	component := dispatchPublicAPI(
		t,
		runtime,
		"MC_uicCreate",
		applicationContext,
		class,
	).Low

	const (
		handler   = uint32(0x02000301)
		key       = uint32(0x02000401)
		client    = uint32(0x55667788)
		eventType = uint32(17)
	)
	dispatchPublicAPI(t, runtime, "MC_uicSetEventHandler", component, handler)
	dispatchPublicAPI(t, runtime, "MC_uicSetCallback", component, 5, key, client)
	var calls []uint32
	runtime.InvokeSync = func(_ context.Context, callback GuestCallback) (uint32, error) {
		calls = append(calls, callback.Procedure)
		switch callback.Procedure {
		case handler:
			if callback.Args != [4]uint32{component, eventType, 23, 42} {
				t.Fatalf("event handler args = %#v", callback.Args)
			}
			return 1, nil
		case key:
			if callback.Args[0] != component || callback.Args[2] != client {
				t.Fatalf("key callback args = %#v", callback.Args)
			}
			value, err := runtime.ReadU32(callback.Args[1])
			if err != nil {
				t.Fatal(err)
			}
			if value != eventType {
				t.Fatalf("key callback event type = %d", value)
			}
			return 0, nil
		default:
			t.Fatalf("unexpected UIC callback 0x%08x", callback.Procedure)
			return 0, nil
		}
	}
	if got := dispatchPublicAPI(
		t,
		runtime,
		"MC_uicHandleEvent",
		component,
		eventType,
		23,
		42,
	).Low; got != 1 {
		t.Fatalf("accepted MC_uicHandleEvent = %d", got)
	}
	if !slices.Equal(calls, []uint32{handler, key}) {
		t.Fatalf("UIC callback order = %#v", calls)
	}

	calls = nil
	runtime.InvokeSync = func(_ context.Context, callback GuestCallback) (uint32, error) {
		calls = append(calls, callback.Procedure)
		return 0, nil
	}
	if got := dispatchPublicAPI(
		t,
		runtime,
		"MC_uicHandleEvent",
		component,
		eventType,
		23,
		42,
	).Low; got != 0 {
		t.Fatalf("rejected MC_uicHandleEvent = %d", got)
	}
	if !slices.Equal(calls, []uint32{handler}) {
		t.Fatalf("rejected UIC callbacks = %#v", calls)
	}
}

func TestWIPIRuntimeUICRepaintRecordsDamageAndSchedulesPaint(t *testing.T) {
	runtime := newPublicRuntime(t)
	context := dispatchPublicAPI(t, runtime, "MC_uicCreateApplicationContext").Low
	className, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(className, []byte("Canvas"), -1); err != nil {
		t.Fatal(err)
	}
	class := dispatchPublicAPI(t, runtime, "MC_uicGetClass", className).Low
	component := dispatchPublicAPI(t, runtime, "MC_uicCreate", context, class).Low
	dispatchPublicAPI(t, runtime, "MC_uicConfigure", component, 3, 4, 100, 50, 3)
	dispatchPublicAPI(
		t,
		runtime,
		"MC_uicSetCallback",
		component,
		2,
		0x02000001,
		0x1234,
	)
	dispatchPublicAPI(
		t,
		runtime,
		"MC_uicRepaint",
		component,
		10,
		5,
		^uint32(0),
		^uint32(0),
	)
	if len(runtime.UicRepaints) != 1 ||
		runtime.UicRepaints[0] != (UICRepaint{
			Component: component,
			X:         10,
			Y:         5,
			Width:     90,
			Height:    45,
		}) {
		t.Fatalf("repaint trace = %+v", runtime.UicRepaints)
	}
	if len(runtime.PendingCallbacks) != 1 ||
		runtime.PendingCallbacks[0].Procedure != 0x02000001 ||
		runtime.PendingCallbacks[0].Args != [4]uint32{component, 0, 0x1234, 0} {
		t.Fatalf("paint callback = %+v", runtime.PendingCallbacks)
	}
}

func TestWIPIRuntimeMediaAndSerialModels(t *testing.T) {
	runtime := newPublicRuntime(t)
	mediaType, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(mediaType, []byte("audio/midi"), -1); err != nil {
		t.Fatal(err)
	}
	clip := dispatchPublicAPI(t, runtime, "MC_mdaClipCreate", mediaType, 64, 0).Low
	if clip == 0 {
		t.Fatal("media clip is null")
	}
	data, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(data, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if got := dispatchPublicAPI(t, runtime, "MC_mdaClipPutData", clip, data, 4).Low; got != 4 {
		t.Fatalf("MC_mdaClipPutData = %d", got)
	}
	dispatchPublicAPI(t, runtime, "MC_mdaPlay", clip, 1)
	if runtime.MediaClips[clip].State != 1 || !runtime.MediaClips[clip].Repeat {
		t.Fatalf("media clip state = %+v", runtime.MediaClips[clip])
	}

	serial := int32(dispatchPublicAPI(t, runtime, "MC_srlOpen", 0, 0).Low)
	if serial < 1 {
		t.Fatalf("serial descriptor = %d", serial)
	}
	if got := dispatchPublicAPI(t, runtime, "MC_srlWrite", uint32(serial), data, 4).Low; got != 4 {
		t.Fatalf("MC_srlWrite = %d", got)
	}
	output, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := dispatchPublicAPI(t, runtime, "MC_srlRead", uint32(serial), output, 4).Low; got != 4 {
		t.Fatalf("MC_srlRead = %d", got)
	}
	var loopback [4]byte
	if err := runtime.CPU.ReadMemory(output, loopback[:]); err != nil {
		t.Fatal(err)
	}
	if loopback != [4]byte{1, 2, 3, 4} {
		t.Fatalf("serial loopback = %v", loopback)
	}
}

func TestWIPIRuntimeOfflineNetworkAndHTTPModels(t *testing.T) {
	runtime := newPublicRuntime(t)
	dispatchPublicAPI(t, runtime, "MC_netConnect", 0, 0)
	socket := int32(dispatchPublicAPI(t, runtime, "MC_netSocket", 2, 1).Low)
	if socket < 1 {
		t.Fatalf("socket descriptor = %d", socket)
	}
	if got := dispatchPublicAPI(
		t,
		runtime,
		"MC_netSocketConnect",
		uint32(socket),
		0x7f000001,
		8080,
		0,
		0,
	).Low; got != 0 {
		t.Fatalf("MC_netSocketConnect = 0x%08x", got)
	}
	if !runtime.sockets[socket].connected {
		t.Fatal("socket did not enter connected state")
	}

	url, err := runtime.Heap.Allocate(64, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.writeCString(url, []byte("https://example.invalid/"), -1); err != nil {
		t.Fatal(err)
	}
	request := int32(dispatchPublicAPI(t, runtime, "MC_netHttpOpen", url).Low)
	if request < 1 {
		t.Fatalf("HTTP descriptor = %d", request)
	}
	if got := dispatchPublicAPI(t, runtime, "MC_netHttpConnect", uint32(request), 0, 0).Low; got != 0 {
		t.Fatalf("MC_netHttpConnect = 0x%08x", got)
	}
	if code := dispatchPublicAPI(t, runtime, "MC_netHttpGetResponseCode", uint32(request)).Low; code != 204 {
		t.Fatalf("HTTP response code = %d", code)
	}
}

// A Raptor title hands its clip a completion callback and waits to be told the
// clip finished before it frees the handle and builds the next one, so a stop
// has to report completion the way the handset does. 제노니아1 played its intro
// jingle and then stayed silent for the rest of the session without this
// (issue #49).
func TestRaptorStopClipReportsCompletionAndFrees(t *testing.T) {
	runtime := newPublicRuntime(t)
	handle, err := runtime.RaptorCreateClip("Yamaha_MA3", 64, 0x02000001)
	if err != nil || handle == 0 {
		t.Fatalf("RaptorCreateClip = 0x%08x, err=%v", handle, err)
	}
	data, err := runtime.Heap.Allocate(16, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(data, []byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if !runtime.RaptorPutClipData(handle, data, 4) {
		t.Fatal("RaptorPutClipData rejected the clip source")
	}
	if !runtime.RaptorPlayClip(handle, true) {
		t.Fatal("RaptorPlayClip refused to start the clip")
	}
	runtime.PendingCallbacks = nil

	runtime.RaptorStopClip(handle, false)
	clip := runtime.MediaClips[handle]
	if clip == nil || clip.State != 0 || clip.Repeat {
		t.Fatalf("stopped clip = %+v", clip)
	}
	if len(runtime.PendingCallbacks) != 1 ||
		runtime.PendingCallbacks[0].Procedure != 0x02000001 ||
		runtime.PendingCallbacks[0].Args !=
			[4]uint32{handle, RaptorClipEndCode, 0, 0} {
		t.Fatalf("completion callback = %+v", runtime.PendingCallbacks)
	}

	// A clip that already stopped stays stopped and reports once, so a title
	// that stops the same handle on every scene change is not flooded.
	runtime.PendingCallbacks = nil
	runtime.RaptorStopClip(handle, false)
	if len(runtime.PendingCallbacks) != 0 {
		t.Fatalf("second stop callback = %+v", runtime.PendingCallbacks)
	}

	runtime.RaptorStopClip(handle, true)
	if _, live := runtime.MediaClips[handle]; live {
		t.Fatal("freed clip is still registered")
	}
	if _, live := runtime.MediaServices[handle]; live {
		t.Fatal("freed clip still owns a media service")
	}
}

// MC_grpGetPixelFromRGB converts an RGB triple into the screen's own pixel
// spelling, which is what makes it useful: a Clet that walks the raw
// framebuffer through MC_grpGetFrameBufferPixels stores the result directly.
// 블레이드마스터4 draws its title splash that way, keeping only the low half of
// the returned word (lsls #16 / lsrs #16) because its screen is 16bpp. Handing
// it 24-bit RGB left it storing the green and blue bytes as an RGB565 pixel,
// so the splash kept its shapes but lost every colour.
func TestWIPIRuntimeGetPixelFromRGBReturnsDevicePixel(t *testing.T) {
	runtime := newPublicRuntime(t)
	runtime.framebufferBits = 16
	screen := dispatchPublicAPI(t, runtime, "MC_grpGetScreenFrameBuffer", 0).Low
	framebuffer := runtime.Framebuffers[screen]

	pixel := dispatchPublicAPI(
		t,
		runtime,
		"MC_grpGetPixelFromRGB",
		0xb7,
		0x31,
		0x09,
	).Low
	if want := uint32(0xb181); pixel != want {
		t.Fatalf("device pixel = 0x%08x, want 0x%04x", pixel, want)
	}

	// Store the low half the way the Clet does, then present the buffer.
	var raw [2]byte
	binary.LittleEndian.PutUint16(raw[:], uint16(pixel))
	offset := framebuffer.Pixels + uint32(4*framebuffer.Width+6)*2
	if err := runtime.CPU.WriteMemory(offset, raw[:]); err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpFlushLcd", 0, screen, 0, 0, 16, 12)
	if got := runtime.Frame.RGBAAt(6, 4); got != (color.RGBA{
		R: 0xb5,
		G: 0x30,
		B: 0x08,
		A: 0xff,
	}) {
		t.Fatalf("presented pixel = %#v, want the requested colour", got)
	}

	// MC_grpGetRGBFromPixel is the same conversion in reverse, so it has to
	// read the device spelling back rather than a 24-bit word.
	components, err := runtime.Heap.Allocate(12, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(
		t,
		runtime,
		"MC_grpGetRGBFromPixel",
		pixel,
		components,
		components+4,
		components+8,
	)
	var encoded [12]byte
	if err := runtime.CPU.ReadMemory(components, encoded[:]); err != nil {
		t.Fatal(err)
	}
	red := binary.LittleEndian.Uint32(encoded[0:4])
	green := binary.LittleEndian.Uint32(encoded[4:8])
	blue := binary.LittleEndian.Uint32(encoded[8:12])
	if red != 0xb5 || green != 0x30 || blue != 0x08 {
		t.Fatalf("decoded RGB = (%d, %d, %d)", red, green, blue)
	}
}

// TestWIPIRuntimeCompactGraphicsContextPlacesScalarsWithoutClipFlag pins LGT
// Raptor's MC_GrpContext spelling. A Raptor Clet reads the struct itself:
// 블레이드마스터4 fills its text strip with the colour it loads back from
// offset 0x10 and keys that colour out when it blits the strip, so writing the
// colour where the SDK struct keeps it (0x14, past a clip_enabled word LGT does
// not have) filled the strip with the clip flag instead and put every string on
// a black plate.
func TestWIPIRuntimeCompactGraphicsContextPlacesScalarsWithoutClipFlag(t *testing.T) {
	runtime := newPublicRuntime(t)
	runtime.CompactGraphicsContext = true
	contextAddress, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpInitContext", contextAddress)
	dispatchPublicAPI(t, runtime, "MC_grpSetContext", contextAddress, 1, 0x0000f81f)
	dispatchPublicAPI(t, runtime, "MC_grpSetContext", contextAddress, 2, 0x00001234)

	foreground, err := runtime.ReadU32(contextAddress + 0x10)
	if err != nil {
		t.Fatal(err)
	}
	if foreground != 0x0000f81f {
		t.Fatalf("compact foreground word = 0x%08x, want 0x0000f81f", foreground)
	}
	background, err := runtime.ReadU32(contextAddress + 0x14)
	if err != nil {
		t.Fatal(err)
	}
	if background != 0x00001234 {
		t.Fatalf("compact background word = 0x%08x, want 0x00001234", background)
	}
	alpha, err := runtime.ReadU32(contextAddress + 0x18)
	if err != nil {
		t.Fatal(err)
	}
	if alpha != 255 {
		t.Fatalf("compact alpha word = %d, want 255", alpha)
	}

	context, err := runtime.context(contextAddress)
	if err != nil {
		t.Fatal(err)
	}
	if context.foreground != 0x0000f81f || context.background != 0x00001234 ||
		context.alpha != 255 {
		t.Fatalf("compact context = %+v", context)
	}

	// MC_grpGetContext must read the same members back.
	output, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	dispatchPublicAPI(t, runtime, "MC_grpGetContext", contextAddress, 1, output)
	readBack, err := runtime.ReadU32(output)
	if err != nil {
		t.Fatal(err)
	}
	if readBack != 0x0000f81f {
		t.Fatalf("compact MC_grpGetContext foreground = 0x%08x", readBack)
	}

	// An untouched context clips nothing: without a clip_enabled word an empty
	// rectangle must not blank the screen.
	blank, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := runtime.context(blank)
	if err != nil {
		t.Fatal(err)
	}
	if empty.clipEnabled {
		t.Fatal("zeroed compact context reports an enabled clip rectangle")
	}
}

// Presenting a non-screen framebuffer is only refused once a screen surface
// exists, so a title can flush an offscreen buffer of its own size first. A
// buffer larger than the host frame must still deliver the overlap rather than
// failing the flush.
func TestPresentOversizedFramebufferCopiesTheOverlap(t *testing.T) {
	runtime := newPublicRuntime(t)
	handle, err := runtime.newFramebuffer(
		runtime.Frame.Bounds().Dx()*2,
		runtime.Frame.Bounds().Dy()*2,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	framebuffer := runtime.Framebuffers[handle]
	pixels := make([]byte,
		int(framebuffer.Width)*int(framebuffer.Height)*
			int(framebuffer.bytesPerPixel()))
	for index := range pixels {
		pixels[index] = 0xff
	}
	if err := runtime.CPU.WriteMemory(framebuffer.Pixels, pixels); err != nil {
		t.Fatal(err)
	}
	if err := runtime.present(handle); err != nil {
		t.Fatalf("flushing an oversized offscreen buffer failed: %v", err)
	}
	if runtime.Frame.Pix[0] == 0 && runtime.Frame.Pix[1] == 0 &&
		runtime.Frame.Pix[2] == 0 {
		t.Fatal("the host frame kept no pixels from the oversized flush")
	}
}
