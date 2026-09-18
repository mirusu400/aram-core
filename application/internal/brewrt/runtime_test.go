package brewrt

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
	"time"

	"bytes"
	"golang.org/x/image/bmp"

	"github.com/mirusu400/aram-core/cpu"
	loaderbrew "github.com/mirusu400/aram-core/loader/brew"
)

func newSyntheticRuntime(t *testing.T) *Runtime {
	t.Helper()
	module := make([]byte, 20)
	for offset, instruction := range []uint32{0xe59f0008, 0xe5820000, 0xe3a00000, 0xe12fff1e, heapBase} {
		binary.LittleEndian.PutUint32(module[offset*4:], instruction)
	}
	runtime, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func TestRuntimeBootstrapsSyntheticARMModule(t *testing.T) {
	// ldr r0,[pc,#8]; str r0,[r2]; mov r0,#0; bx lr; .word heapBase
	module := make([]byte, 20)
	for offset, instruction := range []uint32{0xe59f0008, 0xe5820000, 0xe3a00000, 0xe12fff1e, heapBase} {
		binary.LittleEndian.PutUint32(module[offset*4:], instruction)
	}
	runtime, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err := runtime.Bootstrap(context.Background()); err != nil {
		t.Fatalf("bootstrap synthetic ARM module: %v", err)
	}
	if got := runtime.ModuleObject(); got != heapBase {
		t.Fatalf("module object = 0x%08x, want 0x%08x", got, heapBase)
	}
}

func TestStdlibHelperTableDoesNotAliasRuntimeObjects(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	var word [4]byte
	if err := runtime.cpu.ReadMemory(moduleBase-4, word[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(word[:]); got != helperTableBase {
		t.Fatalf("stdlib table pointer = 0x%08x, want 0x%08x", got, helperTableBase)
	}
	if err := runtime.cpu.ReadMemory(helperTableBase+64*4, word[:]); err != nil {
		t.Fatal(err)
	}
	if got, want := binary.LittleEndian.Uint32(word[:]), helperMethodTrapBase+64*2|1; got != want {
		t.Fatalf("stdlib slot 64 = 0x%08x, want 0x%08x", got, want)
	}
	if err := runtime.cpu.ReadMemory(shellObject, word[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(word[:]); got != shellVTable {
		t.Fatalf("shell object vtable = 0x%08x, want 0x%08x", got, shellVTable)
	}
}

func TestWStrCompressEncodesKoreanTextAsEUCKR(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	source := heapBase + 0x100
	destination := heapBase + 0x120
	wide := make([]byte, 6)
	binary.LittleEndian.PutUint16(wide[0:2], 0xac00)
	binary.LittleEndian.PutUint16(wide[2:4], 'A')
	if err := runtime.cpu.WriteMemory(source, wide); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: source,
		cpu.RegisterR1: 2,
		cpu.RegisterR2: destination,
		cpu.RegisterR3: 8,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + helperWStrCompressSlot*2 + 2)
	if err != nil || !handled {
		t.Fatalf("WSTRCOMPRESS handled=%v err=%v", handled, err)
	}
	var compressed [4]byte
	if err := runtime.cpu.ReadMemory(destination, compressed[:]); err != nil {
		t.Fatal(err)
	}
	if want := [4]byte{0xb0, 0xa1, 'A', 0}; compressed != want {
		t.Fatalf("WSTRCOMPRESS result = % x, want % x", compressed, want)
	}
}

func TestReallocPreservesGuestAllocationContents(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	oldAddress, err := runtime.allocateGuest(8)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(oldAddress, []byte("aramBREW")); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: oldAddress,
		cpu.RegisterR1: 16,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + helperReallocSlot*2 + 2)
	if err != nil || !handled {
		t.Fatalf("realloc handled=%v err=%v", handled, err)
	}
	newAddress, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil || newAddress == 0 || newAddress == oldAddress {
		t.Fatalf("realloc address = 0x%08x err=%v", newAddress, err)
	}
	var contents [8]byte
	if err := runtime.cpu.ReadMemory(newAddress, contents[:]); err != nil {
		t.Fatal(err)
	}
	if string(contents[:]) != "aramBREW" {
		t.Fatalf("realloc contents = %q", contents)
	}
	if _, allocated := runtime.heapAllocated[oldAddress]; allocated {
		t.Fatal("old realloc block remains allocated")
	}
}

func TestRunAppletCodeAllowsLongGuestInitialization(t *testing.T) {
	// ldr r0,[pc,#8]; subs r0,r0,#1; bne loop; bx lr; .word 1100000
	// This executes just over 2.2 million instructions, matching real BREW
	// constructors that legitimately exceeded the former two-million limit.
	module := make([]byte, 20)
	for offset, instruction := range []uint32{
		0xe59f0008, 0xe2500001, 0x1afffffd, 0xe12fff1e, 1_100_000,
	} {
		binary.LittleEndian.PutUint32(module[offset*4:], instruction)
	}
	runtime, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	for register, value := range map[uint32]uint32{
		cpu.RegisterSP: stackBase + stackSize - 16,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.runAppletCode(context.Background(), moduleBase, cpu.ModeARM, "long initialization"); err != nil {
		t.Fatalf("run long guest initialization: %v", err)
	}
}

func TestGuestEntryStackReservesCallerFrameHeadroom(t *testing.T) {
	runtime := newSyntheticRuntime(t)

	if got := stackBase + stackSize - stackEntrySP; got != stackCallerHeadroom || got < 0x1000 {
		t.Fatalf("guest caller-frame headroom = 0x%x, want at least 0x1000", got)
	}
	// Real BREW applets copy fixed-size local buffers whose source begins below
	// the host-provided SP but extends into their caller's frame above it. Keep
	// that caller-frame area mapped instead of placing SP at the mapping's edge.
	const localOffset = uint32(0xd8)
	const copySize = uint32(1024)
	start := stackEntrySP - localOffset
	if end := start + copySize; end > stackBase+stackSize {
		t.Fatalf("guest entry stack copy ends at 0x%08x beyond stack 0x%08x", end, stackBase+stackSize)
	}
	data := make([]byte, copySize)
	if err := runtime.cpu.WriteMemory(start, data); err != nil {
		t.Fatalf("write guest entry caller-frame span: %v", err)
	}
}

func TestHostGuestEntriesUseReservedStack(t *testing.T) {
	readWord := func(t *testing.T, runtime *Runtime, address uint32) uint32 {
		t.Helper()
		var encoded [4]byte
		if err := runtime.cpu.ReadMemory(address, encoded[:]); err != nil {
			t.Fatal(err)
		}
		return binary.LittleEndian.Uint32(encoded[:])
	}
	writeWords := func(t *testing.T, runtime *Runtime, address uint32, words ...uint32) {
		t.Helper()
		encoded := make([]byte, len(words)*4)
		for index, word := range words {
			binary.LittleEndian.PutUint32(encoded[index*4:], word)
		}
		if err := runtime.cpu.WriteMemory(address, encoded); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("bootstrap", func(t *testing.T) {
		// str sp,[r2,#4]; ldr r0,[pc,#4]; str r0,[r2]; bx lr; moduleObject
		module := make([]byte, 20)
		for index, word := range []uint32{0xe582d004, 0xe59f0004, 0xe5820000, 0xe12fff1e, heapBase} {
			binary.LittleEndian.PutUint32(module[index*4:], word)
		}
		runtime, err := New(Package{Module: module})
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		if err := runtime.Bootstrap(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := readWord(t, runtime, outputAddr+4); got != stackEntrySP {
			t.Fatalf("bootstrap SP = 0x%08x, want 0x%08x", got, stackEntrySP)
		}
	})

	t.Run("constructor", func(t *testing.T) {
		// str sp,[r3,#4]; ldr r0,[pc,#4]; str r0,[r3]; bx lr; appletObject
		module := make([]byte, 20)
		appletObject := heapBase + 0x40
		for index, word := range []uint32{0xe583d004, 0xe59f0004, 0xe5830000, 0xe12fff1e, appletObject} {
			binary.LittleEndian.PutUint32(module[index*4:], word)
		}
		runtime, err := New(Package{Module: module})
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		runtime.moduleObject = heapBase
		writeWords(t, runtime, heapBase, heapBase+0x20)
		writeWords(t, runtime, heapBase+0x20, 0, 0, moduleBase)
		if err := runtime.ProbeAppletBoundary(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := readWord(t, runtime, outputAddr+8); got != stackEntrySP {
			t.Fatalf("constructor SP = 0x%08x, want 0x%08x", got, stackEntrySP)
		}
	})

	t.Run("event", func(t *testing.T) {
		// ldr r0,[pc,#12]; str sp,[r0]; mov r0,#1; bx lr; nop; output
		module := make([]byte, 24)
		for index, word := range []uint32{0xe59f000c, 0xe580d000, 0xe3a00001, 0xe12fff1e, 0xe1a00000, outputAddr} {
			binary.LittleEndian.PutUint32(module[index*4:], word)
		}
		runtime, err := New(Package{Module: module})
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		runtime.appletObject = heapBase
		writeWords(t, runtime, heapBase, heapBase+0x20)
		writeWords(t, runtime, heapBase+0x20, 0, 0, moduleBase)
		if _, err := runtime.DispatchEvent(context.Background(), 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		if got := readWord(t, runtime, outputAddr); got != stackEntrySP {
			t.Fatalf("event SP = 0x%08x, want 0x%08x", got, stackEntrySP)
		}
	})

	t.Run("timer and cleanup callbacks", func(t *testing.T) {
		// str sp,[r0]; bx lr
		module := make([]byte, 8)
		binary.LittleEndian.PutUint32(module[0:], 0xe580d000)
		binary.LittleEndian.PutUint32(module[4:], 0xe12fff1e)
		runtime, err := New(Package{Module: module})
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		runtime.cleanupCallbacks = []brewCallback{{function: moduleBase, context: outputAddr}}
		runtime.timers = []brewCallback{{function: moduleBase, context: outputAddr + 4}}
		if err := runtime.RunCallbacks(context.Background(), 0); err != nil {
			t.Fatal(err)
		}
		for _, address := range []uint32{outputAddr, outputAddr + 4} {
			if got := readWord(t, runtime, address); got != stackEntrySP {
				t.Fatalf("callback SP at 0x%08x = 0x%08x, want 0x%08x", address, got, stackEntrySP)
			}
		}
	})
}

func TestRunAppletCodeAllowsManyBoundedHostCalls(t *testing.T) {
	// Preserve lr, load a 5000-call counter and the AEE_GetUpTimeMS trap, then
	// repeatedly BLX into the host before returning through the saved lr.
	module := make([]byte, 44)
	for offset, instruction := range []uint32{
		0xe1a0600e, 0xe59f4018, 0xe59f5018, 0xe12fff35, 0xe2544001,
		0x1afffffc, 0xe3a00000, 0xe1a0e006, 0xe12fff1e, 5_000,
		helperMethodTrapBase + helperGetUpTimeMSSlot*2 | 1,
	} {
		binary.LittleEndian.PutUint32(module[offset*4:], instruction)
	}
	runtime, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	for register, value := range map[uint32]uint32{
		cpu.RegisterSP: stackBase + stackSize - 16,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.runAppletCode(context.Background(), moduleBase, cpu.ModeARM, "host-call loop"); err != nil {
		t.Fatalf("run bounded host-call loop: %v", err)
	}
	if got, want := runtime.clock, 5_000*time.Millisecond; got != want {
		t.Fatalf("clock after uptime calls = %v, want %v", got, want)
	}
}

func TestDisplayUpdateRequiresChangedFramebufferAndCommitsDetachedSnapshot(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	black := make([]byte, framebufferBytes)
	if err := runtime.cpu.WriteMemory(framebufferBase, black); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterLR, returnTrap|1); err != nil {
		t.Fatal(err)
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(displayTrapBase + 7*2 + 2)
	if err != nil || !handled {
		t.Fatalf("IDisplay Update handled=%v err=%v", handled, err)
	}
	if count, valid := runtime.FrameStats(); count != 1 || valid {
		t.Fatalf("untouched frame stats count=%d valid=%v, want 1/false", count, valid)
	}

	changed := append([]byte(nil), black...)
	changed[0], changed[1] = 0xff, 0xff
	if err := runtime.cpu.WriteMemory(framebufferBase, changed); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := runtime.handleAppletMethodTrap(displayTrapBase + 7*2 + 2); err != nil {
		t.Fatal(err)
	}
	frame, presented, err := runtime.Framebuffer()
	if err != nil || !presented {
		t.Fatalf("committed framebuffer presented=%v err=%v", presented, err)
	}
	if got := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA); got != (color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
		t.Fatalf("committed guest write missing from frame: pixel=%#v", got)
	}
	if count, valid := runtime.FrameStats(); count != 2 || !valid {
		t.Fatalf("changed frame stats count=%d valid=%v, want 2/true", count, valid)
	}
	if err := runtime.cpu.WriteMemory(framebufferBase, black); err != nil {
		t.Fatal(err)
	}
	frame, _, err = runtime.Framebuffer()
	if err != nil {
		t.Fatal(err)
	}
	if got := color.RGBAModel.Convert(frame.At(0, 0)).(color.RGBA); got != (color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
		t.Fatalf("uncommitted guest write leaked into frame: pixel=%#v", got)
	}
}

func TestCompletedEventCommitsChangedPrimarySurfaceSnapshot(t *testing.T) {
	// ldr r0,[pc,#8]; mov r1,#1; strh r1,[r0]; bx lr; framebufferBase
	module := make([]byte, 20)
	for index, word := range []uint32{0xe59f0008, 0xe3a01001, 0xe1c010b0, 0xe12fff1e, framebufferBase} {
		binary.LittleEndian.PutUint32(module[index*4:], word)
	}
	runtime, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	runtime.appletObject = heapBase
	var encoded [12]byte
	binary.LittleEndian.PutUint32(encoded[0:], heapBase+0x20)
	if err := runtime.cpu.WriteMemory(heapBase, encoded[:4]); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(encoded[8:], moduleBase)
	if err := runtime.cpu.WriteMemory(heapBase+0x20, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.DispatchEvent(context.Background(), 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if count, valid := runtime.FrameStats(); count != 1 || !valid {
		t.Fatalf("implicit frame stats count=%d valid=%v, want 1/true", count, valid)
	}
	frame, presented, err := runtime.Framebuffer()
	if err != nil || !presented {
		t.Fatalf("implicit framebuffer presented=%v err=%v", presented, err)
	}
	committed := frame.RGBAAt(0, 0)
	if committed.B == 0 {
		t.Fatalf("implicit framebuffer first pixel = %#v, want changed blue channel", committed)
	}
	if _, err := runtime.DispatchEvent(context.Background(), 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if count, valid := runtime.FrameStats(); count != 1 || !valid {
		t.Fatalf("unchanged implicit frame stats count=%d valid=%v, want 1/true", count, valid)
	}

	if err := runtime.cpu.WriteMemory(framebufferBase, []byte{0, 0}); err != nil {
		t.Fatal(err)
	}
	frame, presented, err = runtime.Framebuffer()
	if err != nil || !presented || frame.RGBAAt(0, 0) != committed {
		t.Fatalf("detached implicit frame changed with live surface: pixel=%#v presented=%v err=%v", frame.RGBAAt(0, 0), presented, err)
	}
}

func TestCompletedCallbackCommitsChangedPrimarySurfaceSnapshot(t *testing.T) {
	// ldr r0,[pc,#8]; mov r1,#1; strh r1,[r0]; bx lr; framebufferBase
	module := make([]byte, 20)
	for index, word := range []uint32{0xe59f0008, 0xe3a01001, 0xe1c010b0, 0xe12fff1e, framebufferBase} {
		binary.LittleEndian.PutUint32(module[index*4:], word)
	}
	runtime, err := New(Package{Module: module})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtime.timers = []brewCallback{{function: moduleBase}}

	if err := runtime.RunCallbacks(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if count, valid := runtime.FrameStats(); count != 1 || !valid {
		t.Fatalf("implicit callback frame stats count=%d valid=%v, want 1/true", count, valid)
	}
}

func TestDeviceBitmapGetInfoReportsGuestFramebufferGeometry(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	interfaceAt := heapBase + 0x80
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: deviceBitmapObject,
		cpu.RegisterR1: 0x01001045,
		cpu.RegisterR2: interfaceAt,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := runtime.handleAppletMethodTrap(bitmapTrapBase + 2*2 + 2); err != nil {
		t.Fatal(err)
	}
	var interfaceValue [4]byte
	if err := runtime.cpu.ReadMemory(interfaceAt, interfaceValue[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(interfaceValue[:]); got != deviceBitmapObject {
		t.Fatalf("IDIB interface = 0x%08x, want device bitmap", got)
	}
	infoAt := heapBase + 0x100
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: deviceBitmapObject,
		cpu.RegisterR1: infoAt,
		cpu.RegisterR2: 12,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(bitmapTrapBase + 12*2 + 2)
	if err != nil || !handled {
		t.Fatalf("IBitmap GetInfo handled=%v err=%v", handled, err)
	}
	var info [12]byte
	if err := runtime.cpu.ReadMemory(infoAt, info[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(info[0:4]); got != framebufferWidth {
		t.Fatalf("bitmap width = %d, want %d", got, framebufferWidth)
	}
	if got := binary.LittleEndian.Uint32(info[4:8]); got != framebufferHeight {
		t.Fatalf("bitmap height = %d, want %d", got, framebufferHeight)
	}
	if got := binary.LittleEndian.Uint32(info[8:12]); got != 16 {
		t.Fatalf("bitmap depth = %d, want 16", got)
	}
}

func TestCreateCompatibleBitmapReturnsBoundedSoftwareBitmap(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	objectOut := heapBase + 0x100
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: deviceBitmapObject,
		cpu.RegisterR1: objectOut,
		cpu.RegisterR2: 7,
		cpu.RegisterR3: 5,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(bitmapTrapBase + 13*2 + 2)
	if err != nil || !handled {
		t.Fatalf("CreateCompatibleBitmap handled=%v err=%v", handled, err)
	}
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(objectOut, encoded[:]); err != nil {
		t.Fatal(err)
	}
	object := binary.LittleEndian.Uint32(encoded[:])
	if object == 0 {
		t.Fatal("CreateCompatibleBitmap returned null")
	}
	infoAt := heapBase + 0x110
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: object,
		cpu.RegisterR1: infoAt,
		cpu.RegisterR2: 12,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := runtime.handleAppletMethodTrap(bitmapTrapBase + 12*2 + 2); err != nil {
		t.Fatal(err)
	}
	var info [12]byte
	if err := runtime.cpu.ReadMemory(infoAt, info[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(info[0:4]); got != 7 {
		t.Fatalf("compatible bitmap width = %d, want 7", got)
	}
	if got := binary.LittleEndian.Uint32(info[4:8]); got != 5 {
		t.Fatalf("compatible bitmap height = %d, want 5", got)
	}
}

func TestRunCallbacksHonorsRequestedDelay(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.timers = []brewCallback{{function: returnTrap | 1, context: 7, remaining: 100 * time.Millisecond}}
	if err := runtime.RunCallbacks(context.Background(), 16*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(runtime.timers) != 1 || runtime.timers[0].remaining != 84*time.Millisecond {
		t.Fatalf("timers after 16ms = %#v, want one timer with 84ms remaining", runtime.timers)
	}
}

func TestCommonHelperContracts(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	call := func(slot uint32, r0, r1, r2 uint32) uint32 {
		t.Helper()
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0: r0, cpu.RegisterR1: r1, cpu.RegisterR2: r2,
			cpu.RegisterLR: returnTrap | 1,
		} {
			if err := runtime.cpu.WriteRegister(register, value); err != nil {
				t.Fatal(err)
			}
		}
		handled, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + slot*2 + 2)
		if err != nil || !handled {
			t.Fatalf("helper slot %d handled=%v err=%v", slot, handled, err)
		}
		value, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	source, destination := heapBase+0x100, heapBase+0x200
	if err := runtime.cpu.WriteMemory(source, []byte("ab\x00tail")); err != nil {
		t.Fatal(err)
	}
	if got := call(helperStrncpySlot, destination, source, 6); got != destination {
		t.Fatalf("strncpy return = 0x%08x, want destination", got)
	}
	copied := make([]byte, 6)
	if err := runtime.cpu.ReadMemory(destination, copied); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copied, []byte{'a', 'b', 0, 0, 0, 0}) {
		t.Fatalf("strncpy bytes = %v", copied)
	}
	if err := runtime.cpu.WriteMemory(destination, []byte("ac\x00")); err != nil {
		t.Fatal(err)
	}
	if got := int32(call(helperStrncmpSlot, source, destination, 3)); got != -1 {
		t.Fatalf("strncmp result = %d, want -1", got)
	}
	if err := runtime.cpu.WriteMemory(source, []byte("Prefix/Needle/Suffix\x00")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(destination, []byte("needle\x00")); err != nil {
		t.Fatal(err)
	}
	if got := int32(call(helperStricmpSlot, destination, heapBase+0x400, 0)); got == 0 {
		t.Fatal("stricmp treated different strings as equal")
	}
	if err := runtime.cpu.WriteMemory(heapBase+0x400, []byte("NEEDLE\x00")); err != nil {
		t.Fatal(err)
	}
	if got := int32(call(helperStricmpSlot, destination, heapBase+0x400, 0)); got != 0 {
		t.Fatalf("stricmp result = %d, want equal", got)
	}
	if err := runtime.cpu.WriteMemory(destination, []byte("Needle\x00")); err != nil {
		t.Fatal(err)
	}
	if got := call(helperStrstrSlot, source, destination, 0); got != source+7 {
		t.Fatalf("strstr result = 0x%08x, want 0x%08x", got, source+7)
	}
	if got := call(helperStrchrSlot, source, '/', 0); got != source+6 {
		t.Fatalf("strchr result = 0x%08x, want 0x%08x", got, source+6)
	}
	if got := call(helperStristrSlot, source, destination, 0); got != source+7 {
		t.Fatalf("stristr result = 0x%08x, want 0x%08x", got, source+7)
	}
	if err := runtime.cpu.WriteMemory(source, []byte{0xb0, 0xa1, 'A'}); err != nil {
		t.Fatal(err)
	}
	wideExpanded := heapBase + 0x680
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 8); err != nil {
		t.Fatal(err)
	}
	call(helperStrExpandSlot, source, 3, wideExpanded)
	expanded := make([]byte, 6)
	if err := runtime.cpu.ReadMemory(wideExpanded, expanded); err != nil {
		t.Fatal(err)
	}
	gotExpanded := []uint16{
		binary.LittleEndian.Uint16(expanded[0:2]),
		binary.LittleEndian.Uint16(expanded[2:4]),
		binary.LittleEndian.Uint16(expanded[4:6]),
	}
	wantExpanded := []uint16{'가', 'A', 0}
	for index := range wantExpanded {
		if gotExpanded[index] != wantExpanded[index] {
			t.Fatalf("strexpand unit %d = 0x%04x, want 0x%04x", index, gotExpanded[index], wantExpanded[index])
		}
	}
	if err := runtime.cpu.WriteMemory(destination, []byte("prefix-\x00")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(heapBase+0x480, []byte("suffix\x00")); err != nil {
		t.Fatal(err)
	}
	if got := call(helperStrcatSlot, destination, heapBase+0x480, 0); got != destination {
		t.Fatalf("strcat return = 0x%08x, want destination", got)
	}
	if got, err := runtime.readCString(destination); err != nil || got != "prefix-suffix" {
		t.Fatalf("strcat result = %q err=%v", got, err)
	}

	wideSource, wideDestination := heapBase+0x700, heapBase+0x740
	wide := []byte{'K', 0, 'T', 0, 'F', 0, 0, 0}
	if err := runtime.cpu.WriteMemory(wideSource, wide); err != nil {
		t.Fatal(err)
	}
	if got := call(helperWStrcpySlot, wideDestination, wideSource, 0); got != wideDestination {
		t.Fatalf("wstrcpy return = 0x%08x, want destination", got)
	}
	copiedWide := make([]byte, len(wide))
	if err := runtime.cpu.ReadMemory(wideDestination, copiedWide); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copiedWide, wide) {
		t.Fatalf("wstrcpy bytes = %v, want %v", copiedWide, wide)
	}
	if got := call(helperWStrlenSlot, wideDestination, 0, 0); got != 3 {
		t.Fatalf("wstrlen = %d, want 3", got)
	}
	if got := call(helperWStrcmpSlot, wideSource, wideDestination, 0); got != 0 {
		t.Fatalf("wstrcmp = %d, want equal", int32(got))
	}
	if got := call(helperWStrSizeSlot, wideDestination, 0, 0); got != 8 {
		t.Fatalf("wstrsize = %d, want 8", got)
	}
	if err := runtime.cpu.WriteMemory(source, []byte("path/to/file.txt\x00")); err != nil {
		t.Fatal(err)
	}
	if got := call(helperStrrchrSlot, source, '/', 0); got != source+7 {
		t.Fatalf("strrchr result = 0x%08x, want 0x%08x", got, source+7)
	}
	if err := runtime.cpu.WriteMemory(source, []byte{0xb0, 0xa1, 'A', 0}); err != nil {
		t.Fatal(err)
	}
	convertedWide := heapBase + 0x780
	if got := call(helperStrToWStrSlot, source, convertedWide, 8); got != convertedWide {
		t.Fatalf("strtowstr return = 0x%08x, want destination", got)
	}
	convertedBack := heapBase + 0x7c0
	if got := call(helperWStrToStrSlot, convertedWide, convertedBack, 8); got != convertedBack {
		t.Fatalf("wstrtostr return = 0x%08x, want destination", got)
	}
	back := make([]byte, 4)
	if err := runtime.cpu.ReadMemory(convertedBack, back); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(back, []byte{0xb0, 0xa1, 'A', 0}) {
		t.Fatalf("wide round trip = %v", back)
	}
	shortWide := heapBase + 0x800
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, ^uint32(0)); err != nil {
		t.Fatal(err)
	}
	if got := call(helperWStrNCopyNSlot, shortWide, 6, wideSource); got != 2 {
		t.Fatalf("wstrncopyn copied = %d, want 2", got)
	}
	shortData := make([]byte, 6)
	if err := runtime.cpu.ReadMemory(shortWide, shortData); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(shortData, []byte{'K', 0, 'T', 0, 0, 0}) {
		t.Fatalf("wstrncopyn bytes = %v", shortData)
	}
	if err := runtime.cpu.WriteMemory(heapBase+0x500, []byte(" \t-123tail\x00")); err != nil {
		t.Fatal(err)
	}
	if got := int32(call(helperAtoiSlot, heapBase+0x500, 0, 0)); got != -123 {
		t.Fatalf("atoi result = %d, want -123", got)
	}

	randomAt := heapBase + 0x300
	if got := call(helperGetRandSlot, randomAt, 4, 0); got != 0 {
		t.Fatalf("GetRand result = %d, want success", got)
	}
	random := make([]byte, 4)
	if err := runtime.cpu.ReadMemory(randomAt, random); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(random, make([]byte, 4)) {
		t.Fatal("GetRand left the destination unchanged")
	}

	if err := runtime.RunCallbacks(context.Background(), 2500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := call(helperGetUpTimeMSSlot, 0, 0, 0); got != 2500 {
		t.Fatalf("GetUpTimeMS = %d, want 2500", got)
	}
	if got := call(helperGetSecondsSlot, 0, 0, 0); got != 630_720_002 {
		t.Fatalf("GetSeconds = %d, want deterministic calendar time", got)
	}
	julianAt := heapBase + 0x600
	call(helperGetJulianDateSlot, 630_720_000, julianAt, 0)
	julian := make([]byte, 14)
	if err := runtime.cpu.ReadMemory(julianAt, julian); err != nil {
		t.Fatal(err)
	}
	wantJulian := []uint16{2000, 1, 1, 0, 0, 0, 6}
	for index, want := range wantJulian {
		if got := binary.LittleEndian.Uint16(julian[index*2:]); got != want {
			t.Fatalf("Julian field %d = %d, want %d", index, got, want)
		}
	}
}

func TestSprintfSupportsBoundedStringAndIntegerFormats(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	destination, formatAt, textAt := heapBase+0x100, heapBase+0x200, heapBase+0x300
	if err := runtime.cpu.WriteMemory(formatAt, []byte("%s-%03d-%x%%\x00")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(textAt, []byte("giftkart\x00")); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: destination,
		cpu.RegisterR1: formatAt,
		cpu.RegisterR2: textAt,
		cpu.RegisterR3: 7,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	var thirdArgument [4]byte
	binary.LittleEndian.PutUint32(thirdArgument[:], 0x2a)
	if err := runtime.cpu.WriteMemory(stackBase, thirdArgument[:]); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, stackBase); err != nil {
		t.Fatal(err)
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + helperSprintfSlot*2 + 2)
	if err != nil || !handled {
		t.Fatalf("sprintf handled=%v err=%v", handled, err)
	}
	text, err := runtime.readCString(destination)
	if err != nil {
		t.Fatal(err)
	}
	if text != "giftkart-007-2a%" {
		t.Fatalf("sprintf result = %q, want giftkart-007-2a%%", text)
	}
	if err := runtime.cpu.WriteMemory(formatAt, []byte("%c%c\x00")); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, destination); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, formatAt); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 'K'); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 'T'); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + helperSprintfSlot*2 + 2); err != nil {
		t.Fatal(err)
	}
	if text, err := runtime.readCString(destination); err != nil || text != "KT" {
		t.Fatalf("sprintf %%c result = %q err=%v, want KT", text, err)
	}
}

func TestLegacyIconViewControlMaintainsItemsAndSelection(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	call := func(slot uint32, r1, r2, r3 uint32) uint32 {
		t.Helper()
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0: menuCtlObject,
			cpu.RegisterR1: r1,
			cpu.RegisterR2: r2,
			cpu.RegisterR3: r3,
			cpu.RegisterLR: returnTrap | 1,
		} {
			if err := runtime.cpu.WriteRegister(register, value); err != nil {
				t.Fatal(err)
			}
		}
		handled, _, _, err := runtime.handleAppletMethodTrap(menuCtlTrapBase + slot*2 + 2)
		if err != nil || !handled {
			t.Fatalf("menu slot %d handled=%v err=%v", slot, handled, err)
		}
		value, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	var stackArgs [8]byte
	binary.LittleEndian.PutUint32(stackArgs[4:], 0x12345678)
	if err := runtime.cpu.WriteMemory(stackBase, stackArgs[:]); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, stackBase); err != nil {
		t.Fatal(err)
	}
	if got := call(12, 0, 0, 42); got != 1 {
		t.Fatalf("AddItem = %d, want true", got)
	}
	if got := call(26, 0, 0, 0); got != 1 {
		t.Fatalf("GetItemCount = %d, want 1", got)
	}
	if got := call(18, 0, 0, 0); got != 42 {
		t.Fatalf("GetSel = %d, want 42", got)
	}
	dataAt := heapBase + 0x100
	if got := call(14, 42, dataAt, 0); got != 1 {
		t.Fatalf("GetItemData = %d, want true", got)
	}
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(dataAt, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded[:]); got != 0x12345678 {
		t.Fatalf("menu item data = 0x%08x", got)
	}
}

func TestFileReadNullDestinationReturnsZeroWithoutAdvancing(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.currentFile = []byte("data")
	runtime.fileOffset = 1
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: 0,
		cpu.RegisterR2: 2,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.readGuestFile(); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 0 {
		t.Fatalf("null-buffer read result=%d err=%v, want 0", got, err)
	}
	if runtime.fileOffset != 1 {
		t.Fatalf("null-buffer read advanced offset to %d", runtime.fileOffset)
	}
}

func TestLegacySoundAndActiveAppletContracts(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.activeApplet = heapBase + 0x900
	runtime.activeClassID = 0x01020304
	if err := runtime.cpu.WriteRegister(cpu.RegisterLR, returnTrap|1); err != nil {
		t.Fatal(err)
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 8*2 + 2)
	if err != nil || !handled {
		t.Fatalf("ActiveApplet handled=%v err=%v", handled, err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != runtime.activeClassID {
		t.Fatalf("ActiveApplet = 0x%08x err=%v, want ClassID 0x%08x", got, err, runtime.activeClassID)
	}
	appInfo := heapBase + 0x980
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: runtime.activeClassID,
		cpu.RegisterR2: appInfo,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err = runtime.handleAppletMethodTrap(shellMethodTrapBase + 3*2 + 2)
	if err != nil || !handled {
		t.Fatalf("QueryClass handled=%v err=%v", handled, err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 1 {
		t.Fatalf("QueryClass = %d err=%v, want true", got, err)
	}
	appInfoData := make([]byte, 20)
	if err := runtime.cpu.ReadMemory(appInfo, appInfoData); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(appInfoData[:4]); got != runtime.activeClassID {
		t.Fatalf("AEEAppInfo.cls = 0x%08x, want 0x%08x", got, runtime.activeClassID)
	}
	if got := binary.LittleEndian.Uint16(appInfoData[18:20]); got != 0x0210 {
		t.Fatalf("AEEAppInfo.wFlags = 0x%04x, want GAME|RUNNING", got)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterLR, returnTrap|1); err != nil {
		t.Fatal(err)
	}
	handled, _, _, err = runtime.handleAppletMethodTrap(helperMethodTrapBase + helperCurrentAppletSlot*2 + 2)
	if err != nil || !handled {
		t.Fatalf("GetAppInstance handled=%v err=%v", handled, err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != runtime.activeApplet {
		t.Fatalf("GetAppInstance = 0x%08x err=%v, want IApplet 0x%08x", got, err, runtime.activeApplet)
	}
	if handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 28*2 + 2); err != nil || !handled {
		t.Fatalf("MessageBoxText handled=%v err=%v", handled, err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 1 {
		t.Fatalf("MessageBoxText = %d err=%v, want true", got, err)
	}

	out := heapBase + 0xa00
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: Sound10ClassID,
		cpu.RegisterR2: out,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.createShellInstance(); err != nil {
		t.Fatal(err)
	}
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(out, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded[:]); got != soundObject {
		t.Fatalf("Sound10 object = 0x%08x, want 0x%08x", got, soundObject)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 0); err != nil {
		t.Fatal(err)
	}
	if err := runtime.createShellInstance(); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 3 {
		t.Fatalf("unsupported CreateInstance status = %d err=%v, want AEE_ECLASSNOTSUPPORT", got, err)
	}
	if err := runtime.cpu.ReadMemory(out, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded[:]); got != 0 {
		t.Fatalf("unsupported CreateInstance object = 0x%08x, want null", got)
	}
}

func TestShellGetHandlerReturnsImplementedBMPViewer(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	name := heapBase + 0xa80
	if err := runtime.cpu.WriteMemory(name, []byte("image/bmp\x00")); err != nil {
		t.Fatal(err)
	}
	call := func(handlerType uint32, pointer uint32) uint32 {
		t.Helper()
		for register, value := range map[uint32]uint32{
			cpu.RegisterR1: handlerType,
			cpu.RegisterR2: pointer,
			cpu.RegisterLR: returnTrap | 1,
		} {
			if err := runtime.cpu.WriteRegister(register, value); err != nil {
				t.Fatal(err)
			}
		}
		handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 32*2 + 2)
		if err != nil || !handled {
			t.Fatalf("GetHandler handled=%v err=%v", handled, err)
		}
		value, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := call(0, name); got != WinBMPClassID {
		t.Fatalf("BMP viewer handler = 0x%08x, want WinBMP 0x%08x", got, WinBMPClassID)
	}
	if err := runtime.cpu.WriteMemory(name, []byte("IMAGE/BMP\x00")); err != nil {
		t.Fatal(err)
	}
	if got := call(0, name); got != WinBMPClassID {
		t.Fatalf("case-insensitive BMP viewer handler = 0x%08x", got)
	}
	if got := call(1, name); got != 0 {
		t.Fatalf("sound handler for BMP = 0x%08x, want unsupported", got)
	}
	if err := runtime.cpu.WriteMemory(name, []byte("image/png\x00")); err != nil {
		t.Fatal(err)
	}
	if got := call(0, name); got != 0 {
		t.Fatalf("unimplemented PNG viewer handler = 0x%08x, want unsupported", got)
	}
}

func TestShellCreatesLegacySoftKeyControl(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	out := heapBase + 0xb00
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: SoftKeyCtl10ClassID,
		cpu.RegisterR2: out,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.createShellInstance(); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 0 {
		t.Fatalf("SoftKeyCtl CreateInstance status=%d err=%v", got, err)
	}
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(out, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded[:]); got != menuCtlObject {
		t.Fatalf("SoftKeyCtl object=0x%08x, want menu control 0x%08x", got, menuCtlObject)
	}
}

func TestKTFServiceCreateAndSetupContracts(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	out := heapBase + 0xa40
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: KTFServiceClassID,
		cpu.RegisterR2: out,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.createShellInstance(); err != nil {
		t.Fatal(err)
	}
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(out, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded[:]); got != ktfServiceObject {
		t.Fatalf("KTF service object = 0x%08x, want 0x%08x", got, ktfServiceObject)
	}
	if err := runtime.cpu.ReadMemory(ktfServiceObject, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded[:]); got != ktfServiceVTable {
		t.Fatalf("KTF service vtable = 0x%08x, want 0x%08x", got, ktfServiceVTable)
	}
	for _, slot := range []uint32{2, 3} {
		if err := runtime.cpu.WriteRegister(cpu.RegisterR0, ktfServiceObject); err != nil {
			t.Fatal(err)
		}
		handled, _, _, err := runtime.handleAppletMethodTrap(ktfServiceTrapBase + slot*2 + 2)
		if err != nil || !handled {
			t.Fatalf("IKTFService slot %d handled=%v err=%v", slot, handled, err)
		}
		if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 0 {
			t.Fatalf("IKTFService slot %d status=%d err=%v", slot, got, err)
		}
	}
}

func TestSoundPlayerSetUsesInputDiscriminator(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	out := heapBase + 0xb00
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: SoundPlayerClassID,
		cpu.RegisterR2: out,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.createShellInstance(); err != nil {
		t.Fatal(err)
	}
	var encoded [4]byte
	if err := runtime.cpu.ReadMemory(out, encoded[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(encoded[:]); got != soundPlayerObject {
		t.Fatalf("SoundPlayer object = 0x%08x, want 0x%08x", got, soundPlayerObject)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: 2,
		cpu.RegisterR2: heapBase + 0xc00,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(soundPlayerTrapBase + 3*2 + 2)
	if err != nil || !handled {
		t.Fatalf("ISoundPlayer Set handled=%v err=%v", handled, err)
	}
}

func TestHeapMallocAndFreeContracts(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	if err := runtime.cpu.WriteRegister(cpu.RegisterLR, returnTrap|1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 17); err != nil {
		t.Fatal(err)
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(heapTrapBase + 2*2 + 2)
	if err != nil || !handled {
		t.Fatalf("IHeap Malloc handled=%v err=%v", handled, err)
	}
	address, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil || address != heapBase {
		t.Fatalf("IHeap Malloc = 0x%08x err=%v, want 0x%08x", address, err, heapBase)
	}
	if runtime.heapNext != heapBase+24 {
		t.Fatalf("aligned heap next = 0x%08x, want 0x%08x", runtime.heapNext, heapBase+24)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, address); err != nil {
		t.Fatal(err)
	}
	handled, _, _, err = runtime.handleAppletMethodTrap(heapTrapBase + 4*2 + 2)
	if err != nil || !handled {
		t.Fatalf("IHeap Free handled=%v err=%v", handled, err)
	}
	if runtime.heapNext != heapBase {
		t.Fatalf("heap next after free = 0x%08x, want 0x%08x", runtime.heapNext, heapBase)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 17); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := runtime.handleAppletMethodTrap(heapTrapBase + 2*2 + 2); err != nil {
		t.Fatal(err)
	}
	if reused, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || reused != address {
		t.Fatalf("reused allocation = 0x%08x err=%v, want 0x%08x", reused, err, address)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, address); err != nil {
		t.Fatal(err)
	}
	handled, _, _, err = runtime.handleAppletMethodTrap(shellMethodTrapBase + 20*2 + 2)
	if err != nil || !handled {
		t.Fatalf("IShell FreeResData handled=%v err=%v", handled, err)
	}
	if runtime.heapNext != heapBase {
		t.Fatalf("heap next after FreeResData = 0x%08x, want 0x%08x", runtime.heapNext, heapBase)
	}
}

func TestHelperMallocReturnsNullForZeroSize(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
		t.Fatal(err)
	}
	if err := runtime.returnAllocation(); err != nil {
		t.Fatal(err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 0 {
		t.Fatalf("malloc(0) = 0x%08x err=%v, want NULL", got, err)
	}
}

func TestReleaseReclaimsImageAndBitmapAllocations(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	object, err := runtime.createNativeBitmap(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.heapNext == heapBase {
		t.Fatal("native bitmap did not allocate guest memory")
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: object,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.runAppletCode(context.Background(), releaseTrap, cpu.ModeThumb, "release bitmap"); err != nil {
		t.Fatal(err)
	}
	if runtime.heapNext != heapBase {
		t.Fatalf("heap next after bitmap release = 0x%08x, want 0x%08x", runtime.heapNext, heapBase)
	}
}

func TestFreeReclaimsRuntimeOwnedBitmapPixels(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	object, err := runtime.createNativeBitmap(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.heapNext == heapBase {
		t.Fatal("native bitmap did not allocate guest memory")
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: object,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.runAppletCode(context.Background(), freeTrap, cpu.ModeThumb, "free bitmap"); err != nil {
		t.Fatal(err)
	}
	if runtime.heapNext != heapBase {
		t.Fatalf("heap next after bitmap free = 0x%08x, want 0x%08x", runtime.heapNext, heapBase)
	}
}

func TestTAPIStatusUsesStableSyntheticIdentity(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	destination := heapBase + 0x700
	sentinel := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := runtime.cpu.WriteMemory(destination+20, sentinel); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: destination,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(tapiTrapBase + 3*2 + 2)
	if err != nil || !handled {
		t.Fatalf("ITAPI GetStatus handled=%v err=%v", handled, err)
	}
	status := make([]byte, 20)
	if err := runtime.cpu.ReadMemory(destination, status); err != nil {
		t.Fatal(err)
	}
	if got := string(status[:16]); got != "000000000000000\x00" {
		t.Fatalf("mobile ID = %q", got)
	}
	if flags := binary.LittleEndian.Uint16(status[17:19]); flags != 1<<6 {
		t.Fatalf("TAPI flags = 0x%x, want registered", flags)
	}
	gotSentinel := make([]byte, len(sentinel))
	if err := runtime.cpu.ReadMemory(destination+20, gotSentinel); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotSentinel, sentinel) {
		t.Fatalf("TAPI status overwrote caller storage: got %x, want %x", gotSentinel, sentinel)
	}
}

func TestNet11ServiceStaysOfflineWithoutHostNetworkAccess(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	if err := runtime.cpu.WriteRegister(cpu.RegisterLR, returnTrap|1); err != nil {
		t.Fatal(err)
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(netTrapBase + 6*2 + 2)
	if err != nil || !handled {
		t.Fatalf("INetMgr NetStatus handled=%v err=%v", handled, err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 4 {
		t.Fatalf("INetMgr NetStatus = %d err=%v, want NET_PPP_CLOSED", got, err)
	}
}

func TestMissingBREWPreferencesLeaveCallerBufferUntouched(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	destination, stack := heapBase+0x900, heapBase+0x980
	want := []byte{0xaa, 0xbb, 0xcc, 0xdd}
	if err := runtime.cpu.WriteMemory(destination, want); err != nil {
		t.Fatal(err)
	}
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(want)))
	if err := runtime.cpu.WriteMemory(stack, size[:]); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR3: destination,
		cpu.RegisterSP: stack,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 23*2 + 2)
	if err != nil || !handled {
		t.Fatalf("IShell GetPrefs handled=%v err=%v", handled, err)
	}
	if got, err := runtime.cpu.ReadRegister(cpu.RegisterR0); err != nil || got != 1 {
		t.Fatalf("IShell GetPrefs = %d err=%v, want EFAILED", got, err)
	}
	got := make([]byte, len(want))
	if err := runtime.cpu.ReadMemory(destination, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("preference buffer changed: got %x want %x", got, want)
	}
}

func TestBREWPreferencesRoundTripWithinRuntime(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	runtime.preferences = make(map[brewPreferenceKey][]byte)
	buffer, stack := heapBase+0xa00, heapBase+0xb00
	want := []byte{1, 2, 3, 4, 5}
	if err := runtime.cpu.WriteMemory(buffer, want); err != nil {
		t.Fatal(err)
	}
	var size [4]byte
	binary.LittleEndian.PutUint32(size[:], uint32(len(want)))
	if err := runtime.cpu.WriteMemory(stack, size[:]); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: 0x01015391,
		cpu.RegisterR2: 0x65,
		cpu.RegisterR3: buffer,
		cpu.RegisterSP: stack,
		cpu.RegisterLR: returnTrap | 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 24*2 + 2); err != nil || !handled {
		t.Fatalf("SetPrefs handled=%v err=%v", handled, err)
	}
	if err := runtime.cpu.WriteMemory(buffer, make([]byte, len(want))); err != nil {
		t.Fatal(err)
	}
	if handled, _, _, err := runtime.handleAppletMethodTrap(shellMethodTrapBase + 23*2 + 2); err != nil || !handled {
		t.Fatalf("GetPrefs handled=%v err=%v", handled, err)
	}
	got := make([]byte, len(want))
	if err := runtime.cpu.ReadMemory(buffer, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("preferences = %v, want %v", got, want)
	}
}

func TestDecodeSplashUsesEmbeddedBMPContract(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 120, 61))
	for y := 0; y < source.Bounds().Dy(); y++ {
		for x := 0; x < source.Bounds().Dx(); x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x * 2), G: uint8(y * 4), B: 0x55, A: 0xff})
		}
	}
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	mif := make([]byte, mifSplashOffset+encoded.Len())
	copy(mif[mifSplashOffset:], encoded.Bytes())
	decoded, err := decodeSplash(mif)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds() != source.Bounds() {
		t.Fatalf("decoded bounds = %v, want %v", decoded.Bounds(), source.Bounds())
	}
}

func TestMatchRejectsEveryUnauthenticatedArchive(t *testing.T) {
	if _, matched, err := Match([]byte("PK\x03\x04synthetic")); err != nil || matched {
		t.Fatalf("unauthenticated archive matched=%v err=%v", matched, err)
	}
}

func TestMatchAcceptsGenericSingleModulePackage(t *testing.T) {
	module := make([]byte, 8)
	binary.LittleEndian.PutUint32(module[0:], 0xe92d400c)
	mif := make([]byte, 64)
	for offset, value := range map[int]uint32{0: 0x00010011, 4: 0x10001, 8: 32, 12: 8, 16: 40, 20: 1, 24: 48, 28: 16} {
		binary.LittleEndian.PutUint32(mif[offset:], value)
	}
	binary.LittleEndian.PutUint32(mif[40:], 48)
	binary.LittleEndian.PutUint32(mif[44:], 64)
	binary.LittleEndian.PutUint32(mif[48:], 0x01023456)
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	for name, data := range map[string][]byte{"game.mif": mif, "bin/game.mod": module, "bin/game.sig": []byte("unverified carrier signature")} {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	pkg, matched, err := Match(archive.Bytes())
	if err != nil || !matched {
		t.Fatalf("generic archive matched=%v err=%v", matched, err)
	}
	if len(pkg.ClassIDs) != 1 || pkg.ClassIDs[0] != 0x01023456 || pkg.Splash != nil {
		t.Fatalf("generic package = %+v", pkg)
	}
	if pkg.Authenticated {
		t.Fatal("generic package was reported as authenticated")
	}
}

func TestMatchAcceptsGenericSplitPackage(t *testing.T) {
	module := make([]byte, 8)
	binary.LittleEndian.PutUint32(module, 0xe92d400e)
	mif := make([]byte, 64)
	for offset, value := range map[int]uint32{0: 0x00010011, 4: 0x10001, 8: 32, 12: 8, 16: 40, 20: 1, 24: 48, 28: 16} {
		binary.LittleEndian.PutUint32(mif[offset:], value)
	}
	binary.LittleEndian.PutUint32(mif[40:], 48)
	binary.LittleEndian.PutUint32(mif[44:], 64)
	binary.LittleEndian.PutUint32(mif[48:], 0x01023456)

	writeArchive := func(files map[string][]byte) []byte {
		t.Helper()
		var archive bytes.Buffer
		writer := zip.NewWriter(&archive)
		for name, data := range files {
			entry, err := writer.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return archive.Bytes()
	}
	inner := writeArchive(map[string][]byte{
		"bin/game.mod": module,
		"bin/game.sig": []byte("unverified carrier signature"),
		"data.bar":     []byte("resource"),
	})
	outer := writeArchive(map[string][]byte{
		"game.mif":    mif,
		"payload.zip": inner,
	})
	pkg, matched, err := Match(outer)
	if err != nil || !matched {
		t.Fatalf("split archive matched=%v err=%v", matched, err)
	}
	if len(pkg.ClassIDs) != 1 || pkg.ClassIDs[0] != 0x01023456 || !bytes.Equal(pkg.Module, module) {
		t.Fatalf("split package = %+v", pkg)
	}
	if _, ok := pkg.Files["payload.zip"]; ok {
		t.Fatal("split package retained nested carrier ZIP")
	}
}

func TestOEMStrSizeHelperContract(t *testing.T) {
	runtime := newSyntheticRuntime(t)
	address := heapBase + 0xc00
	call := func(pointer uint32) uint32 {
		t.Helper()
		if err := runtime.cpu.WriteRegister(cpu.RegisterR0, pointer); err != nil {
			t.Fatal(err)
		}
		if err := runtime.cpu.WriteRegister(cpu.RegisterLR, returnTrap|1); err != nil {
			t.Fatal(err)
		}
		handled, _, _, err := runtime.handleAppletMethodTrap(helperMethodTrapBase + helperOEMStrSizeSlot*2 + 2)
		if err != nil || !handled {
			t.Fatalf("OEMSTRSIZE handled=%v err=%v", handled, err)
		}
		value, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := call(0); got != 0 {
		t.Fatalf("OEMSTRSIZE(NULL)=%d, want 0", got)
	}
	if err := runtime.cpu.WriteMemory(address, []byte{0}); err != nil {
		t.Fatal(err)
	}
	if got := call(address); got != 0 {
		t.Fatalf("OEMSTRSIZE(empty)=%d, want 0", got)
	}
	if err := runtime.cpu.WriteMemory(address, []byte("hello\x00")); err != nil {
		t.Fatal(err)
	}
	if got := call(address); got != 6 {
		t.Fatalf("OEMSTRSIZE(hello)=%d, want 6", got)
	}
}

func TestMIFApplicationClassIDAcceptsPrivateIDsAndRejectsServicesOrUnboundedRecords(t *testing.T) {
	metadata := loaderbrew.Metadata{IndexOffset: 32, IndexCount: 1, DataOffset: 40, DataSize: 8}
	data := make([]byte, 48)
	binary.LittleEndian.PutUint32(data[32:], 40)
	binary.LittleEndian.PutUint32(data[36:], 48)
	serviceClassIDs := []uint32{
		ShellClassID, DisplayClassID, HeapClassID, FileMgrClassID,
		OptionalDeviceClassID, KTFServiceClassID, SoundPlayerClassID,
		GraphicsClassID, Sound10ClassID, MemAStreamClassID, TAPIClassID,
		Net11ClassID, TextCtl10ClassID, IconViewCtl10ClassID,
		SoftKeyCtl10ClassID, MenuCtl10ClassID, DateCtl10ClassID,
		ClockCtl10ClassID, WinBMPClassID, BitmapClassID,
	}
	for _, classID := range serviceClassIDs {
		binary.LittleEndian.PutUint32(data[40:], classID)
		if _, ok := mifApplicationClassID(metadata, data); ok {
			t.Fatalf("service ClassID 0x%08x was accepted as an application ClassID", classID)
		}
	}
	for _, classID := range []uint32{0x26001000, 0x00456788} {
		binary.LittleEndian.PutUint32(data[40:], classID)
		if got, ok := mifApplicationClassID(metadata, data); !ok || got != classID {
			t.Fatalf("private ClassID 0x%08x parsed as 0x%08x ok=%v", classID, got, ok)
		}
	}
	binary.LittleEndian.PutUint32(data[40:], 0x01023456)
	binary.LittleEndian.PutUint32(data[36:], 52)
	if _, ok := mifApplicationClassID(metadata, data); ok {
		t.Fatal("out-of-bounds final MIF record was accepted")
	}
}

func TestLookupGuestFileUsesUniqueRelativeSuffix(t *testing.T) {
	runtime := &Runtime{files: map[string][]byte{"1234/data.bin": {1, 2, 3}}}
	data, name, ok := runtime.lookupGuestFile("Data.BIN")
	if !ok || name != "1234/data.bin" || !bytes.Equal(data, []byte{1, 2, 3}) {
		t.Fatalf("relative lookup data=%v name=%q ok=%v", data, name, ok)
	}
	runtime.files["other/data.bin"] = []byte{4}
	if _, _, ok := runtime.lookupGuestFile("data.bin"); ok {
		t.Fatal("ambiguous relative package path was accepted")
	}
}
