package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestKTFRuntimeMapsAndCallsClientEntry(t *testing.T) {
	client := syntheticKTFClient()
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin4096",
		BSSSize:    4096,
		Client:     client,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	result, value, err := runtime.bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if value != ktfImageBase+0x100 {
		t.Fatalf("bootstrap result = 0x%08x", value)
	}
	if result.Reason != cpu.StopBreakpoint ||
		result.PC != ktfReturnSentinel+2 {
		t.Fatalf("execution result = %+v", result)
	}
	if runtime.exe.Name != "SyntheticKTF" ||
		runtime.exe.InterfaceInit != (ktfImageBase+0x20)|1 ||
		runtime.exe.GetClass != (ktfImageBase+0x20)|1 {
		t.Fatalf("executable = %+v", runtime.exe)
	}
	var bss [4]byte
	if err := runtime.cpu.ReadMemory(ktfImageBase+uint32(len(client)), bss[:]); err != nil {
		t.Fatal(err)
	}
	if bss != [4]byte{} {
		t.Fatalf("BSS is not zero: %x", bss)
	}
}

func TestKTFRuntimeInitializesCompleteJavaEnvironment(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin4096",
		BSSSize:    4096,
		Client:     syntheticKTFClient(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.initialize(context.Background()); err != nil {
		t.Fatal(err)
	}

	contextAddress, err := runtime.readU32(runtime.javaEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	if contextAddress != runtime.exceptionContext {
		t.Fatalf(
			"Java environment context = 0x%08x, want 0x%08x",
			contextAddress,
			runtime.exceptionContext,
		)
	}
	if err := runtime.writeWords(
		contextAddress+0x24,
		[]uint32{2, 0x12345678},
	); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.readWords(contextAddress+0x24, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []uint32{2, 0x12345678}) {
		t.Fatalf("Java environment native fields = %08x", got)
	}
	frame, err := runtime.readU32(contextAddress + 8*4)
	if err != nil {
		t.Fatal(err)
	}
	if frame != 0 {
		t.Fatalf("initial Java exception frame = 0x%08x", frame)
	}
}

func TestKTFRuntimeLoadsPackagedDataBases(t *testing.T) {
	index := make([]byte, 13)
	copy(index, "qtpdb")
	binary.BigEndian.PutUint32(index[5:9], 3)
	binary.BigEndian.PutUint32(index[9:13], 2)
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
		Files: map[string][]byte{
			`wrapped\p\SAVE.idx`: index,
			"wrapped/p/SAVE.db":  {1, 2, 3, 4, 5, 6},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	store := runtime.databaseStores["save"]
	if store == nil {
		store = runtime.databaseStores["SAVE"]
	}
	if store == nil || store.recordSize != 3 ||
		len(store.records) != 2 ||
		!bytes.Equal(store.records[0], []byte{1, 2, 3}) ||
		!bytes.Equal(store.records[1], []byte{4, 5, 6}) {
		t.Fatalf("packaged database = %#v", store)
	}
}

func TestKTFRuntimeLoadsPackagedPrivateFiles(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		JARName:    "wrapped/01020304.jar",
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
		Files: map[string][]byte{
			`wrapped\P\config.do`: {1, 2, 3},
			"other/P/ignored.do":  {4, 5, 6},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if !bytes.Equal(runtime.fileData["/config.do"], []byte{1, 2, 3}) {
		t.Fatalf("private file = %v", runtime.fileData)
	}
	if _, ok := runtime.fileData["/ignored.do"]; ok {
		t.Fatalf("loaded file outside package root: %v", runtime.fileData)
	}
}

func TestKTFPrivateFilesSynthesizeOnlyMissingFunterPatchBundle(t *testing.T) {
	files := map[string][]byte{
		"P/FUNTER_DL.db": {1},
		"P/dragon.dat":   {2},
		"P/dragon.map":   {3},
		"P/dragon.res":   {4},
		"P/dragon.spr":   {5},
	}
	loaded := loadKTFPrivateFiles("01038088.jar", files)
	patch, ok := loaded["/dragon.pch"]
	if !ok {
		t.Fatalf("private files = %v", loaded)
	}
	if len(patch) != 24 || !bytes.Equal(patch, make([]byte, 24)) {
		t.Fatalf("synthetic Funter patch = %x", patch)
	}

	files["P/dragon.pch"] = []byte{9, 8, 7}
	loaded = loadKTFPrivateFiles("01038088.jar", files)
	if got := loaded["/dragon.pch"]; !bytes.Equal(got, []byte{9, 8, 7}) {
		t.Fatalf("packaged Funter patch = %x", got)
	}

	delete(files, "P/dragon.pch")
	delete(files, "P/FUNTER_DL.db")
	loaded = loadKTFPrivateFiles("01038088.jar", files)
	if _, ok := loaded["/dragon.pch"]; ok {
		t.Fatalf("unmarked bundle synthesized a patch: %v", loaded)
	}
}

func syntheticKTFClient() []byte {
	client := make([]byte, 0x200)
	copy(client, []byte{
		0x00, 0x48, // ldr r0, [pc, #0]
		0x70, 0x47, // bx lr
	})
	binary.LittleEndian.PutUint32(client[4:8], ktfImageBase+0x100)
	copy(client[0x20:], []byte{
		0x00, 0x20, // movs r0, #0
		0x70, 0x47, // bx lr
	})
	binary.LittleEndian.PutUint32(client[0x100:], ktfImageBase+0x140)
	binary.LittleEndian.PutUint32(client[0x104:], ktfImageBase+0x180)
	binary.LittleEndian.PutUint32(client[0x114:], (ktfImageBase+0x20)|1)
	binary.LittleEndian.PutUint32(client[0x140:], ktfImageBase+0x160)
	binary.LittleEndian.PutUint32(client[0x168:], (ktfImageBase+0x20)|1)
	binary.LittleEndian.PutUint32(client[0x170:], (ktfImageBase+0x20)|1)
	copy(client[0x180:], "SyntheticKTF\x00")
	return client
}

func TestKTFTaskSliceRunsToReturnSentinel(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client: []byte{
			0x07, 0x20, // movs r0, #7
			0x70, 0x47, // bx lr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.newTask(ktfImageBase|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	runtime.tasks = append(runtime.tasks, task)
	result := runtime.runTaskSlice(context.Background(), 16)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != cpu.StopExited || !task.done {
		t.Fatalf("KTF task result = %+v, done=%t", result, task.done)
	}
}

func TestKTFMachineRemainsPausedWhileDockedCardCanReceiveEvents(t *testing.T) {
	for _, test := range []struct {
		name      string
		dockCard  bool
		wantState machinecore.State
	}{
		{
			name:      "event target",
			dockCard:  true,
			wantState: machinecore.StatePaused,
		},
		{
			name:      "no event target",
			wantState: machinecore.StateStopped,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
				ClientName: "client.bin0",
				Client:     []byte{0x70, 0x47},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.cpu.Close()
			if test.dockCard {
				runtime.defaultDisplay = 1
				runtime.displayCards[1] = 2
			}
			machine := &Machine{
				cpu:        runtime.cpu,
				state:      machinecore.StatePaused,
				runBudget:  DefaultRunBudget,
				ktf:        runtime,
				ktfStarted: true,
			}
			if err := machine.runKTFSlice(
				context.Background(),
				16,
			); err != nil {
				t.Fatal(err)
			}
			if machine.State() != test.wantState ||
				machine.LastResult().Reason != cpu.StopExited {
				t.Fatalf(
					"quiescent KTF state = %s, result=%+v",
					machine.State(),
					machine.LastResult(),
				)
			}
		})
	}
}

func TestKTFTaskSliceScopesPendingJavaMethodPerTask(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	var observed []string
	newProbe := func(name string) uint32 {
		return runtime.registerHostCall(
			"test.task_java_method."+name,
			func(_ context.Context, runtime *ktfRuntime) (uint32, error) {
				observed = append(observed, runtime.lastJavaMethod)
				return 0, nil
			},
		)
	}
	first, err := runtime.newTask(newProbe("first"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.newTask(newProbe("second"), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	first.lastJavaMethod = "example/First.pending()V"
	second.lastJavaMethod = "example/Second.pending()V"
	runtime.tasks = []*ktfTask{first, second}
	runtime.lastJavaMethod = "ambient"

	for !first.done || !second.done {
		result := runtime.runTaskSlice(context.Background(), 16)
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	if want := []string{
		"example/First.pending()V",
		"example/Second.pending()V",
	}; !slices.Equal(observed, want) {
		t.Fatalf("pending Java methods = %q, want %q", observed, want)
	}
	if runtime.lastJavaMethod != "ambient" {
		t.Fatalf("ambient Java method = %q", runtime.lastJavaMethod)
	}
}

func TestKTFJavaCallsWaitForReusableTaskStack(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	runnable, err := runtime.newHostJavaObject("java/lang/Thread")
	if err != nil {
		t.Fatal(err)
	}
	runtime.tasks = make([]*ktfTask, ktfMaxTasks)
	for index := range runtime.tasks {
		runtime.tasks[index] = &ktfTask{}
	}

	if err := runtime.queueJavaVirtual(runnable, "run", "()V"); err != nil {
		t.Fatal(err)
	}
	if len(runtime.pendingJavaCalls) != 1 {
		t.Fatalf("pending Java calls = %d, want 1", len(runtime.pendingJavaCalls))
	}
	runtime.tasks[3].done = true
	if err := runtime.activatePendingJavaCalls(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.pendingJavaCalls) != 0 ||
		runtime.tasks[3].done ||
		len(runtime.tasks[3].context) == 0 {
		t.Fatalf(
			"activated Java call: pending=%d task=%+v",
			len(runtime.pendingJavaCalls),
			runtime.tasks[3],
		)
	}
}

func TestKTFMachineQueuesDueInputToDockedCard(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	card, err := runtime.newHostJavaObject("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	const display = uint32(0x10004000)
	runtime.defaultDisplay = display
	runtime.displayCards[display] = card
	runtime.tickMS = 32
	machine := &Machine{
		ktf: runtime,
		input: []machinecore.InputEvent{
			{Control: "left", Pressed: true, At: 48 * time.Millisecond},
			{Control: "ok", Pressed: true, At: 32 * time.Millisecond},
		},
	}

	if err := machine.queueKTFInput(runtime); err != nil {
		t.Fatal(err)
	}
	if len(machine.input) != 1 || machine.input[0].Control != "left" {
		t.Fatalf("remaining input = %#v", machine.input)
	}
	if len(runtime.tasks) != 1 {
		t.Fatalf("KTF tasks = %d, want 1", len(runtime.tasks))
	}
	if err := runtime.cpu.RestoreContext(runtime.tasks[0].context); err != nil {
		t.Fatal(err)
	}
	for register, want := range map[uint32]uint32{
		cpu.RegisterR1: card,
		cpu.RegisterR2: ktfKeyPressed,
		cpu.RegisterR3: 0xfffffffb,
	} {
		got, readErr := runtime.cpu.ReadRegister(register)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if got != want {
			t.Fatalf("input register r%d = 0x%08x, want 0x%08x", register, got, want)
		}
	}
	if trace := runtime.hostTrace[len(runtime.hostTrace)-1]; !strings.Contains(
		trace,
		"java_key_event:type=1:key=-5",
	) {
		t.Fatalf("input trace = %q", trace)
	}
}

func TestKTFMachineKeepsInputUntilCardExists(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.tickMS = 100
	machine := &Machine{
		ktf: runtime,
		input: []machinecore.InputEvent{
			{Control: "down", At: 10 * time.Millisecond},
		},
	}

	if err := machine.queueKTFInput(runtime); err != nil {
		t.Fatal(err)
	}
	if len(machine.input) != 1 || len(runtime.tasks) != 0 {
		t.Fatalf("input=%#v tasks=%d", machine.input, len(runtime.tasks))
	}
}

func TestKTFDisplayKeyMappingsAreSymmetric(t *testing.T) {
	tests := []struct {
		key    int32
		action int32
		name   string
	}{
		{key: -1, action: 1, name: "UP"},
		{key: -2, action: 6, name: "DOWN"},
		{key: -3, action: 2, name: "LEFT"},
		{key: -4, action: 5, name: "RIGHT"},
		{key: -5, action: 8, name: "FIRE"},
		{key: -6, action: 90, name: "SOFT1"},
		{key: -7, action: 91, name: "SOFT2"},
		{key: -8, action: 92, name: "SOFT3"},
		{key: -13, action: 96, name: "SIDE_UP"},
		{key: -14, action: 97, name: "SIDE_DOWN"},
		{key: -15, action: 98, name: "SIDE_SEL"},
		{key: -16, action: 99, name: "CLEAR"},
	}
	for _, test := range tests {
		if got := int32(ktfGameAction(test.key)); got != test.action {
			t.Errorf("getGameAction(%d) = %d, want %d", test.key, got, test.action)
		}
		if got := ktfGameKeyCode(test.action); got != test.key {
			t.Errorf("getKeyCode(%d) = %d, want %d", test.action, got, test.key)
		}
		if got := ktfKeyName(test.key); got != test.name {
			t.Errorf("getKeyName(%d) = %q, want %q", test.key, got, test.name)
		}
	}
	for key := int32('0'); key <= '9'; key++ {
		if got := ktfKeyName(key); got != string(rune(key)) {
			t.Errorf("getKeyName(%d) = %q", key, got)
		}
	}
	for _, signature := range []string{
		"org/kwis/msp/lcdui/Display.getKeyCode(I)I",
		"org/kwis/msp/lcdui/Display.getKeyName(I)Ljava/lang/String;",
	} {
		if _, ok := ktfJavaNativeOverride(signature); !ok {
			t.Errorf("native override missing for %s", signature)
		}
	}
}

func TestKTFNestedCallPropagatesPendingJavaMethod(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	var observed string
	procedure := runtime.registerHostCall(
		"test.nested_java_method",
		func(_ context.Context, runtime *ktfRuntime) (uint32, error) {
			observed = runtime.lastJavaMethod
			runtime.lastJavaMethod = "example/Inner.pending()V"
			return 0, nil
		},
	)
	runtime.lastJavaMethod = "example/Outer.pending()V"
	result, _, err := runtime.call(
		context.Background(),
		procedure,
		nil,
		16,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reason != cpu.StopBreakpoint ||
		result.PC != ktfReturnSentinel+2 {
		t.Fatalf("nested call result = %+v", result)
	}
	if observed != "example/Outer.pending()V" {
		t.Fatalf("nested call Java method = %q", observed)
	}
	if runtime.lastJavaMethod != "example/Inner.pending()V" {
		t.Fatalf("propagated Java method = %q", runtime.lastJavaMethod)
	}
}

func TestKTFGraphicsFillRectUpdatesFramebuffer(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 16, 16))
	graphics, err := runtime.ensureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	graphicsState := runtime.graphics[graphics]
	graphicsState.translate = image.Pt(99, -17)
	graphicsState.clip = image.Rect(2, 3, 4, 5)
	graphicsState.color = color.RGBA{R: 1, G: 2, B: 3, A: 4}
	runtime.resetScreenGraphics(graphics)
	if graphicsState.translate != (image.Point{}) ||
		graphicsState.clip != runtime.frame.Bounds() ||
		graphicsState.color != (color.RGBA{A: 0xff}) {
		t.Fatalf("reset screen graphics = %#v", graphicsState)
	}
	stack := DefaultStackBase + 0x100
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{4, 3}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: graphics,
		cpu.RegisterR2: 2,
		cpu.RegisterR3: 1,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleGraphicsMethod("setColor", "(I)V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 0x3366cc); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleGraphicsMethod("setColor", "(I)V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleGraphicsMethod("fillRect", "(IIII)V"); err != nil {
		t.Fatal(err)
	}
	got := runtime.frame.RGBAAt(3, 2)
	if got.R != 0x33 || got.G != 0x66 || got.B != 0xcc || got.A != 0xff {
		t.Fatalf("filled pixel = %#v", got)
	}
	pixels, err := runtime.newJavaByteArray(make([]byte, 4))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(
		stack,
		[]uint32{1, 1, pixels, 2, 1},
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 3); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleGraphicsMethod(
		"getPixels",
		"(IIII[BII)V",
	); err != nil {
		t.Fatal(err)
	}
	data, err := runtime.readJavaByteArray(pixels)
	if err != nil {
		t.Fatal(err)
	}
	const wantLuma = byte((0x33*77 + 0x66*150 + 0xcc*29) >> 8)
	if data[2] != wantLuma {
		t.Fatalf("copied grayscale pixel = 0x%02x, want 0x%02x", data[2], wantLuma)
	}
	text, err := runtime.newJavaString("A")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 0xffffff); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleGraphicsMethod("setColor", "(I)V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, text); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 8); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{6, 4 | 16}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleGraphicsMethod(
		"drawString",
		"(Ljava/lang/String;III)V",
	); err != nil {
		t.Fatal(err)
	}
	if got := runtime.frame.RGBAAt(9, 8); got != (color.RGBA{
		R: 0xff,
		G: 0xff,
		B: 0xff,
		A: 0xff,
	}) {
		t.Fatalf("text pixel = %#v", got)
	}
}

func TestKTFStringBufferDeleteClampsEndToLength(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	const instance = uint32(0x1234)
	runtime.stringBuffers[instance] = "abcdefg"
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: instance,
		cpu.RegisterR2: 0,
		cpu.RegisterR3: 400,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	result, err := runtime.handleStringBufferMethod(
		"delete",
		"(II)Ljava/lang/StringBuffer;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if result != instance {
		t.Fatalf("StringBuffer.delete result = 0x%08x", result)
	}
	if got := runtime.stringBuffers[instance]; got != "" {
		t.Fatalf("StringBuffer.delete value = %q", got)
	}
}

func TestKTFHandsetSystemPropertyProvidesCompatiblePhoneModel(t *testing.T) {
	runtime := &ktfRuntime{}
	if got := runtime.handsetSystemProperty(" phonemodel "); got != "LG-KH1300" {
		t.Fatalf("PHONEMODEL = %q, want LG-KH1300", got)
	}
	if got := runtime.handsetSystemProperty("UNKNOWN"); got != "" {
		t.Fatalf("unknown handset property = %q, want empty string", got)
	}
}

func TestKTFJavaArrayNewCreatesPrimitiveArray(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, 'I'); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 3); err != nil {
		t.Fatal(err)
	}
	instance, err := ktfJavaArrayNew(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	instanceWords, err := runtime.readWords(instance, 2)
	if err != nil {
		t.Fatal(err)
	}
	if instanceWords[0] == 0 || instanceWords[1] == 0 {
		t.Fatalf("array instance = %08x", instanceWords)
	}
	class, err := runtime.inspectJavaClass(instanceWords[1])
	if err != nil {
		t.Fatal(err)
	}
	if class.Name != "[I" {
		t.Fatalf("array class = %q", class.Name)
	}
	fields, err := runtime.readWords(instanceWords[0], 5)
	if err != nil {
		t.Fatal(err)
	}
	if fields[1] != 3 || fields[2] != 0 || fields[3] != 0 || fields[4] != 0 {
		t.Fatalf("array fields = %08x", fields)
	}
}

func TestKTFJavaCheckTypeFollowsClassHierarchyAndArrayRule(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	objectClass, err := runtime.ensureJavaClass("java/lang/Object")
	if err != nil {
		t.Fatal(err)
	}
	cardClass, err := runtime.ensureJavaClass("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	stringClass, err := runtime.ensureJavaClass("java/lang/String")
	if err != nil {
		t.Fatal(err)
	}
	card, err := runtime.newJavaInstance("org/kwis/msp/lcdui/Card", 0)
	if err != nil {
		t.Fatal(err)
	}
	array, err := runtime.newJavaArray("[I", 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	check := func(target, instance, unknown uint32) uint32 {
		t.Helper()
		for register, value := range map[uint32]uint32{
			cpu.RegisterR0: target,
			cpu.RegisterR1: instance,
			cpu.RegisterR2: unknown,
		} {
			if err := runtime.cpu.WriteRegister(register, value); err != nil {
				t.Fatal(err)
			}
		}
		value, err := ktfJavaCheckType(context.Background(), runtime)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := check(cardClass, card, 0); got != 1 {
		t.Fatalf("Card instanceof Card = %d", got)
	}
	if got := check(objectClass, card, 0); got != 1 {
		t.Fatalf("Card instanceof Object = %d", got)
	}
	if got := check(stringClass, card, 0); got != 0 {
		t.Fatalf("Card instanceof String = %d", got)
	}
	if got := check(objectClass, 0, 0); got != 0 {
		t.Fatalf("null instanceof Object = %d", got)
	}
	if got := check(stringClass, array, 0); got != 1 {
		t.Fatalf("int[] KTF compatibility check = %d", got)
	}
	if got := check(stringClass, card, 1); got != 1 {
		t.Fatalf("unknown-form KTF compatibility check = %d", got)
	}
}

func TestKTFHostCardVTableMatchesDeclaredMethodOrder(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	address, err := runtime.ensureJavaClass("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.inspectJavaClass(address)
	if err != nil {
		t.Fatal(err)
	}
	if class.FieldSize != 24 {
		t.Fatalf("Card field size = %d", class.FieldSize)
	}
	constructor, ok := findKTFJavaMethod(class, "<init>", "()V")
	if !ok || constructor.AccessFlags&0x0100 != 0 ||
		constructor.Body == 0 || constructor.NativeBody != 0 {
		t.Fatalf("Card constructor layout = %+v", constructor)
	}
	width, ok := findKTFJavaMethod(class, "getWidth", "()I")
	if !ok || width.VTableIndex != 14 {
		t.Fatalf("Card.getWidth method = %+v, found=%v", width, ok)
	}
	height, ok := findKTFJavaMethod(class, "getHeight", "()I")
	if !ok || height.VTableIndex != 15 {
		t.Fatalf("Card.getHeight method = %+v, found=%v", height, ok)
	}
}

func TestKTFFindDeclaredJavaMethodDoesNotInheritClassInitializer(t *testing.T) {
	const (
		parentAddress = uint32(0x1000)
		childAddress  = uint32(0x2000)
	)
	inherited := ktfJavaMethod{
		DeclaringClass: parentAddress,
		Name:           "<clinit>",
		Descriptor:     "()V",
		Body:           0x3001,
	}
	child := ktfJavaClass{
		Address: childAddress,
		Methods: []ktfJavaMethod{inherited},
	}
	if method, ok := findKTFJavaMethod(child, "<clinit>", "()V"); !ok ||
		method.Body != inherited.Body {
		t.Fatalf("inherited lookup = %+v, found=%t", method, ok)
	}
	if method, ok := findKTFDeclaredJavaMethod(
		child,
		"<clinit>",
		"()V",
	); ok {
		t.Fatalf("declared lookup inherited %+v", method)
	}

	declared := inherited
	declared.DeclaringClass = childAddress
	declared.Body = 0x4001
	child.Methods = append(child.Methods, declared)
	method, ok := findKTFDeclaredJavaMethod(child, "<clinit>", "()V")
	if !ok || method.Body != declared.Body {
		t.Fatalf("declared lookup = %+v, found=%t", method, ok)
	}
}

func TestKTFDynamicHostMethodsPreserveOccupiedCompatibilitySlots(
	t *testing.T,
) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	labelAddress, err := runtime.ensureJavaClass(
		"org/kwis/msp/lwc/LabelComponent",
	)
	if err != nil {
		t.Fatal(err)
	}
	labelClass, err := runtime.inspectJavaClass(labelAddress)
	if err != nil {
		t.Fatal(err)
	}
	label, err := runtime.newJavaInstanceForClass(labelClass)
	if err != nil {
		t.Fatal(err)
	}
	labelConstructor, ok := findKTFJavaMethod(
		labelClass,
		"<init>",
		"(Ljava/lang/String;)V",
	)
	if !ok {
		t.Fatal("LabelComponent constructor is missing")
	}
	labelWords, err := runtime.readWords(labelAddress, 5)
	if err != nil {
		t.Fatal(err)
	}
	const collisionCapacity = uint32(512)
	labelVTable := make([]uint32, collisionCapacity)
	labelCopyCount := labelWords[4] & 0xffff
	currentLabel, err := runtime.readWords(
		labelWords[3],
		int(labelCopyCount),
	)
	if err != nil {
		t.Fatal(err)
	}
	copy(labelVTable, currentLabel)
	labelVTable[ktfHostVirtualSlotBase] = labelConstructor.Address
	labelReplacement, err := runtime.allocateWords(collisionCapacity)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(labelReplacement, labelVTable); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(labelAddress+12, labelReplacement); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ensureJavaVTableIndex(
		labelAddress,
		labelReplacement,
	); err != nil {
		t.Fatal(err)
	}
	runtime.javaVTableCapacity[labelAddress] = collisionCapacity
	stringAddress, err := runtime.ensureJavaClass("java/lang/String")
	if err != nil {
		t.Fatal(err)
	}
	stringClass, err := runtime.inspectJavaClass(stringAddress)
	if err != nil {
		t.Fatal(err)
	}
	stringLength, ok := findKTFJavaMethod(
		stringClass,
		"length",
		"()I",
	)
	if !ok {
		t.Fatal("String.length()I is missing")
	}
	stringWords, err := runtime.readWords(stringAddress, 5)
	if err != nil {
		t.Fatal(err)
	}
	stringVTable := make([]uint32, collisionCapacity)
	copyCount := stringWords[4] & 0xffff
	current, err := runtime.readWords(stringWords[3], int(copyCount))
	if err != nil {
		t.Fatal(err)
	}
	copy(stringVTable, current)
	stringVTable[ktfHostVirtualSlotBase] = stringLength.Address
	replacement, err := runtime.allocateWords(collisionCapacity)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(replacement, stringVTable); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(stringAddress+12, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ensureJavaVTableIndex(stringAddress, replacement); err != nil {
		t.Fatal(err)
	}
	runtime.javaVTableCapacity[stringAddress] = collisionCapacity
	componentAddress, err := runtime.ensureJavaClass(
		"org/kwis/msp/lwc/Component",
	)
	if err != nil {
		t.Fatal(err)
	}
	backgroundAddress, err := runtime.resolveJavaMethod(
		componentAddress,
		"setBackground",
		"(I)V",
	)
	if err != nil {
		t.Fatal(err)
	}
	foregroundAddress, err := runtime.resolveJavaMethod(
		componentAddress,
		"setForeground",
		"(I)V",
	)
	if err != nil {
		t.Fatal(err)
	}
	background, err := runtime.inspectJavaMethod(backgroundAddress)
	if err != nil {
		t.Fatal(err)
	}
	foreground, err := runtime.inspectJavaMethod(foregroundAddress)
	if err != nil {
		t.Fatal(err)
	}
	if background.VTableIndex < ktfHostVirtualSlotBase ||
		foreground.VTableIndex != background.VTableIndex+1 {
		t.Fatalf(
			"compatibility slots: background=%d foreground=%d",
			background.VTableIndex,
			foreground.VTableIndex,
		)
	}
	virtualMethod := func(slot uint16) uint32 {
		t.Helper()
		fields, err := runtime.readU32(label)
		if err != nil {
			t.Fatal(err)
		}
		header, err := runtime.readU32(fields)
		if err != nil {
			t.Fatal(err)
		}
		vtable, err := runtime.readU32(runtime.jvmContext + 12 + (header >> 5))
		if err != nil {
			t.Fatal(err)
		}
		method, err := runtime.readU32(vtable + uint32(slot)*4)
		if err != nil {
			t.Fatal(err)
		}
		return method
	}
	if got := virtualMethod(background.VTableIndex); got != backgroundAddress {
		t.Fatalf(
			"LabelComponent setBackground target = 0x%08x, want 0x%08x",
			got,
			backgroundAddress,
		)
	}
	if got := virtualMethod(foreground.VTableIndex); got != foregroundAddress {
		t.Fatalf(
			"LabelComponent setForeground target = 0x%08x, want 0x%08x",
			got,
			foregroundAddress,
		)
	}
	gotStringSlot, err := runtime.readU32(
		replacement + uint32(background.VTableIndex)*4,
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotStringSlot != stringLength.Address {
		t.Fatalf(
			"occupied String slot = 0x%08x, want preserved 0x%08x",
			gotStringSlot,
			stringLength.Address,
		)
	}
	labelClass, err = runtime.inspectJavaClass(labelAddress)
	if err != nil {
		t.Fatal(err)
	}
	constructor, ok := findKTFJavaMethod(
		labelClass,
		"<init>",
		"(Ljava/lang/String;)V",
	)
	if !ok || constructor.VTableIndex != 12 {
		t.Fatalf("LabelComponent constructor = %+v, found=%v", constructor, ok)
	}
	if got := virtualMethod(constructor.VTableIndex); got != constructor.Address {
		t.Fatalf(
			"LabelComponent constructor target = 0x%08x, want 0x%08x",
			got,
			constructor.Address,
		)
	}
}

func TestKTFLWCFoundationMethodsTrackState(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	const (
		component = uint32(0x10001000)
		listener  = uint32(0x10002000)
		eventData = uint32(0x10003000)
		shell     = uint32(0x10004000)
	)
	for register, value := range []uint32{0, component, listener, eventData} {
		if err := runtime.cpu.WriteRegister(uint32(register), value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ktfHostJavaMethod(
		"org/kwis/msp/lwc/Component",
		"setEventListener",
		"(Lorg/kwis/msp/lwc/EventListener;Ljava/lang/Object;)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.listeners[component] != listener ||
		runtime.lwcEventData[component] != eventData {
		t.Fatalf(
			"component listener = (0x%08x, 0x%08x), want "+
				"(0x%08x, 0x%08x)",
			runtime.listeners[component],
			runtime.lwcEventData[component],
			listener,
			eventData,
		)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, component); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 24); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfHostJavaMethod(
		"org/kwis/msp/lwc/TextComponent",
		"setMaxLength",
		"(I)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.lwcMaxLengths[component] != 24 {
		t.Fatalf(
			"component max length = %d, want 24",
			runtime.lwcMaxLengths[component],
		)
	}
	if _, err := ktfHostJavaMethod(
		"org/kwis/msp/lwc/TextBoxComponent",
		"keyNotify",
		"(II)Z",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	textBoxKey :=
		"org/kwis/msp/lwc/TextBoxComponent.keyNotify(II)Z"
	if runtime.unimplementedJava[textBoxKey] != 0 {
		t.Fatalf(
			"TextBoxComponent.keyNotify was left unimplemented: %v",
			runtime.unimplementedJava,
		)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, shell); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, component); err != nil {
		t.Fatal(err)
	}
	index, err := ktfHostJavaMethod(
		"org/kwis/msp/lwc/ShellComponent",
		"addComponent",
		"(Lorg/kwis/msp/lwc/Component;)I",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if index != 0 || len(runtime.lwcChildren[shell]) != 1 ||
		runtime.lwcChildren[shell][0] != component {
		t.Fatalf(
			"shell children = %#v at index %d",
			runtime.lwcChildren[shell],
			index,
		)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, listener); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, component); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfHostJavaMethod(
		"com/ktf/kfc/GTextListener",
		"setIMEModes",
		"([I)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if len(runtime.unimplementedJava) != 0 {
		t.Fatalf(
			"LWC calls recorded as unimplemented: %#v",
			runtime.unimplementedJava,
		)
	}
}

func TestKTFLWCHierarchyAndAnnunciatorGeometry(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	annunciatorAddress, err := runtime.ensureJavaClass(
		"org/kwis/msp/lwc/AnnunciatorComponent",
	)
	if err != nil {
		t.Fatal(err)
	}
	wantHierarchy := []string{
		"org/kwis/msp/lwc/AnnunciatorComponent",
		"org/kwis/msp/lwc/ShellComponent",
		"org/kwis/msp/lwc/ContainerComponent",
		"org/kwis/msp/lwc/Component",
		"java/lang/Object",
	}
	address := annunciatorAddress
	for index, want := range wantHierarchy {
		class, inspectErr := runtime.inspectJavaClass(address)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if class.Name != want {
			t.Fatalf("LWC hierarchy[%d] = %q, want %q", index, class.Name, want)
		}
		address = class.Parent
	}
	annunciatorClass, err := runtime.inspectJavaClass(annunciatorAddress)
	if err != nil {
		t.Fatal(err)
	}
	annunciator, err := runtime.newJavaInstanceForClass(annunciatorClass)
	if err != nil {
		t.Fatal(err)
	}
	registers := make([]uint32, cpu.RegisterR12+1)
	registers[1] = annunciator
	if _, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/AnnunciatorComponent",
		"<init>",
		"(Z)V",
		registers,
	); err != nil {
		t.Fatal(err)
	}
	height, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/Component",
		"getHeight",
		"()I",
		registers,
	)
	if err != nil {
		t.Fatal(err)
	}
	width, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/Component",
		"getWidth",
		"()I",
		registers,
	)
	if err != nil {
		t.Fatal(err)
	}
	if width != 240 || height != 20 {
		t.Fatalf("annunciator geometry = %dx%d, want 240x20", width, height)
	}
}

func TestKTFLWCFormLaysOutChildrenAndScreenCoordinates(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	const (
		form   = uint32(0x10001000)
		first  = uint32(0x10002000)
		second = uint32(0x10003000)
	)
	call := func(
		className, name, descriptor string,
		instance uint32,
		args ...uint32,
	) uint32 {
		t.Helper()
		registers := make([]uint32, cpu.RegisterR12+1)
		registers[1] = instance
		copy(registers[2:], args)
		value, callErr := runtime.handleLWCMethod(
			context.Background(),
			className,
			name,
			descriptor,
			registers,
		)
		if callErr != nil {
			t.Fatal(callErr)
		}
		return value
	}
	call(
		"org/kwis/msp/lwc/FormComponent",
		"<init>",
		"()V",
		form,
	)
	call(
		"org/kwis/msp/lwc/Component",
		"configure",
		"(IIIII)V",
		form,
		10, 20, 100, 80, 3,
	)
	call(
		"org/kwis/msp/lwc/Component",
		"configure",
		"(IIIII)V",
		first,
		0, 0, 30, 10, 3,
	)
	call(
		"org/kwis/msp/lwc/Component",
		"configure",
		"(IIIII)V",
		second,
		0, 0, 40, 20, 3,
	)
	call(
		"org/kwis/msp/lwc/ContainerComponent",
		"addComponent",
		"(Lorg/kwis/msp/lwc/Component;)I",
		form,
		first,
	)
	call(
		"org/kwis/msp/lwc/ContainerComponent",
		"addComponent",
		"(Lorg/kwis/msp/lwc/Component;)I",
		form,
		second,
	)
	call(
		"org/kwis/msp/lwc/FormComponent",
		"setPacked",
		"(Z)V",
		form,
		1,
	)
	call(
		"org/kwis/msp/lwc/FormComponent",
		"setGab",
		"(I)V",
		form,
		2,
	)
	call(
		"org/kwis/msp/lwc/ContainerComponent",
		"validate",
		"()V",
		form,
	)

	firstState := runtime.lwcComponents[first]
	secondState := runtime.lwcComponents[second]
	if firstState.x != 0 || firstState.y != 0 ||
		firstState.width != 100 || firstState.height != 10 {
		t.Fatalf("first child geometry = %+v", firstState)
	}
	if secondState.x != 0 || secondState.y != 12 ||
		secondState.width != 100 || secondState.height != 20 {
		t.Fatalf("second child geometry = %+v", secondState)
	}
	if got := int32(call(
		"org/kwis/msp/lwc/Component",
		"getXOnScreen",
		"()I",
		second,
	)); got != 10 {
		t.Fatalf("second screen x = %d, want 10", got)
	}
	if got := int32(call(
		"org/kwis/msp/lwc/Component",
		"getYOnScreen",
		"()I",
		second,
	)); got != 32 {
		t.Fatalf("second screen y = %d, want 32", got)
	}
	if len(runtime.unimplementedJava) != 0 {
		t.Fatalf("LWC calls recorded as unimplemented: %#v", runtime.unimplementedJava)
	}
}

func TestKTFDisplayCallSeriallyTimeoutQueuesRunnable(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	runnable, err := runtime.newHostJavaObject("java/lang/Thread")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, runnable); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 100); err != nil {
		t.Fatal(err)
	}
	runtime.deferThreads = true
	if _, err := ktfHostJavaMethod(
		"org/kwis/msp/lcdui/Display",
		"callSerially",
		"(Ljava/lang/Runnable;I)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if len(runtime.tasks) != 1 || runtime.tasks[0].done {
		t.Fatalf("callSerially tasks = %#v", runtime.tasks)
	}
}

func TestKTFCalendarGetTimeReturnsModeledDate(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	calendar, err := runtime.newHostJavaObject("java/util/Calendar")
	if err != nil {
		t.Fatal(err)
	}
	const millis = int64(123456789)
	runtime.dates[calendar] = millis
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, calendar); err != nil {
		t.Fatal(err)
	}
	date, err := ktfHostJavaMethod(
		"java/util/Calendar",
		"getTime",
		"()Ljava/util/Date;",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if date == 0 || runtime.dates[date] != millis {
		t.Fatalf(
			"Calendar.getTime date = 0x%08x millis=%d",
			date,
			runtime.dates[date],
		)
	}
}

func TestKTFCallNativeDispatchesHostMethodWithParameterContainer(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	systemClassAddress, err := runtime.ensureJavaClass("java/lang/System")
	if err != nil {
		t.Fatal(err)
	}
	systemClass, err := runtime.inspectJavaClass(systemClassAddress)
	if err != nil {
		t.Fatal(err)
	}
	currentTime, ok := findKTFJavaMethod(
		systemClass,
		"currentTimeMillis",
		"()J",
	)
	if !ok || currentTime.AccessFlags&0x0100 == 0 ||
		currentTime.Body != 0 || currentTime.NativeBody == 0 {
		t.Fatalf("System.currentTimeMillis method = %+v, found=%v", currentTime, ok)
	}
	parameters, err := runtime.allocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, currentTime.NativeBody); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}
	result, err := ktfCallNative(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if result != parameters {
		t.Fatalf("call-native result = 0x%08x", result)
	}
	values, err := runtime.readWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if values[0] != 0 || values[1] != 0 {
		t.Fatalf("native return container = %08x", values)
	}
	if runtime.nativeParameterBase != 0 {
		t.Fatalf(
			"native parameter base leaked: 0x%08x",
			runtime.nativeParameterBase,
		)
	}
}

func TestKTFCallNativePrefersExplicitHostTargetOverStaleOverride(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	target := runtime.registerHostCall(
		"test.explicit_native_target",
		func(context.Context, *ktfRuntime) (uint32, error) {
			return 42, nil
		},
	)
	parameters, err := runtime.allocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	runtime.lastJavaMethod =
		"org/kwis/msp/lcdui/Graphics.getClipHeight()I"
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, target); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}

	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	values, err := runtime.readWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(values, []uint32{42, 0}) {
		t.Fatalf("explicit native target return = %08x", values)
	}
}

func TestKTFCallNativeCorrectsStaleMethodForCachedGuestNative(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	systemClassAddress, err := runtime.ensureJavaClass("java/lang/System")
	if err != nil {
		t.Fatal(err)
	}
	systemClass, err := runtime.inspectJavaClass(systemClassAddress)
	if err != nil {
		t.Fatal(err)
	}
	currentTime, ok := findKTFJavaMethod(
		systemClass,
		"currentTimeMillis",
		"()J",
	)
	if !ok {
		t.Fatal("System.currentTimeMillis was not found")
	}
	if err := runtime.writeU32(
		currentTime.Address+8,
		ktfImageBase|1,
	); err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.allocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	runtime.tickMS = 123
	runtime.lastJavaMethod =
		"org/kwis/msp/lcdui/Graphics.getClipHeight()I"
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, ktfImageBase|1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}

	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	values, err := runtime.readWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(values, []uint32{123, 0}) {
		t.Fatalf("corrected native return = %08x", values)
	}
	if runtime.lastJavaMethod !=
		"java/lang/System.currentTimeMillis()J" {
		t.Fatalf("corrected native method = %q", runtime.lastJavaMethod)
	}
}

func TestKTFJavaStringExposesNativeLayoutAndCopiesToGuest(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.newJavaString("Clet")
	if err != nil {
		t.Fatal(err)
	}
	characters, err := runtime.readJavaFieldWord(value, 0)
	if err != nil {
		t.Fatal(err)
	}
	offset, err := runtime.readJavaFieldWord(value, 4)
	if err != nil {
		t.Fatal(err)
	}
	count, err := runtime.readJavaFieldWord(value, 8)
	if err != nil {
		t.Fatal(err)
	}
	if offset != 0 || count != 4 {
		t.Fatalf("native String layout offset=%d count=%d", offset, count)
	}
	decoded, err := runtime.readJavaCharArrayRange(characters, offset, count)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != "Clet" {
		t.Fatalf("native String characters = %q", decoded)
	}

	destination, err := runtime.allocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	for register, registerValue := range []uint32{
		value,
		destination,
		8,
	} {
		if err := runtime.cpu.WriteRegister(
			uint32(register),
			registerValue,
		); err != nil {
			t.Fatal(err)
		}
	}
	copied, err := ktfJavaStringCopy(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if copied != 4 {
		t.Fatalf("native String copy count = %d", copied)
	}
	output := make([]byte, 5)
	if err := runtime.cpu.ReadMemory(destination, output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, []byte{'C', 'l', 'e', 't', 0}) {
		t.Fatalf("native String copy = %q", output)
	}
}

func TestKTFObjectWaitYieldsDeferredThread(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.ensureJavaClass("java/lang/Object")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.inspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	wait, ok := findKTFJavaMethod(class, "wait", "()V")
	if !ok {
		t.Fatal("Object.wait() host method is missing")
	}
	host, ok := runtime.hostCalls[wait.Body&^1]
	if !ok {
		t.Fatalf("Object.wait() host call 0x%08x is missing", wait.Body)
	}
	runtime.deferThreads = true
	if _, err := host.handler(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if !runtime.yieldRequested {
		t.Fatal("Object.wait() did not request a deferred-thread yield")
	}
	for _, descriptor := range []string{"(J)V", "(JI)V", "()V"} {
		runtime.yieldRequested = false
		override, ok := ktfJavaNativeOverride(
			"java/lang/Object.wait" + descriptor,
		)
		if !ok {
			t.Fatalf("Object.wait%s native override is missing", descriptor)
		}
		if _, err := override.handler(context.Background(), runtime); err != nil {
			t.Fatal(err)
		}
		if !runtime.yieldRequested {
			t.Fatalf("Object.wait%s native override did not yield", descriptor)
		}
	}
}

func TestKTFCardRepaintQueuesPaintAtTaskYield(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	card, err := runtime.newHostJavaObject("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	parent := &ktfTask{}
	runtime.deferThreads = true
	runtime.activeTask = parent
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, card); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfHostJavaMethod(
		"org/kwis/msp/lcdui/Card",
		"repaint",
		"()V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if !runtime.dirtyCards[card] ||
		!slices.Equal(runtime.deferredPaintCards[parent], []uint32{card}) {
		t.Fatalf(
			"deferred repaint: dirty=%t cards=%08x",
			runtime.dirtyCards[card],
			runtime.deferredPaintCards[parent],
		)
	}
	if runtime.deferredShownCards[parent][card] {
		t.Fatal("ordinary repaint incorrectly requested showNotify")
	}

	runtime.activeTask = nil
	if err := runtime.releaseDeferredCardPaints(
		context.Background(),
		parent,
	); err != nil {
		t.Fatal(err)
	}
	if task := runtime.paintTasks[card]; task == nil ||
		!task.presentOnReturn {
		t.Fatalf("repaint task = %#v", task)
	}
	if len(runtime.tasks) != 1 {
		t.Fatalf("repaint queued %d tasks, want paint only", len(runtime.tasks))
	}
}

func TestKTFCallNativeOverridesBrokenFrameworkNative(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.allocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	runtime.lastJavaMethod =
		"org/kwis/msp/lcdui/Display.addJletEventListener" +
			"(Lorg/kwis/msp/lcdui/JletEventListener;)V"
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, ktfImageBase|1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	values, err := runtime.readWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0] != 0 || values[1] != 0 {
		t.Fatalf("native override return container = %08x", values)
	}
}

func TestKTFCallNativeOverridesNullFrameworkNative(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.allocateWords(3)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method string
		want   uint32
	}{
		{"java/lang/System.currentTimeMillis()J", 0},
		{"org/kwis/msp/media/Volume.get()I", 5},
		{"org/kwis/msp/media/Vibrator.on(II)V", 0},
		{"org/kwis/msf/io/Network.connect()I", 1},
		{"org/kwis/msf/io/Network.disconnect()V", 0},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			runtime.lastJavaMethod = test.method
			if err := runtime.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
				t.Fatal(err)
			}
			if err := runtime.cpu.WriteRegister(
				cpu.RegisterR1,
				parameters,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := ktfCallNative(
				context.Background(),
				runtime,
			); err != nil {
				t.Fatal(err)
			}
			values, err := runtime.readWords(parameters, 2)
			if err != nil {
				t.Fatal(err)
			}
			if values[0] != test.want || values[1] != 0 {
				t.Fatalf(
					"native override return = %08x, want %08x",
					values,
					[]uint32{test.want, 0},
				)
			}
		})
	}
}

func TestKTFCallNativeOverridesNullStringValueOfChars(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	chars, err := runtime.newJavaCharArray("WIPI!")
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.allocateWords(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(
		parameters,
		[]uint32{chars, 1, 3, 0},
	); err != nil {
		t.Fatal(err)
	}
	runtime.lastJavaMethod =
		"java/lang/String.valueOf([CII)Ljava/lang/String;"
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	values, err := runtime.readWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(values[0]); got != "IPI" {
		t.Fatalf("String.valueOf(chars, 1, 3) = %q", got)
	}
	if values[1] != 0 {
		t.Fatalf("String.valueOf high return word = 0x%08x", values[1])
	}
}

func TestKTFCallNativeRoutesGraphicsMethodsThroughFramebufferModel(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	graphics, err := runtime.ensureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.allocateWords(3)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(parameters, []uint32{graphics, 0, 0}); err != nil {
		t.Fatal(err)
	}
	runtime.lastJavaMethod =
		"org/kwis/msp/lcdui/Graphics.getClipHeight()I"
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, 0); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	values, err := runtime.readWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if values[0] != 320 || values[1] != 0 {
		t.Fatalf("Graphics.getClipHeight return = %08x", values)
	}
}

func TestKTFDispatchJavaExceptionBuildsGuestRestoreTarget(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	catchClass, err := runtime.ensureJavaClass("java/lang/Exception")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := runtime.allocateWords(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(entry, []uint32{
		4,
		20,
		23,
		catchClass,
	}); err != nil {
		t.Fatal(err)
	}
	table, err := runtime.allocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(table, entry); err != nil {
		t.Fatal(err)
	}
	method, err := runtime.allocateWords(7)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(method, []uint32{
		0,
		0,
		table,
		0,
		1,
		0,
		0,
	}); err != nil {
		t.Fatal(err)
	}
	functions, err := runtime.allocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	const restore = uint32(0x00123457)
	if err := runtime.writeWords(functions, []uint32{0, restore}); err != nil {
		t.Fatal(err)
	}
	frame, err := runtime.allocateWords(17)
	if err != nil {
		t.Fatal(err)
	}
	saved := []uint32{
		0x44,
		0x55,
		0x66,
		0x77,
		0x88,
		0x99,
		0xaa,
		0xbb,
		0xcc,
		0x70001000,
		0x00123457,
	}
	frameWords := append(
		[]uint32{method, 0, 0, 20, 0, functions},
		saved...,
	)
	if err := runtime.writeWords(frame, frameWords); err != nil {
		t.Fatal(err)
	}
	exceptionContext, err := runtime.allocateWords(ktfJavaEnvironmentWords)
	if err != nil {
		t.Fatal(err)
	}
	runtime.exceptionContext = exceptionContext
	if err := runtime.writeU32(exceptionContext+8*4, frame); err != nil {
		t.Fatal(err)
	}
	target, caught, err := runtime.dispatchJavaException(
		"java/lang/NullPointerException",
		0x10203040,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !caught || target.handler != 23 ||
		target.contextBase != frame+6*4 ||
		target.restore != restore {
		t.Fatalf("exception dispatch = target %+v, caught %t", target, caught)
	}
	if len(runtime.javaExceptionFrames) != 1 ||
		!strings.Contains(runtime.javaExceptionFrames[0], "bcp=20") {
		t.Fatalf("exception frames = %v", runtime.javaExceptionFrames)
	}
	if detail, err := runtime.readU32(frame + 4*4); err != nil {
		t.Fatal(err)
	} else if detail != 0x10203040 {
		t.Fatalf("exception detail = 0x%08x", detail)
	}
	contextWords, err := runtime.readWords(target.contextBase, len(saved))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(contextWords, saved) {
		t.Fatalf("exception restore context = %08x, want %08x", contextWords, saved)
	}
}

func TestKTFCallOwnsOnlyNestedJavaExceptionFramesBelowCallerStack(t *testing.T) {
	callerStack := DefaultStackBase + 0x8000
	for _, test := range []struct {
		name           string
		executionDepth int
		contextBase    uint32
		want           bool
	}{
		{
			name:           "root call",
			executionDepth: 1,
			contextBase:    callerStack + 0x100,
			want:           true,
		},
		{
			name:           "nested call frame",
			executionDepth: 2,
			contextBase:    callerStack - 0x100,
			want:           true,
		},
		{
			name:           "suspended caller frame",
			executionDepth: 2,
			contextBase:    callerStack + 0x100,
			want:           false,
		},
		{
			name:           "invalid frame",
			executionDepth: 2,
			contextBase:    0x1000,
			want:           false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := &ktfRuntime{executionDepth: test.executionDepth}
			unwind := &ktfJavaExceptionUnwind{
				target: ktfJavaExceptionTarget{contextBase: test.contextBase},
			}
			if got := runtime.callOwnsJavaExceptionUnwind(callerStack, unwind); got != test.want {
				t.Fatalf("call owns exception unwind = %t, want %t", got, test.want)
			}
		})
	}
}

func TestKTFJavaThrowObjectUsesGuestInstanceClass(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	instance, err := runtime.newHostJavaObject("java/lang/Exception")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, instance); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfJavaThrowObject(
		context.Background(),
		runtime,
	); err == nil {
		t.Fatal("uncaught Java exception object returned without an error")
	} else if !strings.Contains(
		err.Error(),
		"KTF Java exception java/lang/Exception",
	) || !strings.Contains(
		err.Error(),
		"detail=0x"+fmt.Sprintf("%08x", instance),
	) {
		t.Fatalf("Java exception object error = %v", err)
	}
	if runtime.lastJavaThrowName != "java/lang/Exception" {
		t.Fatalf("last Java throw name = %q", runtime.lastJavaThrowName)
	}
}

func TestKTFJavaJumpCallsHostWithoutResettingGuestContext(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	var hostLR uint32
	host := runtime.registerHostCall(
		"test.direct_host",
		func(_ context.Context, runtime *ktfRuntime) (uint32, error) {
			var err error
			hostLR, err = runtime.cpu.ReadRegister(cpu.RegisterLR)
			return 42, err
		},
	)
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, 7); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, host); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterLR, 0x00123457); err != nil {
		t.Fatal(err)
	}
	value, err := ktfJavaJump(1)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if value != 42 {
		t.Fatalf("direct Java host jump returned %d", value)
	}
	if hostLR != 0x00123457 {
		t.Fatalf("direct Java host jump LR = 0x%08x", hostLR)
	}
}

func TestKTFGetJavaFieldCreatesStableHostStaticDescriptor(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	fontClass, err := runtime.ensureJavaClass("org/kwis/msp/lcdui/Font")
	if err != nil {
		t.Fatal(err)
	}
	fullName, err := runtime.allocateBytes(
		append([]byte{0}, []byte("I+SIZE_LARGE")...),
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, fontClass); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, fullName); err != nil {
		t.Fatal(err)
	}
	first, err := ktfGetJavaField(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ktfGetJavaField(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if first == 0 || second != first {
		t.Fatalf("field addresses = 0x%08x, 0x%08x", first, second)
	}
	words, err := runtime.readWords(first, 4)
	if err != nil {
		t.Fatal(err)
	}
	if words[0]&0x0008 == 0 || words[1] != fontClass || words[3] != 16 {
		t.Fatalf("field descriptor = %08x", words)
	}
}

func TestKTFDefaultDisplayStartsWithoutDockedCard(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	display, err := runtime.ensureDefaultDisplay()
	if err != nil {
		t.Fatal(err)
	}
	if card := runtime.displayCards[display]; card != 0 {
		t.Fatalf("default display docked card = 0x%08x, want null", card)
	}

	cardClassAddress, err := runtime.ensureJavaClass(
		"org/kwis/msp/lcdui/Card",
	)
	if err != nil {
		t.Fatal(err)
	}
	cardClass, err := runtime.inspectJavaClass(cardClassAddress)
	if err != nil {
		t.Fatal(err)
	}
	explicitCard, err := runtime.newJavaInstanceForClass(cardClass)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, explicitCard); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, display); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfHostJavaMethod(
		"org/kwis/msp/lcdui/Card",
		"<init>",
		"(Lorg/kwis/msp/lcdui/Display;)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, explicitCard); err != nil {
		t.Fatal(err)
	}
	cardDisplay, err := ktfHostJavaMethod(
		"org/kwis/msp/lcdui/Card",
		"getDisplay",
		"()Lorg/kwis/msp/lcdui/Display;",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if cardDisplay != display {
		t.Fatalf(
			"explicit card display = 0x%08x, want 0x%08x",
			cardDisplay,
			display,
		)
	}

	runtime.deferThreads = true
	parentTask := &ktfTask{}
	childTask := &ktfTask{}
	runtime.tasks = []*ktfTask{parentTask, childTask}
	runtime.activeTask = parentTask
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, display); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, explicitCard); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfHostJavaMethod(
		"org/kwis/msp/lcdui/Display",
		"pushCard",
		"(Lorg/kwis/msp/lcdui/Card;)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.displayCards[display] != explicitCard {
		t.Fatalf(
			"pushed card = 0x%08x, want 0x%08x",
			runtime.displayCards[display],
			explicitCard,
		)
	}
	if task := runtime.paintTasks[explicitCard]; task != nil {
		t.Fatalf("pushCard scheduled paint before its caller yielded: %#v", task)
	}
	runtime.activeTask = nil
	if err := runtime.releaseDeferredCardPaints(
		context.Background(),
		parentTask,
	); err != nil {
		t.Fatal(err)
	}
	if task := runtime.paintTasks[explicitCard]; task == nil ||
		!task.presentOnReturn || !task.bestEffortPaint {
		t.Fatalf("pushed card paint task = %#v", task)
	} else if len(runtime.tasks) != 4 || runtime.tasks[3] != task {
		t.Fatalf(
			"paint task order = %#v, want parent, child, show, paint",
			runtime.tasks,
		)
	}
}

func TestKTFJavaArrayCopySupportsOverlappingRanges(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	array, err := runtime.newJavaByteArray([]byte{1, 2, 3, 4, 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.javaArrayCopy(array, 0, array, 1, 4); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.readJavaByteArray(array)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{1, 1, 2, 3, 4}
	if !bytes.Equal(got, want) {
		t.Fatalf("arraycopy result = %v, want %v", got, want)
	}
	if _, ok := ktfJavaNativeOverride(
		"java/lang/System.arraycopy(Ljava/lang/Object;I" +
			"Ljava/lang/Object;II)V",
	); !ok {
		t.Fatal("System.arraycopy native override is missing")
	}
}

func TestKTFJavaArrayCopyToleratesNullOptionalBuffer(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.javaArrayCopy(0, 0, 0, 0, 16); err != nil {
		t.Fatal(err)
	}
}

func TestKTFJavaArrayCopyRaisesGuestExceptions(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	source, err := runtime.newJavaByteArray([]byte{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	target, err := runtime.newJavaReferenceArray("[[B", []uint32{0, 0})
	if err != nil {
		t.Fatal(err)
	}
	var unhandled *ktfUnhandledJavaException
	if err := runtime.javaArrayCopy(source, 0, target, 0, 1); !errors.As(
		err,
		&unhandled,
	) || unhandled.name != "java/lang/ArrayStoreException" {
		t.Fatalf("arraycopy type error = %v", err)
	}
	unhandled = nil
	if err := runtime.javaArrayCopy(source, 2, source, 0, 1); !errors.As(
		err,
		&unhandled,
	) || unhandled.name != "java/lang/ArrayIndexOutOfBoundsException" {
		t.Fatalf("arraycopy bounds error = %v", err)
	}
}

func TestKTFInputStreamReadReturnsEOFForNullOptionalBuffer(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	stream, err := runtime.newHostJavaObject("java/io/InputStream")
	if err != nil {
		t.Fatal(err)
	}
	runtime.inputStreams[stream] = &ktfInputStream{data: []byte{1}}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, stream); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 0); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.handleInputStreamMethod(
		context.Background(),
		"read",
		"([BII)I",
	)
	if err != nil {
		t.Fatal(err)
	}
	if value != ^uint32(0) {
		t.Fatalf("InputStream.read(null, ...) = %d, want -1", value)
	}
}

func TestKTFDataInputStreamPrimitiveEOFThrows(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.exceptionContext, err = runtime.allocateWords(
		ktfJavaEnvironmentWords,
	)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := runtime.newHostJavaObject("java/io/DataInputStream")
	if err != nil {
		t.Fatal(err)
	}
	runtime.inputStreams[stream] = &ktfInputStream{}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, stream); err != nil {
		t.Fatal(err)
	}

	value, err := runtime.handleInputStreamMethod(
		context.Background(),
		"readInt",
		"()I",
	)
	if err == nil || !strings.Contains(err.Error(), "java/io/EOFException") {
		t.Fatalf("DataInputStream.readInt at EOF = 0x%08x, %v", value, err)
	}
}

func TestKTFDataInputStreamReadUTFDecodesModifiedUTF8(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	stream, err := runtime.newHostJavaObject("java/io/DataInputStream")
	if err != nil {
		t.Fatal(err)
	}
	encoded := []byte{
		0x00, 0x0b,
		0x41,
		0xc0, 0x80,
		0xc3, 0xa9,
		0xed, 0xa0, 0xbd,
		0xed, 0xb8, 0x80,
	}
	runtime.inputStreams[stream] = &ktfInputStream{data: encoded}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, stream); err != nil {
		t.Fatal(err)
	}

	value, err := runtime.handleInputStreamMethod(
		context.Background(),
		"readUTF",
		"()Ljava/lang/String;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := runtime.javaStringValue(value), "A\x00é😀"; got != want {
		t.Fatalf("DataInputStream.readUTF = %q, want %q", got, want)
	}
	if position := runtime.inputStreams[stream].position; position != uint32(len(encoded)) {
		t.Fatalf("DataInputStream position = %d, want %d", position, len(encoded))
	}
}

func TestDecodeKTFModifiedUTF8RejectsMalformedSequences(t *testing.T) {
	for _, encoded := range [][]byte{
		{0xc2},
		{0xc2, 0x20},
		{0xe1, 0x80},
		{0xe1, 0x80, 0x20},
		{0xf0, 0x90, 0x80, 0x80},
		{0x80},
	} {
		if value, err := decodeKTFModifiedUTF8(encoded); err == nil {
			t.Fatalf("decode modified UTF-8 %x = %q without error", encoded, value)
		}
	}
}

func TestKTFTaskExceptionFramesAreSavedIndependently(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.exceptionContext, err = runtime.allocateWords(
		ktfJavaEnvironmentWords,
	)
	if err != nil {
		t.Fatal(err)
	}

	first := &ktfTask{}
	second := &ktfTask{}
	const (
		firstFrame  = uint32(0x7ffffe40)
		secondFrame = uint32(0x7ffefe40)
	)
	if err := runtime.writeU32(runtime.exceptionContext+8*4, firstFrame); err != nil {
		t.Fatal(err)
	}
	if err := runtime.saveTaskContext(first); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(runtime.exceptionContext+8*4, secondFrame); err != nil {
		t.Fatal(err)
	}
	if err := runtime.saveTaskContext(second); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		task *ktfTask
		want uint32
	}{
		{name: "first", task: first, want: firstFrame},
		{name: "second", task: second, want: secondFrame},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := runtime.restoreTaskExceptionFrame(test.task); err != nil {
				t.Fatal(err)
			}
			got, err := runtime.readU32(runtime.exceptionContext + 8*4)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("restored exception frame = 0x%08x, want 0x%08x", got, test.want)
			}
		})
	}
}

func TestKTFStartedThreadWaitsForParentInitializationGrace(t *testing.T) {
	parent := &ktfTask{}
	child := &ktfTask{}
	runtime := &ktfRuntime{
		tasks:              []*ktfTask{parent, child},
		taskCursor:         1,
		activeTask:         parent,
		activeInstructions: 123,
	}

	runtime.deferStartedThread(child)
	if child.startBlocker != parent {
		t.Fatal("new thread was not blocked behind its starting task")
	}
	if got, want := parent.childStartGrace, ktfThreadStartGrace+123; got != want {
		t.Fatalf("parent start grace = %d, want %d", got, want)
	}
	if got := runtime.nextRunnableTask(); got != parent {
		t.Fatalf("scheduler selected blocked child %p, want parent %p", got, parent)
	}

	// Instructions executed before Thread.start in the current slice must not
	// consume the new thread's grace period.
	runtime.chargeThreadStartGrace(parent, 123)
	if got := parent.childStartGrace; got != ktfThreadStartGrace {
		t.Fatalf("remaining start grace = %d, want %d", got, ktfThreadStartGrace)
	}
	runtime.chargeThreadStartGrace(parent, ktfThreadStartGrace-1)
	if child.startBlocker != parent {
		t.Fatal("child was released before the grace period expired")
	}
	runtime.chargeThreadStartGrace(parent, 1)
	if child.startBlocker != nil {
		t.Fatal("child remained blocked after the grace period expired")
	}
	if got := runtime.nextRunnableTask(); got != child {
		t.Fatalf("scheduler selected task %p after release, want child %p", got, child)
	}
}

func TestKTFStartedThreadReleasesWhenParentYields(t *testing.T) {
	parent := &ktfTask{childStartGrace: ktfThreadStartGrace}
	child := &ktfTask{startBlocker: parent}
	runtime := &ktfRuntime{tasks: []*ktfTask{parent, child}}

	runtime.releaseStartedThreads(parent, "yield")
	if parent.childStartGrace != 0 {
		t.Fatalf("parent start grace = %d after yield", parent.childStartGrace)
	}
	if child.startBlocker != nil {
		t.Fatal("child remained blocked after parent yield")
	}
}

func TestKTFTaskRecordsPresentationAfterPaintReturns(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.newTask(ktfReturnSentinel|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	task.presentOnReturn = true
	runtime.tasks = append(runtime.tasks, task)
	result := runtime.runTaskSlice(context.Background(), 16)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != cpu.StopExited {
		t.Fatalf("paint task stopped as %v, want exited", result.Reason)
	}
	if runtime.presentCount != 1 {
		t.Fatalf("paint task presentations = %d, want 1", runtime.presentCount)
	}
}

func TestKTFJavaPresentationRetakesScreenFromWIPIC(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	graphics, err := runtime.ensureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	javaSurface := runtime.graphicsServices[graphics]
	if javaSurface == 0 {
		t.Fatal("Java screen graphics has no shared surface")
	}
	if _, err := runtime.ensureWIPICScreenFramebuffer(); err != nil {
		t.Fatal(err)
	}
	if runtime.services.Graphics.Screen() == javaSurface {
		t.Fatal("WIPI-C screen did not take presentation ownership")
	}
	if err := runtime.recordPresentation(); err != nil {
		t.Fatal(err)
	}
	if got := runtime.services.Graphics.Screen(); got != javaSurface {
		t.Fatalf("presented surface = %s, want Java screen %s", got, javaSurface)
	}
}

func TestKTFReturningTaskDoesNotSkipNextRunnableTask(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runnable, err := runtime.newTask(ktfImageBase|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	returning, err := runtime.newTask(ktfReturnSentinel|1, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	runtime.tasks = []*ktfTask{runnable, returning}
	runtime.taskCursor = 1

	result := runtime.runTaskSlice(context.Background(), 16)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != cpu.StopBudget || !returning.done {
		t.Fatalf(
			"returning task result = %+v, done=%t",
			result,
			returning.done,
		)
	}
	if got := runtime.nextRunnableTask(); got != runnable {
		t.Fatalf(
			"scheduler selected %p after task return, want %p",
			got,
			runnable,
		)
	}
}

func TestKTFBestEffortInitialPaintDiscardsUnhandledJavaException(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	procedure := runtime.registerHostCall(
		"synthetic.initial.paint",
		func(context.Context, *ktfRuntime) (uint32, error) {
			return 0, &ktfUnhandledJavaException{
				name:    "java/lang/NullPointerException",
				detail:  0x10001000,
				context: "synthetic initial paint",
			}
		},
	)
	task, err := runtime.newTask(procedure|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	const card = uint32(0x10002000)
	task.bestEffortPaint = true
	task.paintCard = card
	runtime.tasks = append(runtime.tasks, task)
	runtime.paintTasks[card] = task

	result := runtime.runTaskSlice(context.Background(), 16)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != cpu.StopExited || !task.done {
		t.Fatalf(
			"best-effort paint result = %+v, done=%t",
			result,
			task.done,
		)
	}
	if runtime.paintTasks[card] != nil {
		t.Fatal("discarded initial paint remained pending")
	}
	if runtime.paintInitializedCards[card] {
		t.Fatal("discarded initial paint closed its best-effort window")
	}
	found := false
	for _, trace := range runtime.hostTrace {
		if strings.Contains(
			trace,
			"java_initial_paint_discard:"+
				"java/lang/NullPointerException",
		) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf(
			"initial paint discard missing from trace: %v",
			runtime.hostTrace,
		)
	}
}

func TestKTFHostVTableCollisionRedispatchesToGuestReceiver(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client: []byte{
			0x7f, 0x20, // movs r0, #0x7f
			0x70, 0x47, // bx lr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.ensureJavaClass("test/GuestRenderer")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.inspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	methodAddress, err := runtime.addHostJavaMethod(
		class,
		"draw",
		"()I",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(methodAddress, ktfImageBase|1); err != nil {
		t.Fatal(err)
	}
	delete(runtime.hostJavaClass, classAddress)
	class, err = runtime.inspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := runtime.newJavaInstanceForClass(class)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, instance); err != nil {
		t.Fatal(err)
	}

	value, err := ktfHostJavaMethod(
		"java/lang/System",
		"draw",
		"()I",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if value != 0x7f {
		t.Fatalf("redispatched guest return = 0x%08x, want 0x0000007f", value)
	}
	if runtime.unimplementedJava["java/lang/System.draw()I"] != 0 {
		t.Fatal("redispatched guest method was recorded as unimplemented")
	}
	found := false
	for _, trace := range runtime.hostTrace {
		if strings.Contains(
			trace,
			"java_virtual_redispatch:java/lang/System.draw()I:"+
				"actual=test/GuestRenderer",
		) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("guest redispatch missing from trace: %v", runtime.hostTrace)
	}
}

func TestKTFJavaParameterWords(t *testing.T) {
	for _, test := range []struct {
		descriptor string
		want       int
		ok         bool
	}{
		{descriptor: "()V", want: 0, ok: true},
		{descriptor: "(IJD)V", want: 5, ok: true},
		{
			descriptor: "([B[[ILjava/lang/String;)Ljava/lang/Object;",
			want:       3,
			ok:         true,
		},
		{descriptor: "(Ljava/lang/String)V", ok: false},
		{descriptor: "I)V", ok: false},
	} {
		got, ok := ktfJavaParameterWords(test.descriptor)
		if got != test.want || ok != test.ok {
			t.Errorf(
				"ktfJavaParameterWords(%q) = %d, %t; want %d, %t",
				test.descriptor,
				got,
				ok,
				test.want,
				test.ok,
			)
		}
	}
}

func TestKTFPaintCardCoalescesWhilePaintTaskIsPending(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()

	const card = uint32(0x10001000)
	pending := &ktfTask{}
	runtime.dirtyCards[card] = true
	runtime.paintTasks[card] = pending

	if err := runtime.paintCard(context.Background(), card); err != nil {
		t.Fatal(err)
	}
	if runtime.dirtyCards[card] {
		t.Fatal("coalesced card remained dirty")
	}
	if runtime.paintTasks[card] != pending {
		t.Fatal("coalesced card replaced its pending paint task")
	}
	if len(runtime.tasks) != 0 {
		t.Fatalf("coalesced card queued %d additional tasks", len(runtime.tasks))
	}
	if len(runtime.hostTrace) != 1 ||
		runtime.hostTrace[0] != "java_paint_coalesce:card=0x10001000" {
		t.Fatalf("coalesce trace = %v", runtime.hostTrace)
	}
}

func TestKTFSystemStreamsAreInitializedHostObjects(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	systemAddress, err := runtime.ensureJavaClass("java/lang/System")
	if err != nil {
		t.Fatal(err)
	}
	systemClass, err := runtime.inspectJavaClass(systemAddress)
	if err != nil {
		t.Fatal(err)
	}
	for _, fieldSpec := range []struct {
		name       string
		descriptor string
		className  string
	}{
		{name: "in", descriptor: "Ljava/io/InputStream;", className: "java/io/InputStream"},
		{name: "out", descriptor: "Ljava/io/PrintStream;", className: "java/io/PrintStream"},
		{name: "err", descriptor: "Ljava/io/PrintStream;", className: "java/io/PrintStream"},
	} {
		field, err := runtime.resolveJavaField(
			systemClass,
			fieldSpec.name,
			fieldSpec.descriptor,
		)
		if err != nil {
			t.Fatal(err)
		}
		value, err := runtime.readU32(field + 12)
		if err != nil {
			t.Fatal(err)
		}
		if value == 0 {
			t.Fatalf("System.%s is null", fieldSpec.name)
		}
		words, err := runtime.readWords(value, 2)
		if err != nil {
			t.Fatal(err)
		}
		class, err := runtime.inspectJavaClass(words[1])
		if err != nil {
			t.Fatal(err)
		}
		if class.Name != fieldSpec.className {
			t.Fatalf(
				"System.%s class = %q, want %q",
				fieldSpec.name,
				class.Name,
				fieldSpec.className,
			)
		}
	}
}

func TestKTFResolveJavaMethodFollowsSingleWordClassReference(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.ensureJavaClass("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	reference, err := runtime.allocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(reference, classAddress); err != nil {
		t.Fatal(err)
	}

	methodAddress, err := runtime.resolveJavaMethod(
		reference,
		"repaint",
		"()V",
	)
	if err != nil {
		t.Fatal(err)
	}
	method, err := runtime.inspectJavaMethod(methodAddress)
	if err != nil {
		t.Fatal(err)
	}
	if method.Name != "repaint" || method.Descriptor != "()V" {
		t.Fatalf("resolved method = %s%s", method.Name, method.Descriptor)
	}
}

func TestKTFResolveJavaMethodAcceptsDirectVTable(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.ensureJavaClass("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.inspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}

	methodAddress, err := runtime.resolveJavaMethod(
		class.VTable,
		"serviceRepaints",
		"()V",
	)
	if err != nil {
		t.Fatal(err)
	}
	method, err := runtime.inspectJavaMethod(methodAddress)
	if err != nil {
		t.Fatal(err)
	}
	if method.Name != "serviceRepaints" || method.Descriptor != "()V" {
		t.Fatalf("resolved method = %s%s", method.Name, method.Descriptor)
	}
}

func TestKTFJavaNewRunsClassInitializerOnce(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.ensureJavaClass("test/Initialized")
	if err != nil {
		t.Fatal(err)
	}
	classWords, err := runtime.readWords(classAddress, 5)
	if err != nil {
		t.Fatal(err)
	}
	descriptorWords, err := runtime.readWords(classWords[2], 9)
	if err != nil {
		t.Fatal(err)
	}
	callCount := 0
	body := runtime.registerHostCall(
		"test.class_initializer",
		func(context.Context, *ktfRuntime) (uint32, error) {
			callCount++
			return 0, nil
		},
	)
	fullName, err := runtime.allocateBytes(
		[]byte("\x00()V+<clinit>"),
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	method, err := runtime.allocateWords(7)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(method, []uint32{
		body,
		classAddress,
		0,
		fullName,
		0,
		uint32(0x0008) << 16,
		0,
	}); err != nil {
		t.Fatal(err)
	}
	methods, err := runtime.allocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(methods, []uint32{method, 0}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(classWords[2]+12, methods); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(
		classWords[2]+24,
		descriptorWords[6]&0xffff0000|1,
	); err != nil {
		t.Fatal(err)
	}

	for index := 0; index < 2; index++ {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0,
			classAddress,
		); err != nil {
			t.Fatal(err)
		}
		instance, err := ktfJavaNew(context.Background(), runtime)
		if err != nil {
			t.Fatal(err)
		}
		if instance == 0 {
			t.Fatal("Java allocation returned null")
		}
	}
	if callCount != 1 {
		t.Fatalf("class initializer calls = %d, want 1", callCount)
	}
	if runtime.javaClassInit[classAddress] != ktfJavaClassInitialized {
		t.Fatalf(
			"class initializer state = %d",
			runtime.javaClassInit[classAddress],
		)
	}
}

func TestKTFResolveJavaMethodAugmentsBodylessHostDeclaration(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.ensureJavaClass(
		"java/lang/IllegalStateException",
	)
	if err != nil {
		t.Fatal(err)
	}
	delete(runtime.hostJavaClass, classAddress)
	runtime.rememberRegisteredJavaClass(
		"java/lang/IllegalStateException",
		classAddress,
	)
	if !runtime.hostJavaClass[classAddress] {
		t.Fatal("registered java/lang class was not marked platform-owned")
	}
	classWords, err := runtime.readWords(classAddress, 5)
	if err != nil {
		t.Fatal(err)
	}
	descriptorWords, err := runtime.readWords(classWords[2], 9)
	if err != nil {
		t.Fatal(err)
	}
	fullName, err := runtime.allocateBytes([]byte("\x00()V+<init>"), true)
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := runtime.allocateWords(7)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(declaration, []uint32{
		0,
		classAddress,
		0,
		fullName,
		0,
		0,
		0,
	}); err != nil {
		t.Fatal(err)
	}
	methods, err := runtime.allocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(methods, []uint32{declaration, 0}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(classWords[2]+12, methods); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(
		classWords[2]+24,
		descriptorWords[6]&0xffff0000|1,
	); err != nil {
		t.Fatal(err)
	}
	class, err := runtime.inspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.implementBodylessPlatformMethods(class); err != nil {
		t.Fatal(err)
	}
	patched, err := runtime.inspectJavaMethod(declaration)
	if err != nil {
		t.Fatal(err)
	}
	if patched.Body == 0 {
		t.Fatal("original bodyless declaration was not patched")
	}
	if _, ok := runtime.hostCalls[patched.Body&^1]; !ok {
		t.Fatalf("patched body 0x%08x is not a host call", patched.Body)
	}

	methodAddress, err := runtime.resolveJavaMethod(
		classAddress,
		"<init>",
		"()V",
	)
	if err != nil {
		t.Fatal(err)
	}
	method, err := runtime.inspectJavaMethod(methodAddress)
	if err != nil {
		t.Fatal(err)
	}
	if method.Body == 0 {
		t.Fatal("bodyless host declaration was not augmented")
	}
	if _, ok := runtime.hostCalls[method.Body&^1]; !ok {
		t.Fatalf("augmented body 0x%08x is not a host call", method.Body)
	}
}

func TestKTFHostJavaLongReturnUsesR0R1(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 0xdeadbeef); err != nil {
		t.Fatal(err)
	}
	low, err := ktfHostJavaMethod(
		"java/lang/Runtime",
		"totalMemory",
		"()J",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	high, err := runtime.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		t.Fatal(err)
	}
	if low != guestHeapSize || high != 0 {
		t.Fatalf(
			"Runtime.totalMemory = 0x%08x%08x, want 0x%016x",
			high,
			low,
			uint64(guestHeapSize),
		)
	}

	const dateValue = uint64(0x1122334455667788)
	runtime.dates[1] = int64(dateValue)
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 1); err != nil {
		t.Fatal(err)
	}
	low, err = ktfHostJavaMethod(
		"java/util/Date",
		"getTime",
		"()J",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	high, err = runtime.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		t.Fatal(err)
	}
	if uint64(high)<<32|uint64(low) != dateValue {
		t.Fatalf(
			"Date.getTime = 0x%08x%08x, want 0x%016x",
			high,
			low,
			dateValue,
		)
	}
}

func TestKTFImageAndFontFactoriesReturnHostObjects(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 23); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 17); err != nil {
		t.Fatal(err)
	}
	imageObject, err := runtime.handleImageMethod(
		"createImage",
		"(II)Lorg/kwis/msp/lcdui/Image;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if source := runtime.images[imageObject]; source == nil ||
		source.Bounds().Dx() != 23 || source.Bounds().Dy() != 17 {
		t.Fatalf("host image = %#v", source)
	}
	font, err := runtime.ensureDefaultFont()
	if err != nil {
		t.Fatal(err)
	}
	if font == 0 {
		t.Fatal("default font is null")
	}
}

func TestKTFStringMethodsProduceGuestStringsAndArrays(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	value, err := runtime.newJavaString("  가abc  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, value); err != nil {
		t.Fatal(err)
	}
	trimmed, err := runtime.handleStringMethod(
		"trim",
		"()Ljava/lang/String;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(trimmed); got != "가abc" {
		t.Fatalf("trimmed string = %q", got)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, trimmed); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 4); err != nil {
		t.Fatal(err)
	}
	substring, err := runtime.handleStringMethod(
		"substring",
		"(II)Ljava/lang/String;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(substring); got != "abc" {
		t.Fatalf("substring = %q", got)
	}
	unicodeValue, err := runtime.newJavaString(
		"\uac00\ub098\ub2e4\U0001f600",
	)
	if err != nil {
		t.Fatal(err)
	}
	needle, err := runtime.newJavaString("\ub2e4")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, unicodeValue); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, needle); err != nil {
		t.Fatal(err)
	}
	index, err := runtime.handleStringMethod(
		"indexOf",
		"(Ljava/lang/String;)I",
	)
	if err != nil {
		t.Fatal(err)
	}
	if index != 2 {
		t.Fatalf("UTF-16 String.indexOf = %d, want 2", int32(index))
	}
	delimited, err := runtime.newJavaString("abc\x00def\x00")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, delimited); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 0); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 4); err != nil {
		t.Fatal(err)
	}
	index, err = runtime.handleStringMethod("indexOf", "(II)I")
	if err != nil {
		t.Fatal(err)
	}
	if index != 7 {
		t.Fatalf("String.indexOf(NUL, 4) = %d, want 7", int32(index))
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, unicodeValue); err != nil {
		t.Fatal(err)
	}
	length, err := runtime.handleStringMethod("length", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if length != 5 {
		t.Fatalf("UTF-16 String.length = %d, want 5", length)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 3); err != nil {
		t.Fatal(err)
	}
	character, err := runtime.handleStringMethod("charAt", "(I)C")
	if err != nil {
		t.Fatal(err)
	}
	if character != 0xd83d {
		t.Fatalf("UTF-16 String.charAt = 0x%04x, want high surrogate", character)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 5); err != nil {
		t.Fatal(err)
	}
	emoji, err := runtime.handleStringMethod(
		"substring",
		"(II)Ljava/lang/String;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(emoji); got != "\U0001f600" {
		t.Fatalf("UTF-16 substring = %q", got)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, substring); err != nil {
		t.Fatal(err)
	}
	array, err := runtime.handleStringMethod("getBytes", "()[B")
	if err != nil {
		t.Fatal(err)
	}
	data, err := runtime.readJavaByteArray(array)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("abc")) {
		t.Fatalf("string bytes = %q", data)
	}
	koreanBytes := []byte{0xb0, 0xa1}
	koreanArray, err := runtime.newJavaByteArray(koreanBytes)
	if err != nil {
		t.Fatal(err)
	}
	korean, err := runtime.newHostJavaObject("java/lang/String")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, korean); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, koreanArray); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleStringMethod("<init>", "([B)V"); err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(korean); got != "가" {
		t.Fatalf("EUC-KR String(byte[]) = %q, want %q", got, "가")
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, korean); err != nil {
		t.Fatal(err)
	}
	encodedKorean, err := runtime.handleStringMethod("getBytes", "()[B")
	if err != nil {
		t.Fatal(err)
	}
	encodedKoreanBytes, err := runtime.readJavaByteArray(encodedKorean)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encodedKoreanBytes, koreanBytes) {
		t.Fatalf(
			"EUC-KR String.getBytes = %x, want %x",
			encodedKoreanBytes,
			koreanBytes,
		)
	}
	empty, err := runtime.newHostJavaObject("java/lang/String")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, empty); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleStringMethod("<init>", "([BII)V"); err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(empty); got != "" {
		t.Fatalf("String(null) = %q, want empty compatibility string", got)
	}
	if err := runtime.cpu.WriteRegister(
		cpu.RegisterR1,
		^uint32(41),
	); err != nil {
		t.Fatal(err)
	}
	formatted, err := runtime.handleStringMethod(
		"valueOf",
		"(I)Ljava/lang/String;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(formatted); got != "-42" {
		t.Fatalf("String.valueOf(-42) = %q", got)
	}
	stringClass, err := runtime.inspectJavaClass(
		runtime.javaClasses["java/lang/String"],
	)
	if err != nil {
		t.Fatal(err)
	}
	valueOf, ok := findKTFJavaMethod(
		stringClass,
		"valueOf",
		"(I)Ljava/lang/String;",
	)
	if !ok || valueOf.AccessFlags&0x0008 == 0 {
		t.Fatalf("String.valueOf(int) flags = 0x%04x, found=%t", valueOf.AccessFlags, ok)
	}
	untracked, err := runtime.newHostJavaObject("java/lang/String")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, untracked); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, untracked); err != nil {
		t.Fatal(err)
	}
	equal, err := runtime.handleStringMethod(
		"equals",
		"(Ljava/lang/Object;)Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	if equal != 1 {
		t.Fatal("an untracked String was not equal to itself")
	}
	left, err := runtime.newJavaString("/3")
	if err != nil {
		t.Fatal(err)
	}
	right, err := runtime.newJavaString("/1")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, left); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, right); err != nil {
		t.Fatal(err)
	}
	comparison, err := runtime.handleStringMethod(
		"compareTo",
		"(Ljava/lang/String;)I",
	)
	if err != nil {
		t.Fatal(err)
	}
	if int32(comparison) != 2 {
		t.Fatalf("String.compareTo = %d, want 2", int32(comparison))
	}
	supplementary, err := runtime.newJavaString("\U0001f600")
	if err != nil {
		t.Fatal(err)
	}
	privateUse, err := runtime.newJavaString("\ue000")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, supplementary); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, privateUse); err != nil {
		t.Fatal(err)
	}
	comparison, err = runtime.handleStringMethod(
		"compareTo",
		"(Ljava/lang/String;)I",
	)
	if err != nil {
		t.Fatal(err)
	}
	wantComparison := int32(0xd83d - 0xe000)
	if int32(comparison) != wantComparison {
		t.Fatalf(
			"UTF-16 String.compareTo = %d, want %d",
			int32(comparison),
			wantComparison,
		)
	}
	ascii, err := runtime.newJavaString("abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, ascii); err != nil {
		t.Fatal(err)
	}
	hash, err := runtime.handleStringMethod("hashCode", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if int32(hash) != 96354 {
		t.Fatalf("String.hashCode(\"abc\") = %d, want 96354", int32(hash))
	}
	if err := runtime.cpu.WriteRegister(
		cpu.RegisterR1,
		supplementary,
	); err != nil {
		t.Fatal(err)
	}
	hash, err = runtime.handleStringMethod("hashCode", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if int32(hash) != 1772899 {
		t.Fatalf(
			"UTF-16 String.hashCode(emoji) = %d, want 1772899",
			int32(hash),
		)
	}
}

func TestKTFHostStreamsAndCalendarReturnUsableObjects(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runtime.newHostJavaObject("java/io/ByteArrayOutputStream")
	if err != nil {
		t.Fatal(err)
	}
	data, err := runtime.newJavaByteArray([]byte{4, 5, 6})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, output); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleByteArrayOutputStreamMethod("<init>", "()V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, data); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleByteArrayOutputStreamMethod("write", "([B)V"); err != nil {
		t.Fatal(err)
	}
	copied, err := runtime.handleByteArrayOutputStreamMethod(
		"toByteArray",
		"()[B",
	)
	if err != nil {
		t.Fatal(err)
	}
	copiedData, err := runtime.readJavaByteArray(copied)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copiedData, []byte{4, 5, 6}) {
		t.Fatalf("byte output = %v", copiedData)
	}
	input, err := runtime.handleFileMethod(
		"openInputStream",
		"()Ljava/io/InputStream;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if input == 0 || runtime.inputStreams[input] == nil {
		t.Fatalf("file input stream = 0x%08x", input)
	}
	calendar, err := runtime.handleCalendarMethod(
		"getInstance",
		"()Ljava/util/Calendar;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if calendar == 0 {
		t.Fatal("Calendar.getInstance returned null")
	}
}

func TestKTFIntegerRandomAndDataOutputSemantics(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}

	text, err := runtime.newJavaString("-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, text); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.handleIntegerMethod(
		"parseInt",
		"(Ljava/lang/String;)I",
	)
	if err != nil {
		t.Fatal(err)
	}
	if int32(value) != -123 {
		t.Fatalf("Integer.parseInt = %d", int32(value))
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 0x79); err != nil {
		t.Fatal(err)
	}
	hex, err := runtime.handleIntegerMethod(
		"toHexString",
		"(I)Ljava/lang/String;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(hex); got != "79" {
		t.Fatalf("Integer.toHexString(0x79) = %q, want 79", got)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, ^uint32(0)); err != nil {
		t.Fatal(err)
	}
	hex, err = runtime.handleIntegerMethod(
		"toHexString",
		"(I)Ljava/lang/String;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(hex); got != "ffffffff" {
		t.Fatalf("Integer.toHexString(-1) = %q, want ffffffff", got)
	}

	random, err := runtime.newHostJavaObject("java/util/Random")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, random); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleRandomMethod("<init>", "()V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 7); err != nil {
		t.Fatal(err)
	}
	randomValue, err := runtime.handleRandomMethod("nextInt", "(I)I")
	if err != nil {
		t.Fatal(err)
	}
	if randomValue >= 7 {
		t.Fatalf("Random.nextInt(7) = %d", randomValue)
	}

	target, err := runtime.newHostJavaObject("java/io/ByteArrayOutputStream")
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := runtime.newHostJavaObject("java/io/DataOutputStream")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, wrapper); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, target); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleOutputStreamMethod(
		"<init>",
		"(Ljava/io/OutputStream;)V",
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 0x01020304); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleOutputStreamMethod("writeInt", "(I)V"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(runtime.outputStreams[target], []byte{1, 2, 3, 4}) {
		t.Fatalf("DataOutputStream bytes = %x", runtime.outputStreams[target])
	}

	input, err := runtime.newHostJavaObject("java/io/ByteArrayInputStream")
	if err != nil {
		t.Fatal(err)
	}
	inputData, err := runtime.newJavaByteArray([]byte{0x01, 0x02, 0x03, 0x04})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, input); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, inputData); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleInputStreamMethod(
		context.Background(),
		"<init>",
		"([B)V",
	); err != nil {
		t.Fatal(err)
	}
	readValue, err := runtime.handleInputStreamMethod(
		context.Background(),
		"readInt",
		"()I",
	)
	if err != nil {
		t.Fatal(err)
	}
	if readValue != 0x01020304 {
		t.Fatalf("DataInput.readInt = 0x%08x", readValue)
	}
}

func TestKTFDataInputStreamDelegatesToApplicationInputStream(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client: []byte{
			0x7f, 0x20, // movs r0, #0x7f
			0x70, 0x47, // bx lr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}

	classAddress, err := runtime.ensureJavaClass("test/ApplicationInputStream")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.inspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	method, err := runtime.addHostJavaMethod(class, "read", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(method, ktfImageBase|1); err != nil {
		t.Fatal(err)
	}
	delete(runtime.hostJavaClass, classAddress)
	class, err = runtime.inspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	source, err := runtime.newJavaInstanceForClass(class)
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := runtime.newHostJavaObject("java/io/DataInputStream")
	if err != nil {
		t.Fatal(err)
	}
	runtime.inputTargets[wrapper] = source
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, wrapper); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.handleInputStreamMethod(
		context.Background(),
		"readUnsignedByte",
		"()I",
	)
	if err != nil {
		t.Fatal(err)
	}
	if value != 0x7f {
		t.Fatalf("delegated DataInputStream.readUnsignedByte = 0x%08x", value)
	}
}

func TestKTFFileReadWriteRoundTrip(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}

	file, err := runtime.newHostJavaObject("org/kwis/msp/io/File")
	if err != nil {
		t.Fatal(err)
	}
	filename, err := runtime.newJavaString("save/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: file,
		cpu.RegisterR2: filename,
		cpu.RegisterR3: 3,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleFileMethod(
		"<init>",
		"(Ljava/lang/String;I)V",
	); err != nil {
		t.Fatal(err)
	}

	source, err := runtime.newJavaByteArray([]byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	stack := DefaultStackBase + 0x100
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(stack, 3); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: file,
		cpu.RegisterR2: source,
		cpu.RegisterR3: 0,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	written, err := runtime.handleFileMethod("write", "([BII)I")
	if err != nil {
		t.Fatal(err)
	}
	if written != 3 {
		t.Fatalf("File.write = %d, want 3", written)
	}
	size, err := runtime.handleFileMethod("sizeOf", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if size != 3 {
		t.Fatalf("File.sizeOf = %d, want 3", size)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleFileMethod("seek", "(I)V"); err != nil {
		t.Fatal(err)
	}
	target, err := runtime.newJavaByteArray(make([]byte, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, target); err != nil {
		t.Fatal(err)
	}
	read, err := runtime.handleFileMethod("read", "([BII)I")
	if err != nil {
		t.Fatal(err)
	}
	if read != 3 {
		t.Fatalf("File.read = %d, want 3", read)
	}
	data, err := runtime.readJavaByteArray(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte{1, 2, 3}) {
		t.Fatalf("File.read bytes = %v", data)
	}
}

func TestKTFFileReadOnlyOpenRequiresExistingFile(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	file, err := runtime.newHostJavaObject("org/kwis/msp/io/File")
	if err != nil {
		t.Fatal(err)
	}
	filename, err := runtime.newJavaString("missing.dat")
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: file,
		cpu.RegisterR2: filename,
		cpu.RegisterR3: ktfFileReadOnly,
	} {
		if err := runtime.cpu.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	_, err = runtime.handleFileMethod(
		"<init>",
		"(Ljava/lang/String;I)V",
	)
	var unhandled *ktfUnhandledJavaException
	if !errors.As(err, &unhandled) ||
		unhandled.name != "java/io/IOException" {
		t.Fatalf("open missing read-only file error = %v", err)
	}
	if runtime.files[file] != nil {
		t.Fatalf("missing read-only file was opened: %#v", runtime.files[file])
	}

	runtime.fileData["/missing.dat"] = []byte{}
	if _, err := runtime.handleFileMethod(
		"<init>",
		"(Ljava/lang/String;I)V",
	); err != nil {
		t.Fatal(err)
	}
	if opened := runtime.files[file]; opened == nil ||
		opened.name != "/missing.dat" ||
		opened.mode != ktfFileReadOnly {
		t.Fatalf("opened read-only file = %#v", opened)
	}
}

func TestKTFDataBaseHonorsCreateFlag(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	name, err := runtime.newJavaString("save")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, name); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, 64); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 0); err != nil {
		t.Fatal(err)
	}
	database, err := runtime.handleDataBaseMethod(
		"openDataBase",
		"(Ljava/lang/String;IZ)Lorg/kwis/msp/db/DataBase;",
	)
	if err == nil ||
		!strings.Contains(err.Error(), "org/kwis/msp/db/DataBaseException") {
		t.Fatalf(
			"open missing database without create = 0x%08x, err=%v, store=%v",
			database,
			err,
			runtime.databaseStores["save"],
		)
	}
	if !strings.Contains(err.Error(), "detail=0x1000") {
		t.Fatalf("missing database exception has no guest object: %v", err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 1); err != nil {
		t.Fatal(err)
	}
	database, err = runtime.handleDataBaseMethod(
		"openDataBase",
		"(Ljava/lang/String;IZ)Lorg/kwis/msp/db/DataBase;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if database == 0 || runtime.databaseStores["save"] == nil {
		t.Fatalf(
			"create database = 0x%08x, store=%v",
			database,
			runtime.databaseStores["save"],
		)
	}
}

func TestKTFCollectionsReturnStoredObjectsAndEnumeration(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := runtime.newHostJavaObject("java/util/Vector")
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtime.newJavaString("first")
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := runtime.newJavaString("inserted")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, vector); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleVectorMethod("<init>", "(II)V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, first); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleVectorMethod(
		"addElement",
		"(Ljava/lang/Object;)V",
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, inserted); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleVectorMethod(
		"insertElementAt",
		"(Ljava/lang/Object;I)V",
	); err != nil {
		t.Fatal(err)
	}
	if got := runtime.vectors[vector]; !slices.Equal(
		got,
		[]uint32{inserted, first},
	) {
		t.Fatalf("Vector insertion = %08x", got)
	}

	table, err := runtime.newHostJavaObject("java/util/Hashtable")
	if err != nil {
		t.Fatal(err)
	}
	key, err := runtime.newJavaString("key")
	if err != nil {
		t.Fatal(err)
	}
	value, err := runtime.newJavaString("value")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, table); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleHashtableMethod("<init>", "()V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, key); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, value); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleHashtableMethod(
		"put",
		"(Ljava/lang/Object;Ljava/lang/Object;)Ljava/lang/Object;",
	); err != nil {
		t.Fatal(err)
	}
	got, err := runtime.handleHashtableMethod(
		"get",
		"(Ljava/lang/Object;)Ljava/lang/Object;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != value {
		t.Fatalf("Hashtable.get = 0x%08x, want 0x%08x", got, value)
	}
	enumeration, err := runtime.handleHashtableMethod(
		"keys",
		"()Ljava/util/Enumeration;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if enumeration == 0 {
		t.Fatal("Hashtable.keys returned null")
	}
	methodAddress, err := runtime.resolveJavaMethod(
		enumeration,
		"hasMoreElements",
		"()Z",
	)
	if err != nil || methodAddress == 0 {
		t.Fatalf(
			"resolve Enumeration method through instance = 0x%08x, %v",
			methodAddress,
			err,
		)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, enumeration); err != nil {
		t.Fatal(err)
	}
	more, err := runtime.handleEnumerationMethod("hasMoreElements", "()Z")
	if err != nil {
		t.Fatal(err)
	}
	if more != 1 {
		t.Fatalf("Enumeration.hasMoreElements = %d", more)
	}
	gotKey, err := runtime.handleEnumerationMethod(
		"nextElement",
		"()Ljava/lang/Object;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != key {
		t.Fatalf("Enumeration.nextElement = 0x%08x, want 0x%08x", gotKey, key)
	}
}

func TestKTFRareJavaAndMediaCompatibilityMethods(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.jvmContext, err = runtime.allocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}

	card, err := runtime.newHostJavaObject("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, card); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, ^uint32(4)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 37); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfHostJavaMethod(
		"org/kwis/msp/lcdui/Card",
		"move",
		"(II)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	x, err := ktfHostJavaMethod(
		"org/kwis/msp/lcdui/Card",
		"getX",
		"()I",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	y, err := ktfHostJavaMethod(
		"org/kwis/msp/lcdui/Card",
		"getY",
		"()I",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if int32(x) != -5 || y != 37 {
		t.Fatalf("Card position = (%d,%d), want (-5,37)", int32(x), int32(y))
	}

	source, err := runtime.newHostJavaObject("java/io/InputStream")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := runtime.newHostJavaObject("java/io/InputStreamReader")
	if err != nil {
		t.Fatal(err)
	}
	runtime.inputStreams[source] = &ktfInputStream{data: []byte("ABC")}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, reader); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, source); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleInputStreamReaderMethod(
		"<init>",
		"(Ljava/io/InputStream;)V",
	); err != nil {
		t.Fatal(err)
	}
	characters, err := runtime.newJavaArray("[C", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, characters); err != nil {
		t.Fatal(err)
	}
	read, err := runtime.handleInputStreamReaderMethod("read", "([C)I")
	if err != nil {
		t.Fatal(err)
	}
	characterFields, err := runtime.readU32(characters)
	if err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, 6)
	if err := runtime.cpu.ReadMemory(characterFields+8, encoded); err != nil {
		t.Fatal(err)
	}
	if read != 3 ||
		binary.LittleEndian.Uint16(encoded[0:2]) != 'A' ||
		binary.LittleEndian.Uint16(encoded[2:4]) != 'B' ||
		binary.LittleEndian.Uint16(encoded[4:6]) != 'C' {
		t.Fatalf("Reader.read(char[]) = %d, %x", read, encoded)
	}

	number, err := runtime.newJavaString("-9223372036854775808")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, number); err != nil {
		t.Fatal(err)
	}
	low, err := runtime.handleLongMethod(
		"parseLong",
		"(Ljava/lang/String;)J",
	)
	if err != nil {
		t.Fatal(err)
	}
	if low != 0 || runtime.javaReturnHigh != 0x80000000 {
		t.Fatalf(
			"Long.parseLong = 0x%08x%08x, want 0x8000000000000000",
			runtime.javaReturnHigh,
			low,
		)
	}

	throwable, err := runtime.newHostJavaObject("java/lang/Throwable")
	if err != nil {
		t.Fatal(err)
	}
	message, err := runtime.newJavaString("boom")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, throwable); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, message); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleThrowableMethod(
		"<init>",
		"(Ljava/lang/String;)V",
	); err != nil {
		t.Fatal(err)
	}
	gotMessage, err := runtime.handleThrowableMethod(
		"getMessage",
		"()Ljava/lang/String;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotMessage != message {
		t.Fatalf(
			"Throwable.getMessage = 0x%08x, want 0x%08x",
			gotMessage,
			message,
		)
	}
	if _, err := runtime.handleThrowableMethod(
		"printStackTrace",
		"()V",
	); err != nil {
		t.Fatal(err)
	}
	if trace := runtime.hostTrace[len(runtime.hostTrace)-1]; !strings.Contains(
		trace,
		`java_stack_trace:`,
	) || !strings.Contains(trace, `message="boom"`) {
		t.Fatalf("Throwable.printStackTrace trace = %q", trace)
	}

	clip, err := runtime.newHostJavaObject("org/kwis/msp/media/Clip")
	if err != nil {
		t.Fatal(err)
	}
	sourceData, err := runtime.newJavaByteArray([]byte{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, clip); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, sourceData); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 1); err != nil {
		t.Fatal(err)
	}
	stack, err := runtime.allocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{2}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	written, err := runtime.handleMediaMethod("putData", "([BII)I")
	if err != nil {
		t.Fatal(err)
	}
	targetData, err := runtime.newJavaByteArray(make([]byte, 4))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR2, targetData); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR3, 1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{3}); err != nil {
		t.Fatal(err)
	}
	copied, err := runtime.handleMediaMethod("getData", "([BII)I")
	if err != nil {
		t.Fatal(err)
	}
	gotData, err := runtime.readJavaByteArray(targetData)
	if err != nil {
		t.Fatal(err)
	}
	if written != 2 || copied != 2 ||
		!bytes.Equal(gotData, []byte{0, 2, 3, 0}) {
		t.Fatalf(
			"BaseClip put/get = written %d, copied %d, data %v",
			written,
			copied,
			gotData,
		)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, clip); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleMediaMethod("clearData", "()V"); err != nil {
		t.Fatal(err)
	}
	available, err := runtime.handleMediaMethod("availableDataSize", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if available != 0 {
		t.Fatalf("BaseClip availableDataSize after clear = %d", available)
	}
}

func TestKTFWIPICKernelGetDLLInterfaceRegistersUserMemory(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin64",
		BSSSize:    64,
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	name, err := runtime.allocateBytes(
		[]byte("MXUserMemInterf"),
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	returnMajor, err := runtime.allocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	returnMinor, err := runtime.allocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	stack := DefaultStackBase + 0x100
	if err := runtime.writeU32(stack, returnMinor); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{
		name,
		^uint32(0),
		^uint32(0),
		returnMajor,
	} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	address, err := ktfKernelGetDLLInterface(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if address == 0 || address != runtime.mxUserMemInterface {
		t.Fatalf(
			"MXUserMem interface = 0x%08x, cached 0x%08x",
			address,
			runtime.mxUserMemInterface,
		)
	}
	versions, err := runtime.readWords(returnMajor, 1)
	if err != nil {
		t.Fatal(err)
	}
	minor, err := runtime.readU32(returnMinor)
	if err != nil {
		t.Fatal(err)
	}
	if versions[0] != 1 || minor != 0 {
		t.Fatalf("MXUserMem interface version = %d.%d", versions[0], minor)
	}
	again, err := runtime.lookupInterface("MXUserMemInterf")
	if err != nil {
		t.Fatal(err)
	}
	if again != address {
		t.Fatalf(
			"repeated MXUserMem interface = 0x%08x, want 0x%08x",
			again,
			address,
		)
	}

	callbacks, err := runtime.readWords(address, 4)
	if err != nil {
		t.Fatal(err)
	}
	host, ok := runtime.hostCalls[callbacks[0]&^1]
	if !ok {
		t.Fatalf("MXUserMem add callback 0x%08x is not registered", callbacks[0])
	}
	const (
		regionBase = ktfImageBase + 8
		regionSize = 32
	)
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, regionBase); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, regionSize); err != nil {
		t.Fatal(err)
	}
	if value, err := host.handler(context.Background(), runtime); err != nil {
		t.Fatal(err)
	} else if value != 0 {
		t.Fatalf("MXUserMem registration = 0x%08x", value)
	}
	if !slices.Equal(
		runtime.incrementalMemory,
		[]ktfIncrementalMemoryRegion{{base: regionBase, size: regionSize}},
	) {
		t.Fatalf("incremental-memory regions = %+v", runtime.incrementalMemory)
	}

	allocate := runtime.hostCalls[callbacks[1]&^1]
	if allocate.handler == nil {
		t.Fatalf("MXUserMem allocate callback 0x%08x is not registered", callbacks[1])
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, regionBase); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 7); err != nil {
		t.Fatal(err)
	}
	allocation, err := allocate.handler(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if allocation != regionBase ||
		runtime.incrementalHeaps[regionBase].allocations[allocation] != 8 {
		t.Fatalf(
			"MXUserMem allocation = 0x%08x, heap=%+v",
			allocation,
			runtime.incrementalHeaps[regionBase],
		)
	}

	free := runtime.hostCalls[callbacks[3]&^1]
	if free.handler == nil {
		t.Fatalf("MXUserMem free callback 0x%08x is not registered", callbacks[3])
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, regionBase); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, allocation); err != nil {
		t.Fatal(err)
	}
	if value, err := free.handler(context.Background(), runtime); err != nil {
		t.Fatal(err)
	} else if value != 0 {
		t.Fatalf("MXUserMem free = 0x%08x", value)
	}
	if _, ok := runtime.incrementalHeaps[regionBase].allocations[allocation]; ok {
		t.Fatalf("freed MXUserMem allocation 0x%08x remains registered", allocation)
	}
}

func TestKTFWIPICKernelMemoryIDCopiesResourceToIndirectBuffer(t *testing.T) {
	resource := []byte{0x18, 0xba, 0x72, 0x00, 0xff}
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
		Resources: map[string][]byte{
			"assets/18.BAR": resource,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	name, err := runtime.allocateBytes([]byte("18.bar"), true)
	if err != nil {
		t.Fatal(err)
	}
	sizeAddress, err := runtime.allocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, name); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, sizeAddress); err != nil {
		t.Fatal(err)
	}
	resourceID, err := ktfGetResourceID(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	size, err := runtime.readU32(sizeAddress)
	if err != nil {
		t.Fatal(err)
	}
	if resourceID == 0 || size != uint32(len(resource)) {
		t.Fatalf("resource ID = %d, size = %d", resourceID, size)
	}

	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, size); err != nil {
		t.Fatal(err)
	}
	memoryID, err := ktfKernelAllocate(true)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	header, err := runtime.readWords(memoryID, 2)
	if err != nil {
		t.Fatal(err)
	}
	allocation := runtime.wipicMemory[memoryID]
	if allocation.data == 0 ||
		allocation.base != header[0] ||
		allocation.data != allocation.base+8 ||
		allocation.size != size ||
		header[1] != size {
		t.Fatalf(
			"WIPI-C memory ID 0x%08x header=%08x allocation=%+v",
			memoryID,
			header,
			allocation,
		)
	}
	before := make([]byte, len(resource))
	if err := runtime.cpu.ReadMemory(allocation.data, before); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, make([]byte, len(resource))) {
		t.Fatalf("calloc data before resource copy = %x", before)
	}

	for register, value := range []uint32{resourceID, memoryID, size} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := ktfGetResource(context.Background(), runtime); err != nil {
		t.Fatal(err)
	} else if result != 0 {
		t.Fatalf("MC_knlGetResource = 0x%08x", result)
	}
	got := make([]byte, len(resource))
	if err := runtime.cpu.ReadMemory(allocation.data, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, resource) {
		t.Fatalf("resource at indirect buffer = %x, want %x", got, resource)
	}

	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, memoryID); err != nil {
		t.Fatal(err)
	}
	freeResult, err := ktfKernelFree(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if freeResult != memoryID {
		t.Fatalf(
			"MC_knlFree residual r0 = 0x%08x, want memory ID 0x%08x",
			freeResult,
			memoryID,
		)
	}
	if _, ok := runtime.wipicMemory[memoryID]; ok {
		t.Fatalf("freed WIPI-C memory ID 0x%08x remains registered", memoryID)
	}

	guestHandle, err := runtime.heap.allocate(size+12, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(guestHandle, guestHandle+4); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{resourceID, guestHandle, size} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := ktfGetResource(context.Background(), runtime); err != nil {
		t.Fatal(err)
	} else if result != 0 {
		t.Fatalf("MC_knlGetResource guest indirect buffer = 0x%08x", result)
	}
	if head, err := runtime.readU32(guestHandle); err != nil {
		t.Fatal(err)
	} else if head != guestHandle+4 {
		t.Fatalf(
			"guest indirect buffer handle overwritten with 0x%08x",
			head,
		)
	}
	got = make([]byte, len(resource))
	if err := runtime.cpu.ReadMemory(guestHandle+12, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, resource) {
		t.Fatalf(
			"resource at guest indirect buffer = %x, want %x",
			got,
			resource,
		)
	}

	direct, err := runtime.heap.allocate(size, true)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{resourceID, direct, size} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := ktfGetResource(context.Background(), runtime); err != nil {
		t.Fatal(err)
	} else if result != 0 {
		t.Fatalf("MC_knlGetResource direct buffer = 0x%08x", result)
	}
	got = make([]byte, len(resource))
	if err := runtime.cpu.ReadMemory(direct, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, resource) {
		t.Fatalf("resource at direct buffer = %x, want %x", got, resource)
	}
}

func TestKTFWIPICKernelSprintkFormatsResourcePath(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	destination, err := runtime.heap.allocate(128, true)
	if err != nil {
		t.Fatal(err)
	}
	format, err := runtime.allocateBytes([]byte("data/%s.%04x:%d"), true)
	if err != nil {
		t.Fatal(err)
	}
	name, err := runtime.allocateBytes([]byte("IMG_GFONT_CHO1"), true)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{
		destination,
		format,
		name,
		0x2a,
	} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	stack, err := runtime.heap.allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{^uint32(6)}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	length, err := ktfKernelSprintk(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	const want = "data/IMG_GFONT_CHO1.002a:-7"
	got, err := runtime.readCString(destination, 128)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || length != uint32(len(want)) {
		t.Fatalf("sprintk = %q, %d; want %q, %d", got, length, want, len(want))
	}
}

func TestKTFWIPICKernelSystemPropertyRoundTrip(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	key, err := runtime.allocateBytes([]byte("PHONEMODEL"), true)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runtime.heap.allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{key, output, 32} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := ktfKernelGetSystemProperty(
		context.Background(),
		runtime,
	); err != nil || result != 0 {
		t.Fatalf("get system property result=%08x err=%v", result, err)
	}
	if value, err := runtime.readCString(output, 32); err != nil ||
		value != "LG-KH1300" {
		t.Fatalf("PHONEMODEL = %q, err=%v", value, err)
	}
	value, err := runtime.allocateBytes([]byte("test-value"), true)
	if err != nil {
		t.Fatal(err)
	}
	for register, word := range []uint32{key, value} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			word,
		); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := ktfKernelSetSystemProperty(
		context.Background(),
		runtime,
	); err != nil || result != 0 {
		t.Fatalf("set system property result=%08x err=%v", result, err)
	}
	if got, ok := runtime.wipicSystemProperty("phonemodel"); !ok ||
		got != "test-value" {
		t.Fatalf("updated PHONEMODEL = %q, %t", got, ok)
	}
}

func TestKTFWIPICFileRoundTrip(t *testing.T) {
	if got := ktfWIPICHandler(ktfWIPICMasterFS, 15); reflect.ValueOf(got).Pointer() !=
		reflect.ValueOf(ktfWIPICFileTell).Pointer() {
		t.Fatal("WIPI-C filesystem slot 15 is not MC_fsTell")
	}
	if got := ktfWIPICHandler(ktfWIPICMasterFS, 16); reflect.ValueOf(got).Pointer() !=
		reflect.ValueOf(ktfWIPICFileIsExist).Pointer() {
		t.Fatal("WIPI-C filesystem slot 16 is not MC_fsIsExist")
	}
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	name, err := runtime.allocateBytes([]byte("save.dat"), true)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{
		name,
		ktfWIPICFileReadWrite,
		1,
	} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	handle, err := ktfWIPICFileOpen(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if handle == 0 || int32(handle) < 0 {
		t.Fatalf("MC_fsOpen = 0x%08x", handle)
	}

	input, err := runtime.allocateBytes([]byte{1, 2, 3, 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{handle, input, 4} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if written, err := ktfWIPICFileWrite(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	} else if written != 4 {
		t.Fatalf("MC_fsWrite = %d", written)
	}

	for register, value := range []uint32{handle, 0, 0} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if position, err := ktfWIPICFileSeek(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	} else if position != 0 {
		t.Fatalf("MC_fsSeek = %d", position)
	}

	output, err := runtime.allocateBytes(make([]byte, 4), false)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{handle, output, 4} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if read, err := ktfWIPICFileRead(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	} else if read != 4 {
		t.Fatalf("MC_fsRead = %d", read)
	}
	got := make([]byte, 4)
	if err := runtime.cpu.ReadMemory(output, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("MC_fsRead bytes = %v", got)
	}

	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, handle); err != nil {
		t.Fatal(err)
	}
	if result, err := ktfWIPICFileClose(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	} else if result != 0 {
		t.Fatalf("MC_fsClose = 0x%08x", result)
	}
}

func TestKTFWIPICDirectoryOperationsUseSharedStorage(t *testing.T) {
	for slot, want := range map[int]ktfHostHandler{
		8: ktfWIPICFileMakeDirectory,
		9: ktfWIPICFileRemoveDirectory,
	} {
		if got := ktfWIPICHandler(ktfWIPICMasterFS, slot); reflect.ValueOf(
			got,
		).Pointer() != reflect.ValueOf(want).Pointer() {
			t.Fatalf("WIPI-C filesystem slot %d has the wrong handler", slot)
		}
	}

	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	writeParameters := func(values ...uint32) {
		t.Helper()
		for index, value := range values {
			if err := runtime.cpu.WriteRegister(
				cpu.RegisterR0+uint32(index),
				value,
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	directory, err := runtime.allocateBytes([]byte("/saves"), true)
	if err != nil {
		t.Fatal(err)
	}
	writeParameters(directory)
	if result, err := ktfWIPICFileMakeDirectory(
		context.Background(),
		runtime,
	); err != nil || result != 0 {
		t.Fatalf("MC_fsMakeDirectory result=%08x err=%v", result, err)
	}
	writeParameters(directory)
	if result, err := ktfWIPICFileIsExist(
		context.Background(),
		runtime,
	); err != nil || result != 0 {
		t.Fatalf("MC_fsIsExist directory result=%08x err=%v", result, err)
	}
	if err := runtime.services.Storage.WriteFile(
		shared.NamespacePrivate,
		"/saves/state.dat",
		[]byte("state"),
	); err != nil {
		t.Fatal(err)
	}

	output, err := runtime.allocateBytes(make([]byte, 64), false)
	if err != nil {
		t.Fatal(err)
	}
	writeParameters(directory, output, 64)
	if result, err := ktfWIPICFileList(
		context.Background(),
		runtime,
	); err != nil || result != 0 {
		t.Fatalf("MC_fsList result=%08x err=%v", result, err)
	}
	listed := make([]byte, len("state.dat")+2)
	if err := runtime.cpu.ReadMemory(output, listed); err != nil {
		t.Fatal(err)
	}
	if want := append([]byte("state.dat"), 0, 0); !bytes.Equal(listed, want) {
		t.Fatalf("MC_fsList bytes = %v; want %v", listed, want)
	}
	writeParameters(directory)
	if count, err := ktfWIPICFileGetCounts(
		context.Background(),
		runtime,
	); err != nil || count != 1 {
		t.Fatalf("MC_fsGetCounts count=%d err=%v", count, err)
	}
	writeParameters(directory)
	if result, err := ktfWIPICFileRemoveDirectory(
		context.Background(),
		runtime,
	); err != nil || result != ktfWIPICError {
		t.Fatalf("remove nonempty directory result=%08x err=%v", result, err)
	}
	if err := runtime.services.Storage.Delete(
		shared.NamespacePrivate,
		"/saves/state.dat",
	); err != nil {
		t.Fatal(err)
	}
	writeParameters(directory)
	if result, err := ktfWIPICFileRemoveDirectory(
		context.Background(),
		runtime,
	); err != nil || result != 0 {
		t.Fatalf("remove empty directory result=%08x err=%v", result, err)
	}
}

func TestKTFMetadataFixedWidthRoundTripPreservesIncrementalMemory(t *testing.T) {
	input := ktfMetadataSnapshot{
		IncrementalMemory: []ktfIncrementalMemoryRegionSnapshot{
			{Base: 0x1000, Size: 0x100},
			{Base: 0x2000, Size: 0x200},
		},
		ImageServices: map[uint32]shared.ServiceID{
			9: shared.ServiceID(0x100000002),
			2: shared.ServiceID(0x100000001),
		},
		TaskCursor: 1,
	}
	encoded, err := shared.MarshalStateComponent(input)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ktfMetadataSnapshot
	if err := shared.UnmarshalStateComponent(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, input) {
		t.Fatalf("KTF metadata round-trip:\ngot  %+v\nwant %+v", decoded, input)
	}
	heaps := []ktfIncrementalHeapSnapshot{
		{Base: 0x1000, Size: 0x100},
		{Base: 0x2000, Size: 0x200},
	}
	if err := validateKTFIncrementalMemory(
		decoded.IncrementalMemory,
		heaps,
	); err != nil {
		t.Fatal(err)
	}
	heaps[1].Size++
	if err := validateKTFIncrementalMemory(
		decoded.IncrementalMemory,
		heaps,
	); err == nil {
		t.Fatal("incremental-memory validator accepted mismatched heap geometry")
	}
}

func TestKTFWIPICScreenFramebufferLayoutAndFill(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 8, 6))

	framebufferAddress, err := runtime.ensureWIPICScreenFramebuffer()
	if err != nil {
		t.Fatal(err)
	}
	framebuffer := runtime.wipicFramebuffers[framebufferAddress]
	objectBody, err := runtime.readU32(framebufferAddress)
	if err != nil {
		t.Fatal(err)
	}
	body, err := runtime.readWords(objectBody, 7)
	if err != nil {
		t.Fatal(err)
	}
	pixelHeader, err := runtime.readU32(body[6])
	if err != nil {
		t.Fatal(err)
	}
	if framebuffer == nil ||
		objectBody != framebuffer.body ||
		body[0] != framebuffer.body ||
		body[2] != 8 ||
		body[3] != 6 ||
		body[4] != 16 ||
		body[5] != 16 ||
		pixelHeader+8 != framebuffer.pixels {
		t.Fatalf(
			"WIPI-C framebuffer object=0x%08x body=%08x state=%+v",
			framebufferAddress,
			body,
			framebuffer,
		)
	}

	graphicsContext, err := runtime.allocateWords(15)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, graphicsContext); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICGraphicsInitContext(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeU32(graphicsContext+20, 0xf800); err != nil {
		t.Fatal(err)
	}
	stack, err := runtime.allocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{2, graphicsContext}); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{framebufferAddress, 2, 1, 3} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICGraphicsFillRect(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.presentWIPICFramebuffer(framebufferAddress); err != nil {
		t.Fatal(err)
	}
	if got := runtime.frame.RGBAAt(2, 1); got != (color.RGBA{
		R: 0xff,
		A: 0xff,
	}) {
		t.Fatalf("filled RGB565 pixel = %#v", got)
	}
	if got := runtime.frame.RGBAAt(1, 1); got != (color.RGBA{A: 0xff}) {
		t.Fatalf("pixel outside fill = %#v", got)
	}
	if runtime.presentCount != 1 {
		t.Fatalf("WIPI-C present count = %d", runtime.presentCount)
	}
}

func TestKTFWIPICOffscreenFramebufferLifecycle(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, 32); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, 24); err != nil {
		t.Fatal(err)
	}
	object, err := ktfWIPICGraphicsCreateOffscreenFramebuffer(
		context.Background(),
		runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	framebuffer := runtime.wipicFramebuffers[object]
	if framebuffer == nil || framebuffer.screen ||
		framebuffer.width != 32 || framebuffer.height != 24 {
		t.Fatalf("offscreen framebuffer = %+v", framebuffer)
	}
	screen, err := runtime.ensureWIPICScreenFramebuffer()
	if err != nil {
		t.Fatal(err)
	}
	contextAddress, err := runtime.heap.allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(contextAddress, []uint32{
		0, 0, 32, 24, 1, 0xffff,
	}); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{object, 0, 0, 32} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	stack, err := runtime.heap.allocate(20, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{
		24, contextAddress, 0, 0, 0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICGraphicsFillRect(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.presentWIPICFramebuffer(object); err != nil {
		t.Fatal(err)
	}
	screenSurface := runtime.wipicSurfaceServices[screen]
	if got := runtime.services.Graphics.Screen(); got != screenSurface {
		t.Fatalf("presented surface = %s, want screen %s", got, screenSurface)
	}
	if got := runtime.frame.RGBAAt(0, 0); got != (color.RGBA{
		R: 0xff,
		G: 0xff,
		B: 0xff,
		A: 0xff,
	}) {
		t.Fatalf("presented offscreen pixel = %#v", got)
	}
	for register, value := range []uint32{screen, 0, 0, 32} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.writeWords(stack, []uint32{
		24, object, 0, 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICGraphicsCopyFramebuffer(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	var copied [2]byte
	if err := runtime.cpu.ReadMemory(
		runtime.wipicFramebuffers[screen].pixels,
		copied[:],
	); err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(copied[:]) != 0xffff {
		t.Fatalf(
			"copied offscreen pixel = %04x",
			binary.LittleEndian.Uint16(copied[:]),
		)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, object); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICGraphicsDestroyOffscreenFramebuffer(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	if runtime.wipicFramebuffers[object] != nil {
		t.Fatalf("destroyed offscreen framebuffer 0x%08x remains registered", object)
	}
}

func TestKTFWIPICImageDecodeAndProperties(t *testing.T) {
	var encoded bytes.Buffer
	source := image.NewRGBA(image.Rect(0, 0, 2, 1))
	source.SetRGBA(0, 0, color.RGBA{R: 0xff, A: 0xff})
	source.SetRGBA(1, 0, color.RGBA{G: 0xff, A: 0xff})
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(
		cpu.RegisterR0,
		uint32(encoded.Len()),
	); err != nil {
		t.Fatal(err)
	}
	memoryID, err := ktfKernelAllocate(true)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(
		runtime.wipicMemory[memoryID].data,
		encoded.Bytes(),
	); err != nil {
		t.Fatal(err)
	}
	output, err := runtime.allocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{
		output,
		memoryID,
		0,
		uint32(encoded.Len()),
	} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := ktfWIPICGraphicsCreateImage(
		context.Background(),
		runtime,
	); err != nil || result != 1 {
		t.Fatalf("create image result=%08x err=%v", result, err)
	}
	object, err := runtime.readU32(output)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range map[uint32]uint32{4: 2, 5: 1, 6: 16} {
		if err := runtime.cpu.WriteRegister(cpu.RegisterR0, object); err != nil {
			t.Fatal(err)
		}
		if err := runtime.cpu.WriteRegister(cpu.RegisterR1, index); err != nil {
			t.Fatal(err)
		}
		got, err := ktfWIPICGraphicsGetImageProperty(
			context.Background(),
			runtime,
		)
		if err != nil || got != want {
			t.Fatalf("image property %d = %d, err=%v; want %d", index, got, err, want)
		}
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, object); err != nil {
		t.Fatal(err)
	}
	framebufferObject, err := ktfWIPICGraphicsGetImageFramebuffer(
		context.Background(),
		runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	var pixel [2]byte
	if err := runtime.cpu.ReadMemory(
		runtime.wipicFramebuffers[framebufferObject].pixels,
		pixel[:],
	); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixel[:]); got != 0xf800 {
		t.Fatalf("decoded red pixel = %04x", got)
	}
}

func TestKTFWIPICImageDecodesBMP(t *testing.T) {
	encoded := make([]byte, 66)
	copy(encoded[:2], "BM")
	binary.LittleEndian.PutUint32(encoded[2:6], uint32(len(encoded)))
	binary.LittleEndian.PutUint32(encoded[10:14], 62)
	binary.LittleEndian.PutUint32(encoded[14:18], 40)
	binary.LittleEndian.PutUint32(encoded[18:22], 2)
	binary.LittleEndian.PutUint32(encoded[22:26], 1)
	binary.LittleEndian.PutUint16(encoded[26:28], 1)
	binary.LittleEndian.PutUint16(encoded[28:30], 4)
	binary.LittleEndian.PutUint32(encoded[34:38], 4)
	binary.LittleEndian.PutUint32(encoded[46:50], 2)
	copy(encoded[54:62], []byte{
		0x00, 0x00, 0xff,
		0x00,
		0x00, 0xff, 0x00,
		0x00,
	})
	copy(encoded[62:], []byte{0x01, 0x00, 0x00, 0x00})

	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(
		cpu.RegisterR0,
		uint32(len(encoded)),
	); err != nil {
		t.Fatal(err)
	}
	memoryID, err := ktfKernelAllocate(true)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteMemory(
		runtime.wipicMemory[memoryID].data,
		encoded,
	); err != nil {
		t.Fatal(err)
	}
	output, err := runtime.allocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{
		output,
		memoryID,
		0,
		uint32(len(encoded)),
	} {
		if err := runtime.cpu.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := ktfWIPICGraphicsCreateImage(
		context.Background(),
		runtime,
	); err != nil || result != 1 {
		t.Fatalf("create BMP image result=%08x err=%v", result, err)
	}
	object, err := runtime.readU32(output)
	if err != nil {
		t.Fatal(err)
	}
	body, err := runtime.readU32(object)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.wipicImages[object].body; body != got {
		t.Fatalf("BMP image body = %08x; want %08x", body, got)
	}
	framebufferObject := runtime.wipicImages[object].framebuffer
	if got, err := runtime.readU32(body + 8); err != nil || got != framebufferObject {
		t.Fatalf(
			"BMP image nested framebuffer = %08x, err=%v; want %08x",
			got,
			err,
			framebufferObject,
		)
	}
	if got, err := runtime.readU32(body + 12); err != nil || got != 0 {
		t.Fatalf("BMP image nested mask = %08x, err=%v; want 00000000", got, err)
	}
	framebuffer := runtime.wipicFramebuffers[framebufferObject]
	var pixels [4]byte
	if err := runtime.cpu.ReadMemory(framebuffer.pixels, pixels[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixels[:2]); got != 0xf800 {
		t.Fatalf("decoded BMP red pixel = %04x", got)
	}
	if got := binary.LittleEndian.Uint16(pixels[2:]); got != 0x07e0 {
		t.Fatalf("decoded BMP green pixel = %04x", got)
	}
}

func TestKTFWIPICTimerFiresAndReusesCompletedTask(t *testing.T) {
	runtime, err := newKTFRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.cpu.Close()
	if err := runtime.mapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	timerAddress, err := runtime.allocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	callback := ktfImageBase | 1
	if err := runtime.cpu.WriteRegister(cpu.RegisterR0, timerAddress); err != nil {
		t.Fatal(err)
	}
	if err := runtime.cpu.WriteRegister(cpu.RegisterR1, callback); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfKernelDefineTimer(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}

	const parameter = uint32(0x12345678)
	setTimer := func(timeout uint32) {
		t.Helper()
		for register, value := range []uint32{
			timerAddress,
			timeout,
			0,
			parameter,
		} {
			if err := runtime.cpu.WriteRegister(
				cpu.RegisterR0+uint32(register),
				value,
			); err != nil {
				t.Fatal(err)
			}
		}
		if result, err := ktfKernelSetTimer(
			context.Background(),
			runtime,
		); err != nil {
			t.Fatal(err)
		} else if result != 0 {
			t.Fatalf("MC_knlSetTimer = 0x%08x", result)
		}
	}

	runtime.tickMS = 100
	setTimer(25)
	runtime.tickMS = 124
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.tasks) != 0 {
		t.Fatalf("timer fired before deadline: %d tasks", len(runtime.tasks))
	}
	runtime.tickMS = 125
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.tasks) != 1 {
		t.Fatalf("timer produced %d tasks, want 1", len(runtime.tasks))
	}
	if err := runtime.cpu.RestoreContext(runtime.tasks[0].context); err != nil {
		t.Fatal(err)
	}
	gotTimer, err := runtime.cpu.ReadRegister(cpu.RegisterR0)
	if err != nil {
		t.Fatal(err)
	}
	gotParameter, err := runtime.cpu.ReadRegister(cpu.RegisterR1)
	if err != nil {
		t.Fatal(err)
	}
	gotPC, err := runtime.cpu.ReadRegister(cpu.RegisterPC)
	if err != nil {
		t.Fatal(err)
	}
	if gotTimer != timerAddress ||
		gotParameter != parameter ||
		gotPC != callback&^1 {
		t.Fatalf(
			"timer callback registers: r0=0x%08x r1=0x%08x pc=0x%08x",
			gotTimer,
			gotParameter,
			gotPC,
		)
	}

	runtime.tasks[0].done = true
	runtime.tickMS = 200
	setTimer(1)
	runtime.tickMS = 201
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.tasks) != 1 || runtime.tasks[0].done {
		t.Fatalf(
			"timer task was not reused: count=%d done=%t",
			len(runtime.tasks),
			runtime.tasks[0].done,
		)
	}

	runtime.tasks = make([]*ktfTask, ktfMaxTasks)
	for index := range runtime.tasks {
		runtime.tasks[index] = &ktfTask{}
	}
	runtime.tickMS = 300
	setTimer(1)
	runtime.tickMS = 301
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if !runtime.wipicTimers[timerAddress].active {
		t.Fatal("timer was consumed while the task pool was full")
	}

	const reusableTask = 7
	runtime.tasks[reusableTask].done = true
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if runtime.wipicTimers[timerAddress].active {
		t.Fatal("timer remained active after a task slot became available")
	}
	if runtime.tasks[reusableTask].done {
		t.Fatal("timer callback did not reuse the completed task slot")
	}

	runtime.tasks = nil
	runtime.wipicTimers = map[uint32]*ktfWIPICTimer{
		0x10003000: {
			callback:  callback,
			parameter: 1,
			deadline:  400,
			active:    true,
		},
		0x10004000: {
			callback:  callback,
			parameter: 2,
			deadline:  400,
			active:    true,
		},
	}
	runtime.tickMS = 400
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.tasks) != 1 || !runtime.tasks[0].wipicTimer {
		t.Fatalf("first serialized timer tasks = %+v", runtime.tasks)
	}
	if runtime.wipicTimers[0x10003000].active ||
		!runtime.wipicTimers[0x10004000].active {
		t.Fatalf("serialized timer states = %+v", runtime.wipicTimers)
	}
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.tasks) != 1 ||
		!runtime.wipicTimers[0x10004000].active {
		t.Fatal("second timer ran while the first callback was live")
	}
	runtime.tasks[0].done = true
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.tasks) != 1 ||
		runtime.tasks[0].done ||
		!runtime.tasks[0].wipicTimer ||
		runtime.wipicTimers[0x10004000].active {
		t.Fatal("second timer did not run after the first callback completed")
	}
}
