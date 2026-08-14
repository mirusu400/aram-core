package ktf

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestKTFRuntimeMapsAndCallsClientEntry(t *testing.T) {
	client := syntheticKTFClient()
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin4096",
		BSSSize:    4096,
		Client:     client,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	result, value, err := runtime.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if value != ImageBase+0x100 {
		t.Fatalf("bootstrap result = 0x%08x", value)
	}
	if result.Reason != cpu.StopBreakpoint ||
		result.PC != ktfReturnSentinel+2 {
		t.Fatalf("execution result = %+v", result)
	}
	if runtime.Exe.Name != "SyntheticKTF" ||
		runtime.Exe.InterfaceInit != (ImageBase+0x20)|1 ||
		runtime.Exe.GetClass != (ImageBase+0x20)|1 {
		t.Fatalf("executable = %+v", runtime.Exe)
	}
	var bss [4]byte
	if err := runtime.CPU.ReadMemory(ImageBase+uint32(len(client)), bss[:]); err != nil {
		t.Fatal(err)
	}
	if bss != [4]byte{} {
		t.Fatalf("BSS is not zero: %x", bss)
	}
}

func TestKTFRuntimeInitializesCompleteJavaEnvironment(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin4096",
		BSSSize:    4096,
		Client:     syntheticKTFClient(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}

	contextAddress, err := runtime.ReadU32(runtime.javaEnvironment)
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
	got, err := runtime.ReadWords(contextAddress+0x24, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []uint32{2, 0x12345678}) {
		t.Fatalf("Java environment native fields = %08x", got)
	}
	frame, err := runtime.ReadU32(contextAddress + 8*4)
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
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
	defer runtime.CPU.Close()
	store := runtime.DatabaseStores["save"]
	if store == nil {
		store = runtime.DatabaseStores["SAVE"]
	}
	if store == nil || store.RecordSize != 3 ||
		len(store.Records) != 2 ||
		!bytes.Equal(store.Records[0], []byte{1, 2, 3}) ||
		!bytes.Equal(store.Records[1], []byte{4, 5, 6}) {
		t.Fatalf("packaged database = %#v", store)
	}
}

func TestKTFRuntimeLoadsPackagedPrivateFiles(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
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
	defer runtime.CPU.Close()
	if !bytes.Equal(runtime.FileData["/config.do"], []byte{1, 2, 3}) {
		t.Fatalf("private file = %v", runtime.FileData)
	}
	if _, ok := runtime.FileData["/ignored.do"]; ok {
		t.Fatalf("loaded file outside package root: %v", runtime.FileData)
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
	binary.LittleEndian.PutUint32(client[4:8], ImageBase+0x100)
	copy(client[0x20:], []byte{
		0x00, 0x20, // movs r0, #0
		0x70, 0x47, // bx lr
	})
	binary.LittleEndian.PutUint32(client[0x100:], ImageBase+0x140)
	binary.LittleEndian.PutUint32(client[0x104:], ImageBase+0x180)
	binary.LittleEndian.PutUint32(client[0x114:], (ImageBase+0x20)|1)
	binary.LittleEndian.PutUint32(client[0x140:], ImageBase+0x160)
	binary.LittleEndian.PutUint32(client[0x168:], (ImageBase+0x20)|1)
	binary.LittleEndian.PutUint32(client[0x170:], (ImageBase+0x20)|1)
	copy(client[0x180:], "SyntheticKTF\x00")
	return client
}

func TestKTFTaskSliceRunsToReturnSentinel(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client: []byte{
			0x07, 0x20, // movs r0, #7
			0x70, 0x47, // bx lr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.NewTask(ImageBase|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	runtime.Tasks = append(runtime.Tasks, task)
	result := runtime.RunTaskSlice(context.Background(), 16)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != cpu.StopExited || !task.Done {
		t.Fatalf("KTF task result = %+v, done=%t", result, task.Done)
	}
}

func TestKTFJletNotifyDestroyedStopsNestedAndScheduledExecution(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	const (
		display = uint32(0x10001000)
		card    = uint32(0x10002000)
		jlet    = uint32(0x10003000)
	)
	runtime.DefaultDisplay = display
	runtime.DisplayCards[display] = card
	if !runtime.CanAwaitEvents() {
		t.Fatal("docked card could not receive events before Jlet termination")
	}
	notify := runtime.RegisterHostCall(
		"java.method.org/kwis/msp/lcdui/Jlet.notifyDestroyed()V",
		HostJavaMethod(
			"org/kwis/msp/lcdui/Jlet",
			"notifyDestroyed",
			"()V",
		),
	)
	result, _, err := runtime.call(
		context.Background(),
		notify,
		[]uint32{0, jlet},
		16,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reason != cpu.StopExited {
		t.Fatalf("nested notifyDestroyed result = %+v, want exited", result)
	}
	if !runtime.terminationRequested || runtime.CanAwaitEvents() {
		t.Fatalf(
			"Jlet termination state: requested=%t can_await=%t",
			runtime.terminationRequested,
			runtime.CanAwaitEvents(),
		)
	}

	task, err := runtime.NewTask(ImageBase|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	runtime.Tasks = append(runtime.Tasks, task)
	result = runtime.RunTaskSlice(context.Background(), 16)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != cpu.StopExited || !task.Done {
		t.Fatalf(
			"terminated Jlet task result = %+v, done=%t",
			result,
			task.Done,
		)
	}
	if !slices.Contains(
		runtime.HostTrace,
		"java_lifecycle:notifyDestroyed:instance=0x10003000",
	) {
		t.Fatalf("Jlet lifecycle trace = %v", runtime.HostTrace)
	}
}

func TestKTFTaskSliceScopesPendingJavaMethodPerTask(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	var observed []string
	newProbe := func(name string) uint32 {
		return runtime.RegisterHostCall(
			"test.task_java_method."+name,
			func(_ context.Context, runtime *Runtime) (uint32, error) {
				observed = append(observed, runtime.LastJavaMethod)
				return 0, nil
			},
		)
	}
	first, err := runtime.NewTask(newProbe("first"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.NewTask(newProbe("second"), nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	first.LastJavaMethod = "example/First.pending()V"
	second.LastJavaMethod = "example/Second.pending()V"
	runtime.Tasks = []*Task{first, second}
	runtime.LastJavaMethod = "ambient"

	for !first.Done || !second.Done {
		result := runtime.RunTaskSlice(context.Background(), 16)
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
	if runtime.LastJavaMethod != "ambient" {
		t.Fatalf("ambient Java method = %q", runtime.LastJavaMethod)
	}
}

func TestKTFFrameDurationTracksSixtyHertz(t *testing.T) {
	got := 60 * FrameDuration
	delta := got - time.Second
	if delta < 0 {
		delta = -delta
	}
	if delta > time.Microsecond {
		t.Fatalf("60 KTF frames advance %s, want approximately 1s", got)
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	var observed string
	procedure := runtime.RegisterHostCall(
		"test.nested_java_method",
		func(_ context.Context, runtime *Runtime) (uint32, error) {
			observed = runtime.LastJavaMethod
			runtime.LastJavaMethod = "example/Inner.pending()V"
			return 0, nil
		},
	)
	runtime.LastJavaMethod = "example/Outer.pending()V"
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
	if runtime.LastJavaMethod != "example/Inner.pending()V" {
		t.Fatalf("propagated Java method = %q", runtime.LastJavaMethod)
	}
}

func TestKTFGraphicsFillRectUpdatesFramebuffer(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 16, 16))
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	graphicsState := runtime.Graphics[graphics]
	graphicsState.translate = image.Pt(99, -17)
	graphicsState.clip = image.Rect(2, 3, 4, 5)
	graphicsState.color = color.RGBA{R: 1, G: 2, B: 3, A: 4}
	runtime.ResetScreenGraphics(graphics)
	if graphicsState.translate != (image.Point{}) ||
		graphicsState.clip != runtime.frame.Bounds() ||
		graphicsState.color != (color.RGBA{A: 0xff}) {
		t.Fatalf("reset screen graphics = %#v", graphicsState)
	}
	stack := guest.DefaultStackBase + 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
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
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleGraphicsMethod("setColor", "(I)V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 0x3366cc); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleGraphicsMethod("setColor", "(I)V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleGraphicsMethod("fillRect", "(IIII)V"); err != nil {
		t.Fatal(err)
	}
	got := runtime.frame.RGBAAt(3, 2)
	if got.R != 0x33 || got.G != 0x66 || got.B != 0xcc || got.A != 0xff {
		t.Fatalf("filled pixel = %#v", got)
	}
	servicePixels, err := runtime.Services.Graphics.RGBA(
		runtime.ServiceOwner,
		runtime.GraphicsServices[graphics],
	)
	if err != nil {
		t.Fatal(err)
	}
	serviceOffset := (2*16 + 3) * 4
	if servicePixels[serviceOffset] != 0 ||
		servicePixels[serviceOffset+1] != 0 ||
		servicePixels[serviceOffset+2] != 0 {
		t.Fatalf(
			"unpresented shared pixel = %v, want batched black",
			servicePixels[serviceOffset:serviceOffset+4],
		)
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 3); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 2); err != nil {
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
	text, err := runtime.NewJavaString("A")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 0xffffff); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleGraphicsMethod("setColor", "(I)V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, text); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 8); err != nil {
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
	if got := runtime.frame.RGBAAt(3, 2); got != (color.RGBA{
		R: 0x33,
		G: 0x66,
		B: 0xcc,
		A: 0xff,
	}) {
		t.Fatalf("shared text erased batched fill pixel: %#v", got)
	}
}

func TestKTFGraphicsDrawImageAdvancesSourceWhenClipped(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 1, 1))
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}

	source := image.NewRGBA(image.Rect(10, 20, 11, 23))
	source.SetRGBA(10, 20, color.RGBA{R: 0xff, A: 0xff})
	source.SetRGBA(10, 21, color.RGBA{G: 0xff, A: 0xff})
	source.SetRGBA(10, 22, color.RGBA{B: 0xff, A: 0xff})
	javaImage, err := runtime.newJavaImage(source)
	if err != nil {
		t.Fatal(err)
	}

	stack := guest.DefaultStackBase + 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{^uint32(0), 0}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: graphics,
		cpu.RegisterR2: javaImage,
		cpu.RegisterR3: 0,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleGraphicsMethod(
		"drawImage",
		"(Lorg/kwis/msp/lcdui/Image;III)V",
	); err != nil {
		t.Fatal(err)
	}
	if got := runtime.frame.RGBAAt(0, 0); got != (color.RGBA{
		G: 0xff,
		A: 0xff,
	}) {
		t.Fatalf("clipped image pixel = %#v, want second source row", got)
	}
}

func TestKTFGraphicsSetRGBPixelsUsesByteStrideAndClip(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 4, 3))
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	runtime.Graphics[graphics].clip = image.Rect(2, 0, 3, 2)

	pixels, err := runtime.NewJavaArray("[I", 7, 4)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := runtime.ReadU32(pixels)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(fields+8, []uint32{
		0xdeadbeef,
		0x00112233,
		0xff445566,
		0x0badf00d,
		0x00778899,
		0xffaabbcc,
		0xcafebabe,
	}); err != nil {
		t.Fatal(err)
	}

	stack := guest.DefaultStackBase + 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{
		2,
		2,
		pixels,
		1,
		12,
	}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: graphics,
		cpu.RegisterR2: 1,
		cpu.RegisterR3: 0,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleGraphicsMethod(
		"setRGBPixels",
		"(IIII[III)V",
	); err != nil {
		t.Fatal(err)
	}

	if got := runtime.frame.RGBAAt(2, 0); got != (color.RGBA{
		R: 0x44,
		G: 0x55,
		B: 0x66,
		A: 0xff,
	}) {
		t.Fatalf("first clipped RGB pixel = %#v", got)
	}
	if got := runtime.frame.RGBAAt(2, 1); got != (color.RGBA{
		R: 0xaa,
		G: 0xbb,
		B: 0xcc,
		A: 0xff,
	}) {
		t.Fatalf("second clipped RGB pixel = %#v", got)
	}
	if got := runtime.frame.RGBAAt(1, 0); got != (color.RGBA{}) {
		t.Fatalf("pixel outside clip = %#v", got)
	}
	if !runtime.Graphics[graphics].PixelsDirty {
		t.Fatal("RGB pixel write did not dirty the graphics surface")
	}
}

func TestKTFGraphicsGetRGBPixelsReadsBackSurface(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 4, 3))
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	state := runtime.Graphics[graphics]
	state.Target.Set(1, 0, color.RGBA{R: 0x44, G: 0x55, B: 0x66, A: 0xff})
	state.Target.Set(2, 0, color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xff})
	state.Target.Set(1, 1, color.RGBA{R: 0xaa, G: 0xbb, B: 0xcc, A: 0xff})
	// A tight clip must not affect reads; only writes are clipped.
	state.clip = image.Rect(0, 0, 1, 1)

	pixels, err := runtime.NewJavaArray("[I", 7, 4)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := runtime.ReadU32(pixels)
	if err != nil {
		t.Fatal(err)
	}

	stack := guest.DefaultStackBase + 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{
		2,
		2,
		pixels,
		1,
		12,
	}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: graphics,
		cpu.RegisterR2: 1,
		cpu.RegisterR3: 0,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleGraphicsMethod(
		"getRGBPixels",
		"(IIII[III)V",
	); err != nil {
		t.Fatal(err)
	}

	values, err := runtime.ReadWords(fields+8, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{
		0,
		0x00445566,
		0x00112233,
		0,
		0x00aabbcc,
		0,
		0,
	}
	if !slices.Equal(values, want) {
		t.Fatalf("read-back RGB pixels = %08x, want %08x", values, want)
	}
}

func TestKTFStringGetCharsCopiesIntoGuestArray(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	source, err := runtime.NewJavaString("ds2.pts")
	if err != nil {
		t.Fatal(err)
	}
	buffer, err := runtime.NewJavaArray("[C", 10, 2)
	if err != nil {
		t.Fatal(err)
	}

	stack := guest.DefaultStackBase + 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{buffer, 0}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: source,
		cpu.RegisterR2: 0,
		cpu.RegisterR3: 7,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleStringMethod(
		"getChars",
		"(II[CI)V",
	); err != nil {
		t.Fatal(err)
	}

	copied, err := runtime.readJavaCharArrayRange(buffer, 0, 7)
	if err != nil {
		t.Fatal(err)
	}
	if copied != "ds2.pts" {
		t.Fatalf("copied characters = %q, want %q", copied, "ds2.pts")
	}
}

func TestKTFStringConstructorMaterializesGuestFields(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := runtime.newJavaInstance("java/lang/String", 0)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("abc.01")
	array, err := runtime.NewJavaArray("[B", uint32(len(data)), 1)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := runtime.ReadU32(array)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(fields+8, data); err != nil {
		t.Fatal(err)
	}

	stack := guest.DefaultStackBase + 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{uint32(len(data))}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: instance,
		cpu.RegisterR2: array,
		cpu.RegisterR3: 0,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleStringMethod(
		"<init>",
		"([BII)V",
	); err != nil {
		t.Fatal(err)
	}

	// The guest-visible fields must expose the same characters the host map
	// records, so AOT code that reads value/offset/count directly sees the
	// real text instead of an empty array.
	delete(runtime.JavaStrings, instance)
	value, ok := runtime.readGuestJavaString(instance)
	if !ok || value != "abc.01" {
		t.Fatalf("guest string fields decode = %q, %t", value, ok)
	}
}
func TestKTFGraphicsEncodeImageRoundTripsTranslatedRegion(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 4, 3))
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	state := runtime.Graphics[graphics]
	state.translate = image.Pt(1, 0)
	state.Target.Set(1, 0, color.RGBA{R: 0xff, A: 0xff})
	state.Target.Set(2, 0, color.RGBA{G: 0xff, A: 0xff})
	state.Target.Set(1, 1, color.RGBA{B: 0xff, A: 0xff})
	state.Target.Set(2, 1, color.RGBA{R: 0xff, G: 0xff, A: 0xff})
	state.PixelsDirty = true

	stack := guest.DefaultStackBase + 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{2, 2}); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: graphics,
		cpu.RegisterR2: 0,
		cpu.RegisterR3: 0,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	encodedArray, err := runtime.handleGraphicsMethod(
		"encodeImage",
		"(IIII)[B",
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := runtime.readJavaByteArray(encodedArray)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) < 2 || string(encoded[:2]) != "BM" {
		t.Fatalf("encoded image is not BMP: %x", encoded)
	}

	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, encodedArray); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 0); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(
		cpu.RegisterR3,
		uint32(len(encoded)),
	); err != nil {
		t.Fatal(err)
	}
	imageObject, err := runtime.handleImageMethod(
		"createImage",
		"([BII)Lorg/kwis/msp/lcdui/Image;",
	)
	if err != nil {
		t.Fatal(err)
	}
	decoded := runtime.images[imageObject]
	if decoded == nil || decoded.Bounds().Dx() != 2 || decoded.Bounds().Dy() != 2 {
		t.Fatalf("decoded image = %#v", decoded)
	}
	want := [][]color.RGBA{
		{{R: 0xff, A: 0xff}, {G: 0xff, A: 0xff}},
		{{B: 0xff, A: 0xff}, {R: 0xff, G: 0xff, A: 0xff}},
	}
	for y := range want {
		for x := range want[y] {
			if got := color.RGBAModel.Convert(decoded.At(x, y)); got != want[y][x] {
				t.Fatalf("decoded pixel (%d,%d) = %#v, want %#v", x, y, got, want[y][x])
			}
		}
	}
}

func TestKTFStringBufferDeleteClampsEndToLength(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	const instance = uint32(0x1234)
	runtime.stringBuffers[instance] = "abcdefg"
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: instance,
		cpu.RegisterR2: 0,
		cpu.RegisterR3: 400,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
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
	runtime := &Runtime{}
	if got := runtime.handsetSystemProperty(" phonemodel "); got != "LG-KH1300" {
		t.Fatalf("PHONEMODEL = %q, want LG-KH1300", got)
	}
	if got := runtime.handsetSystemProperty("UNKNOWN"); got != "" {
		t.Fatalf("unknown handset property = %q, want empty string", got)
	}
}

func TestKTFJavaArrayNewCreatesPrimitiveArray(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 'I'); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, 3); err != nil {
		t.Fatal(err)
	}
	instance, err := ktfJavaArrayNew(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	instanceWords, err := runtime.ReadWords(instance, 2)
	if err != nil {
		t.Fatal(err)
	}
	if instanceWords[0] == 0 || instanceWords[1] == 0 {
		t.Fatalf("array instance = %08x", instanceWords)
	}
	class, err := runtime.InspectJavaClass(instanceWords[1])
	if err != nil {
		t.Fatal(err)
	}
	if class.Name != "[I" {
		t.Fatalf("array class = %q", class.Name)
	}
	fields, err := runtime.ReadWords(instanceWords[0], 5)
	if err != nil {
		t.Fatal(err)
	}
	if fields[1] != 3 || fields[2] != 0 || fields[3] != 0 || fields[4] != 0 {
		t.Fatalf("array fields = %08x", fields)
	}
}

func TestKTFJavaArrayNewPreservesMultidimensionalArrayClass(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	arrayClass, err := runtime.EnsureJavaClass("[[B")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, arrayClass); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, 3); err != nil {
		t.Fatal(err)
	}
	instance, err := ktfJavaArrayNew(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	instanceWords, err := runtime.ReadWords(instance, 2)
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.InspectJavaClass(instanceWords[1])
	if err != nil {
		t.Fatal(err)
	}
	if class.Name != "[[B" {
		t.Fatalf("multidimensional array class = %q, want %q", class.Name, "[[B")
	}
}

func TestKTFJavaCheckTypeFollowsClassHierarchyAndArrayRule(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	objectClass, err := runtime.EnsureJavaClass("java/lang/Object")
	if err != nil {
		t.Fatal(err)
	}
	cardClass, err := runtime.EnsureJavaClass("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	stringClass, err := runtime.EnsureJavaClass("java/lang/String")
	if err != nil {
		t.Fatal(err)
	}
	card, err := runtime.newJavaInstance("org/kwis/msp/lcdui/Card", 0)
	if err != nil {
		t.Fatal(err)
	}
	array, err := runtime.NewJavaArray("[I", 1, 4)
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
			if err := runtime.CPU.WriteRegister(register, value); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	address, err := runtime.EnsureJavaClass("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.InspectJavaClass(address)
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
	inherited := JavaMethod{
		DeclaringClass: parentAddress,
		Name:           "<clinit>",
		Descriptor:     "()V",
		Body:           0x3001,
	}
	child := JavaClass{
		Address: childAddress,
		Methods: []JavaMethod{inherited},
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	labelAddress, err := runtime.EnsureJavaClass(
		"org/kwis/msp/lwc/LabelComponent",
	)
	if err != nil {
		t.Fatal(err)
	}
	labelClass, err := runtime.InspectJavaClass(labelAddress)
	if err != nil {
		t.Fatal(err)
	}
	label, err := runtime.NewJavaInstanceForClass(labelClass)
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
	labelWords, err := runtime.ReadWords(labelAddress, 5)
	if err != nil {
		t.Fatal(err)
	}
	const collisionCapacity = uint32(512)
	labelVTable := make([]uint32, collisionCapacity)
	labelCopyCount := labelWords[4] & 0xffff
	currentLabel, err := runtime.ReadWords(
		labelWords[3],
		int(labelCopyCount),
	)
	if err != nil {
		t.Fatal(err)
	}
	copy(labelVTable, currentLabel)
	labelVTable[ktfHostVirtualSlotBase] = labelConstructor.Address
	labelReplacement, err := runtime.AllocateWords(collisionCapacity)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(labelReplacement, labelVTable); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(labelAddress+12, labelReplacement); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ensureJavaVTableIndex(
		labelAddress,
		labelReplacement,
	); err != nil {
		t.Fatal(err)
	}
	runtime.javaVTableCapacity[labelAddress] = collisionCapacity
	stringAddress, err := runtime.EnsureJavaClass("java/lang/String")
	if err != nil {
		t.Fatal(err)
	}
	stringClass, err := runtime.InspectJavaClass(stringAddress)
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
	stringWords, err := runtime.ReadWords(stringAddress, 5)
	if err != nil {
		t.Fatal(err)
	}
	stringVTable := make([]uint32, collisionCapacity)
	copyCount := stringWords[4] & 0xffff
	current, err := runtime.ReadWords(stringWords[3], int(copyCount))
	if err != nil {
		t.Fatal(err)
	}
	copy(stringVTable, current)
	stringVTable[ktfHostVirtualSlotBase] = stringLength.Address
	replacement, err := runtime.AllocateWords(collisionCapacity)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(replacement, stringVTable); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(stringAddress+12, replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ensureJavaVTableIndex(stringAddress, replacement); err != nil {
		t.Fatal(err)
	}
	runtime.javaVTableCapacity[stringAddress] = collisionCapacity
	componentAddress, err := runtime.EnsureJavaClass(
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
	background, err := runtime.InspectJavaMethod(backgroundAddress)
	if err != nil {
		t.Fatal(err)
	}
	foreground, err := runtime.InspectJavaMethod(foregroundAddress)
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
		fields, err := runtime.ReadU32(label)
		if err != nil {
			t.Fatal(err)
		}
		header, err := runtime.ReadU32(fields)
		if err != nil {
			t.Fatal(err)
		}
		vtable, err := runtime.ReadU32(runtime.JvmContext + 12 + (header >> 5))
		if err != nil {
			t.Fatal(err)
		}
		method, err := runtime.ReadU32(vtable + uint32(slot)*4)
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
	gotStringSlot, err := runtime.ReadU32(
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
	labelClass, err = runtime.InspectJavaClass(labelAddress)
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

func TestKTFHostVTableExpansionPreservesLargeGuestTable(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}

	const (
		logicalSize = uint32(579)
		guestSlot   = uint32(540)
		guestMethod = uint32(0x13579bdf)
		hostMethod  = uint32(0x2468ace0)
	)
	entries := make([]uint32, logicalSize)
	entries[guestSlot] = guestMethod
	guestVTable, err := runtime.AllocateWords(logicalSize)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(guestVTable, entries); err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.AllocateWords(5)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(classAddress, []uint32{
		classAddress + 4,
		0,
		0,
		guestVTable,
		logicalSize,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.ensureJavaVTableIndex(
		classAddress,
		guestVTable,
	); err != nil {
		t.Fatal(err)
	}

	if err := runtime.installHostJavaVirtualMethodForClass(
		classAddress,
		hostMethod,
		ktfHostVirtualSlotBase,
	); err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaVTableCapacity[classAddress]; got < logicalSize {
		t.Fatalf("expanded vtable capacity = %d, want at least %d", got, logicalSize)
	}
	expanded, err := runtime.ReadU32(classAddress + 12)
	if err != nil {
		t.Fatal(err)
	}
	gotGuest, err := runtime.ReadU32(expanded + guestSlot*4)
	if err != nil {
		t.Fatal(err)
	}
	if gotGuest != guestMethod {
		t.Fatalf(
			"guest vtable slot %d = 0x%08x, want 0x%08x",
			guestSlot,
			gotGuest,
			guestMethod,
		)
	}
	gotHost, err := runtime.ReadU32(
		expanded + uint32(ktfHostVirtualSlotBase)*4,
	)
	if err != nil {
		t.Fatal(err)
	}
	if gotHost != hostMethod {
		t.Fatalf(
			"host vtable slot %d = 0x%08x, want 0x%08x",
			ktfHostVirtualSlotBase,
			gotHost,
			hostMethod,
		)
	}
}

func TestKTFLWCFoundationMethodsTrackState(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	const (
		component = uint32(0x10001000)
		listener  = uint32(0x10002000)
		eventData = uint32(0x10003000)
		shell     = uint32(0x10004000)
	)
	for register, value := range []uint32{0, component, listener, eventData} {
		if err := runtime.CPU.WriteRegister(uint32(register), value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := HostJavaMethod(
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, component); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 24); err != nil {
		t.Fatal(err)
	}
	if _, err := HostJavaMethod(
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
	if _, err := HostJavaMethod(
		"org/kwis/msp/lwc/TextBoxComponent",
		"keyNotify",
		"(II)Z",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	textBoxKey :=
		"org/kwis/msp/lwc/TextBoxComponent.keyNotify(II)Z"
	if runtime.UnimplementedJava[textBoxKey] != 0 {
		t.Fatalf(
			"TextBoxComponent.keyNotify was left unimplemented: %v",
			runtime.UnimplementedJava,
		)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, shell); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, component); err != nil {
		t.Fatal(err)
	}
	index, err := HostJavaMethod(
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, listener); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, component); err != nil {
		t.Fatal(err)
	}
	if _, err := HostJavaMethod(
		"com/ktf/kfc/GTextListener",
		"setIMEModes",
		"([I)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if len(runtime.UnimplementedJava) != 0 {
		t.Fatalf(
			"LWC calls recorded as unimplemented: %#v",
			runtime.UnimplementedJava,
		)
	}
}

func TestKTFLWCHierarchyAndAnnunciatorGeometry(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	annunciatorAddress, err := runtime.EnsureJavaClass(
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
		class, inspectErr := runtime.InspectJavaClass(address)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if class.Name != want {
			t.Fatalf("LWC hierarchy[%d] = %q, want %q", index, class.Name, want)
		}
		address = class.Parent
	}
	annunciatorClass, err := runtime.InspectJavaClass(annunciatorAddress)
	if err != nil {
		t.Fatal(err)
	}
	annunciator, err := runtime.NewJavaInstanceForClass(annunciatorClass)
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

func TestKTFOpaqueAnnunciatorReservesDefaultCardHeight(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	annunciator, err := runtime.NewHostJavaObject(
		"org/kwis/msp/lwc/AnnunciatorComponent",
	)
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
	if _, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/AnnunciatorComponent",
		"show",
		"()V",
		registers,
	); err != nil {
		t.Fatal(err)
	}
	card, err := runtime.NewHostJavaObject("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.initializeCard(card, 0x10004000); err != nil {
		t.Fatal(err)
	}
	height, err := runtime.readJavaFieldWord(card, 20)
	if err != nil {
		t.Fatal(err)
	}
	if height != ktfDisplayHeight-uint32(ktfAnnunciatorHeight) {
		t.Fatalf("opaque-annunciator card height = %d", height)
	}

	runtime.setLWCShown(annunciator, false)
	transparent, err := runtime.NewHostJavaObject(
		"org/kwis/msp/lwc/AnnunciatorComponent",
	)
	if err != nil {
		t.Fatal(err)
	}
	registers[1] = transparent
	registers[2] = 1
	if _, err := runtime.handleLWCMethod(
		context.Background(),
		"org/kwis/msp/lwc/AnnunciatorComponent",
		"<init>",
		"(Z)V",
		registers,
	); err != nil {
		t.Fatal(err)
	}
	runtime.setLWCShown(transparent, true)
	if got := runtime.DefaultCardHeight(); got != ktfDisplayHeight {
		t.Fatalf("transparent-annunciator card height = %d", got)
	}
}

func TestKTFLWCFormLaysOutChildrenAndScreenCoordinates(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
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
	if len(runtime.UnimplementedJava) != 0 {
		t.Fatalf("LWC calls recorded as unimplemented: %#v", runtime.UnimplementedJava)
	}
}

func TestKTFDisplayCallSeriallyTimeoutQueuesRunnable(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	runnable, err := runtime.NewHostJavaObject("java/lang/Thread")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, runnable); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 100); err != nil {
		t.Fatal(err)
	}
	runtime.DeferThreads = true
	if _, err := HostJavaMethod(
		"org/kwis/msp/lcdui/Display",
		"callSerially",
		"(Ljava/lang/Runnable;I)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 1 || runtime.Tasks[0].Done {
		t.Fatalf("callSerially tasks = %#v", runtime.Tasks)
	}
}

func TestKTFCalendarGetTimeReturnsModeledDate(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	calendar, err := runtime.NewHostJavaObject("java/util/Calendar")
	if err != nil {
		t.Fatal(err)
	}
	const millis = int64(123456789)
	runtime.dates[calendar] = millis
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, calendar); err != nil {
		t.Fatal(err)
	}
	date, err := HostJavaMethod(
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	systemClassAddress, err := runtime.EnsureJavaClass("java/lang/System")
	if err != nil {
		t.Fatal(err)
	}
	systemClass, err := runtime.InspectJavaClass(systemClassAddress)
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
	parameters, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, currentTime.NativeBody); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}
	result, err := ktfCallNative(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if result != parameters {
		t.Fatalf("call-native result = 0x%08x", result)
	}
	values, err := runtime.ReadWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if values[0] != 0 || values[1] != 0 {
		t.Fatalf("native return container = %08x", values)
	}
	if runtime.NativeParameterBase != 0 {
		t.Fatalf(
			"native parameter base leaked: 0x%08x",
			runtime.NativeParameterBase,
		)
	}
}

func TestKTFCallNativePrefersExplicitHostTargetOverStaleOverride(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	target := runtime.RegisterHostCall(
		"test.explicit_native_target",
		func(context.Context, *Runtime) (uint32, error) {
			return 42, nil
		},
	)
	parameters, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	runtime.LastJavaMethod =
		"org/kwis/msp/lcdui/Graphics.getClipHeight()I"
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, target); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}

	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	values, err := runtime.ReadWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(values, []uint32{42, 0}) {
		t.Fatalf("explicit native target return = %08x", values)
	}
}

func TestKTFCallNativeOverridesNullThreadSleepTarget(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.AllocateWords(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(parameters, []uint32{60, 0, 0, 0}); err != nil {
		t.Fatal(err)
	}
	runtime.DeferThreads = true
	task := &Task{}
	runtime.activeTask = task
	runtime.Tasks = []*Task{task}
	runtime.TickMS = 1_000
	runtime.LastJavaMethod = "java/lang/Thread.sleep(J)V"
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 0); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}

	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if !runtime.yieldRequested {
		t.Fatal("null-target Thread.sleep did not yield its Java task")
	}
	if task.WakeAtMS != 1_060 {
		t.Fatalf("Thread.sleep wake deadline = %d, want 1060", task.WakeAtMS)
	}
	runtime.activeTask = nil
	if got := runtime.nextRunnableTask(); got != nil {
		t.Fatalf("sleeping scheduler selected task %p", got)
	}
	result := runtime.RunTaskSlice(context.Background(), 16)
	if result.Reason != cpu.StopBudget || result.Instructions != 0 {
		t.Fatalf("all-sleeping task slice = %+v", result)
	}
	runtime.TickMS = 1_059
	if got := runtime.nextRunnableTask(); got != nil {
		t.Fatalf("scheduler woke task early at %dms", runtime.TickMS)
	}
	runtime.TickMS = 1_060
	if got := runtime.nextRunnableTask(); got != task || task.WakeAtMS != 0 {
		t.Fatalf(
			"scheduler wake at deadline = task %p, deadline %d",
			got,
			task.WakeAtMS,
		)
	}
	if !slices.Contains(
		runtime.HostTrace,
		"java.native_override.java/lang/Thread.sleep(J)V",
	) {
		t.Fatalf("Thread.sleep override trace = %v", runtime.HostTrace)
	}
}

func TestKTFJavaTimerTaskCancelStopsDelayedCallback(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	classAddress, err := runtime.EnsureJavaClass("test/TimerCallback")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.InspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	runMethod, err := runtime.addHostJavaMethod(class, "run", "()V")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(runMethod, ImageBase|1); err != nil {
		t.Fatal(err)
	}
	callback, err := runtime.NewJavaInstanceForClass(class)
	if err != nil {
		t.Fatal(err)
	}
	timer, err := runtime.NewHostJavaObject("java/util/Timer")
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.AllocateWords(6)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(parameters, []uint32{
		timer, callback, 50, 0, 0, 0,
	}); err != nil {
		t.Fatal(err)
	}
	runtime.NativeParameterBase = parameters
	runtime.DeferThreads = true
	runtime.TickMS = 1_000

	if _, err := runtime.handleTimerMethod(
		context.Background(),
		"schedule",
		"(Ljava/util/TimerTask;J)V",
	); err != nil {
		t.Fatal(err)
	}
	queued := runtime.javaTimerTasks[callback]
	if queued == nil || queued.WakeAtMS != 1_050 || queued.Done {
		t.Fatalf("scheduled TimerTask = %#v, want a 1050ms deadline", queued)
	}
	if got := runtime.nextRunnableTask(); got != nil {
		t.Fatalf("TimerTask ran before its deadline: %p", got)
	}

	if err := runtime.WriteU32(parameters, callback); err != nil {
		t.Fatal(err)
	}
	cancelled, err := runtime.handleTimerMethod(
		context.Background(),
		"cancel",
		"()Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 || !queued.Done || runtime.javaTimerTasks[callback] != nil {
		t.Fatalf(
			"cancelled TimerTask = result %d, done %t, pending %p",
			cancelled,
			queued.Done,
			runtime.javaTimerTasks[callback],
		)
	}
	runtime.TickMS = 1_050
	if got := runtime.nextRunnableTask(); got != nil {
		t.Fatalf("cancelled TimerTask became runnable: %p", got)
	}
	second, err := runtime.handleTimerMethod(
		context.Background(),
		"cancel",
		"()Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	if second != 0 {
		t.Fatalf("second TimerTask.cancel result = %d, want 0", second)
	}
}

func TestKTFCallNativeCorrectsStaleMethodForCachedGuestNative(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	systemClassAddress, err := runtime.EnsureJavaClass("java/lang/System")
	if err != nil {
		t.Fatal(err)
	}
	systemClass, err := runtime.InspectJavaClass(systemClassAddress)
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
	if err := runtime.WriteU32(
		currentTime.Address+8,
		ImageBase|1,
	); err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	runtime.TickMS = 123
	runtime.LastJavaMethod =
		"org/kwis/msp/lcdui/Graphics.getClipHeight()I"
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, ImageBase|1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}

	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	values, err := runtime.ReadWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(values, []uint32{123, 0}) {
		t.Fatalf("corrected native return = %08x", values)
	}
	if runtime.LastJavaMethod !=
		"java/lang/System.currentTimeMillis()J" {
		t.Fatalf("corrected native method = %q", runtime.LastJavaMethod)
	}
}

func TestKTFCallNativeReadsEnvironmentReturnSlot(t *testing.T) {
	// Thumb stub modeling an SDK-compiled native: it deposits kind 2 at
	// env+0x24 and its value at env+0x28, then returns with scratch garbage
	// in R0 the way dnff's calcClet does.
	client := make([]byte, 24)
	binary.LittleEndian.PutUint16(client[0:], 0x4B03)  // ldr r3, [pc, #12]
	binary.LittleEndian.PutUint16(client[2:], 0x2202)  // movs r2, #2
	binary.LittleEndian.PutUint16(client[4:], 0x625A)  // str r2, [r3, #0x24]
	binary.LittleEndian.PutUint16(client[6:], 0x4A03)  // ldr r2, [pc, #12]
	binary.LittleEndian.PutUint16(client[8:], 0x629A)  // str r2, [r3, #0x28]
	binary.LittleEndian.PutUint16(client[10:], 0x2077) // movs r0, #0x77
	binary.LittleEndian.PutUint16(client[12:], 0x4770) // bx lr
	binary.LittleEndian.PutUint16(client[14:], 0x46c0) // nop
	binary.LittleEndian.PutUint32(client[20:], 1234)
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     client,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	environment, err := runtime.AllocateWords(ktfJavaEnvironmentWords)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(holder, []uint32{environment}); err != nil {
		t.Fatal(err)
	}
	runtime.javaEnvironment = holder
	if err := runtime.WriteU32(ImageBase+16, environment); err != nil {
		t.Fatal(err)
	}
	// Leave a stale value in the slot; ktfCallNative must clear it before
	// the call so a native that writes nothing is not misread.
	if err := runtime.writeWords(
		environment+0x24,
		[]uint32{2, 0xdead},
	); err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.AllocateWords(4)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR0: ImageBase | 1,
		cpu.RegisterR1: parameters,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}

	value, err := runtime.ReadU32(parameters)
	if err != nil {
		t.Fatal(err)
	}
	if value != 1234 {
		t.Fatalf("native return = %d, want 1234 from the environment slot", value)
	}
}
func TestKTFJavaStringExposesNativeLayoutAndCopiesToGuest(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.NewJavaString("Clet")
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

	destination, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	for register, registerValue := range []uint32{
		value,
		destination,
		8,
	} {
		if err := runtime.CPU.WriteRegister(
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
	if err := runtime.CPU.ReadMemory(destination, output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, []byte{'C', 'l', 'e', 't', 0}) {
		t.Fatalf("native String copy = %q", output)
	}
}

func TestKTFObjectWaitYieldsDeferredThread(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.EnsureJavaClass("java/lang/Object")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.InspectJavaClass(classAddress)
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
	runtime.DeferThreads = true
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	card, err := runtime.NewHostJavaObject("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	parent := &Task{}
	runtime.DeferThreads = true
	runtime.activeTask = parent
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, card); err != nil {
		t.Fatal(err)
	}
	if _, err := HostJavaMethod(
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
	if task := runtime.PaintTasks[card]; task == nil ||
		!task.presentOnReturn {
		t.Fatalf("repaint task = %#v", task)
	}
	if len(runtime.Tasks) != 1 {
		t.Fatalf("repaint queued %d tasks, want paint only", len(runtime.Tasks))
	}
}

func TestKTFCallNativeOverridesBrokenFrameworkNative(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	runtime.LastJavaMethod =
		"org/kwis/msp/lcdui/Display.addJletEventListener" +
			"(Lorg/kwis/msp/lcdui/JletEventListener;)V"
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, ImageBase|1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	values, err := runtime.ReadWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0] != 0 || values[1] != 0 {
		t.Fatalf("native override return container = %08x", values)
	}
}

func TestKTFCallNativeOverridesNullFrameworkNative(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.AllocateWords(3)
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
		{"org/kwis/msp/lcdui/Display.getGameAction(I)I", 0},
	}
	for _, test := range tests {
		t.Run(test.method, func(t *testing.T) {
			runtime.LastJavaMethod = test.method
			if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 0); err != nil {
				t.Fatal(err)
			}
			if err := runtime.CPU.WriteRegister(
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
			values, err := runtime.ReadWords(parameters, 2)
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	chars, err := runtime.newJavaCharArray("WIPI!")
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.AllocateWords(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(
		parameters,
		[]uint32{chars, 1, 3, 0},
	); err != nil {
		t.Fatal(err)
	}
	runtime.LastJavaMethod =
		"java/lang/String.valueOf([CII)Ljava/lang/String;"
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 0); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	values, err := runtime.ReadWords(parameters, 2)
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := runtime.AllocateWords(3)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(parameters, []uint32{graphics, 0, 0}); err != nil {
		t.Fatal(err)
	}
	runtime.LastJavaMethod =
		"org/kwis/msp/lcdui/Graphics.getClipHeight()I"
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 0); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, parameters); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfCallNative(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	values, err := runtime.ReadWords(parameters, 2)
	if err != nil {
		t.Fatal(err)
	}
	if values[0] != 320 || values[1] != 0 {
		t.Fatalf("Graphics.getClipHeight return = %08x", values)
	}
}

func TestKTFDispatchJavaExceptionBuildsGuestRestoreTarget(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	catchClass, err := runtime.EnsureJavaClass("java/lang/Exception")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := runtime.AllocateWords(4)
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
	table, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(table, entry); err != nil {
		t.Fatal(err)
	}
	method, err := runtime.AllocateWords(7)
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
	functions, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	const restore = uint32(0x00123457)
	if err := runtime.writeWords(functions, []uint32{0, restore}); err != nil {
		t.Fatal(err)
	}
	frame, err := runtime.AllocateWords(17)
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
	exceptionContext, err := runtime.AllocateWords(ktfJavaEnvironmentWords)
	if err != nil {
		t.Fatal(err)
	}
	runtime.exceptionContext = exceptionContext
	if err := runtime.WriteU32(exceptionContext+8*4, frame); err != nil {
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
	if len(runtime.JavaExceptionFrames) != 1 ||
		!strings.Contains(runtime.JavaExceptionFrames[0], "bcp=20") {
		t.Fatalf("exception frames = %v", runtime.JavaExceptionFrames)
	}
	if detail, err := runtime.ReadU32(frame + 4*4); err != nil {
		t.Fatal(err)
	} else if detail != 0x10203040 {
		t.Fatalf("exception detail = 0x%08x", detail)
	}
	contextWords, err := runtime.ReadWords(target.contextBase, len(saved))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(contextWords, saved) {
		t.Fatalf("exception restore context = %08x, want %08x", contextWords, saved)
	}
}

func TestKTFCallOwnsOnlyNestedJavaExceptionFramesBelowCallerStack(t *testing.T) {
	callerStack := guest.DefaultStackBase + 0x8000
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
			runtime := &Runtime{executionDepth: test.executionDepth}
			unwind := &ktfJavaExceptionUnwind{
				Target: ktfJavaExceptionTarget{contextBase: test.contextBase},
			}
			if got := runtime.callOwnsJavaExceptionUnwind(callerStack, unwind); got != test.want {
				t.Fatalf("call owns exception unwind = %t, want %t", got, test.want)
			}
		})
	}
}

func TestKTFJavaThrowObjectUsesGuestInstanceClass(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	instance, err := runtime.NewHostJavaObject("java/lang/Exception")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, instance); err != nil {
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
	if runtime.LastJavaThrowName != "java/lang/Exception" {
		t.Fatalf("last Java throw name = %q", runtime.LastJavaThrowName)
	}
}

func TestKTFJavaJumpCallsHostWithoutResettingGuestContext(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	var hostLR uint32
	host := runtime.RegisterHostCall(
		"test.direct_host",
		func(_ context.Context, runtime *Runtime) (uint32, error) {
			var err error
			hostLR, err = runtime.CPU.ReadRegister(cpu.RegisterLR)
			return 42, err
		},
	)
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 7); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, host); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterLR, 0x00123457); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	fontClass, err := runtime.EnsureJavaClass("org/kwis/msp/lcdui/Font")
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, fontClass); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, fullName); err != nil {
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
	words, err := runtime.ReadWords(first, 4)
	if err != nil {
		t.Fatal(err)
	}
	if words[0]&0x0008 == 0 || words[1] != fontClass || words[3] != 16 {
		t.Fatalf("field descriptor = %08x", words)
	}
}

func TestKTFDefaultDisplayStartsWithoutDockedCard(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	display, err := runtime.ensureDefaultDisplay()
	if err != nil {
		t.Fatal(err)
	}
	if card := runtime.DisplayCards[display]; card != 0 {
		t.Fatalf("default display docked card = 0x%08x, want null", card)
	}

	cardClassAddress, err := runtime.EnsureJavaClass(
		"org/kwis/msp/lcdui/Card",
	)
	if err != nil {
		t.Fatal(err)
	}
	cardClass, err := runtime.InspectJavaClass(cardClassAddress)
	if err != nil {
		t.Fatal(err)
	}
	explicitCard, err := runtime.NewJavaInstanceForClass(cardClass)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, explicitCard); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, display); err != nil {
		t.Fatal(err)
	}
	if _, err := HostJavaMethod(
		"org/kwis/msp/lcdui/Card",
		"<init>",
		"(Lorg/kwis/msp/lcdui/Display;)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, explicitCard); err != nil {
		t.Fatal(err)
	}
	cardDisplay, err := HostJavaMethod(
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

	runtime.DeferThreads = true
	parentTask := &Task{}
	childTask := &Task{}
	runtime.Tasks = []*Task{parentTask, childTask}
	runtime.activeTask = parentTask
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, display); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, explicitCard); err != nil {
		t.Fatal(err)
	}
	if _, err := HostJavaMethod(
		"org/kwis/msp/lcdui/Display",
		"pushCard",
		"(Lorg/kwis/msp/lcdui/Card;)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.DisplayCards[display] != explicitCard {
		t.Fatalf(
			"pushed card = 0x%08x, want 0x%08x",
			runtime.DisplayCards[display],
			explicitCard,
		)
	}
	if task := runtime.PaintTasks[explicitCard]; task != nil {
		t.Fatalf("pushCard scheduled paint before its caller yielded: %#v", task)
	}
	runtime.activeTask = nil
	if err := runtime.releaseDeferredCardPaints(
		context.Background(),
		parentTask,
	); err != nil {
		t.Fatal(err)
	}
	if task := runtime.PaintTasks[explicitCard]; task == nil ||
		!task.presentOnReturn || !task.bestEffortPaint {
		t.Fatalf("pushed card paint task = %#v", task)
	} else if len(runtime.Tasks) != 4 || runtime.Tasks[3] != task {
		t.Fatalf(
			"paint task order = %#v, want parent, child, show, paint",
			runtime.Tasks,
		)
	}
}

func TestKTFJavaArrayCopySupportsOverlappingRanges(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.javaArrayCopy(0, 0, 0, 0, 16); err != nil {
		t.Fatal(err)
	}
}

func TestKTFJavaArrayCopyRaisesGuestExceptions(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	stream, err := runtime.NewHostJavaObject("java/io/InputStream")
	if err != nil {
		t.Fatal(err)
	}
	runtime.inputStreams[stream] = &ktfInputStream{data: []byte{1}}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, stream); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 0); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.exceptionContext, err = runtime.AllocateWords(
		ktfJavaEnvironmentWords,
	)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := runtime.NewHostJavaObject("java/io/DataInputStream")
	if err != nil {
		t.Fatal(err)
	}
	runtime.inputStreams[stream] = &ktfInputStream{}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, stream); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	stream, err := runtime.NewHostJavaObject("java/io/DataInputStream")
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, stream); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.exceptionContext, err = runtime.AllocateWords(
		ktfJavaEnvironmentWords,
	)
	if err != nil {
		t.Fatal(err)
	}

	first := &Task{}
	second := &Task{}
	const (
		firstFrame  = uint32(0x7ffffe40)
		secondFrame = uint32(0x7ffefe40)
	)
	if err := runtime.WriteU32(runtime.exceptionContext+8*4, firstFrame); err != nil {
		t.Fatal(err)
	}
	if err := runtime.saveTaskContext(first); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(runtime.exceptionContext+8*4, secondFrame); err != nil {
		t.Fatal(err)
	}
	if err := runtime.saveTaskContext(second); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		task *Task
		want uint32
	}{
		{name: "first", task: first, want: firstFrame},
		{name: "second", task: second, want: secondFrame},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := runtime.restoreTaskExceptionFrame(test.task); err != nil {
				t.Fatal(err)
			}
			got, err := runtime.ReadU32(runtime.exceptionContext + 8*4)
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
	parent := &Task{}
	child := &Task{}
	runtime := &Runtime{
		Tasks:              []*Task{parent, child},
		taskCursor:         1,
		activeTask:         parent,
		ActiveInstructions: 123,
	}

	runtime.deferStartedThread(child)
	if child.startBlocker != parent {
		t.Fatal("new thread was not blocked behind its starting task")
	}
	if got, want := parent.childStartGrace, ktfInitialThreadStartGrace+123; got != want {
		t.Fatalf("parent start grace = %d, want %d", got, want)
	}
	if got := runtime.nextRunnableTask(); got != parent {
		t.Fatalf("scheduler selected blocked child %p, want parent %p", got, parent)
	}

	// Instructions executed before Thread.start in the current slice must not
	// consume the new thread's grace period.
	runtime.chargeThreadStartGrace(parent, 123)
	if got := parent.childStartGrace; got != ktfInitialThreadStartGrace {
		t.Fatalf("remaining start grace = %d, want %d", got, ktfInitialThreadStartGrace)
	}
	runtime.chargeThreadStartGrace(parent, ktfInitialThreadStartGrace-1)
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

func TestKTFStartedThreadUsesShortGraceAfterCardIsShown(t *testing.T) {
	parent := &Task{}
	child := &Task{}
	runtime := &Runtime{
		Tasks:              []*Task{parent, child},
		taskCursor:         1,
		activeTask:         parent,
		ActiveInstructions: 123,
		DefaultDisplay:     1,
		DisplayCards:       map[uint32]uint32{1: 2},
	}

	runtime.deferStartedThread(child)
	if got, want := parent.childStartGrace, ktfThreadStartGrace+123; got != want {
		t.Fatalf("parent start grace = %d, want %d", got, want)
	}
}

func TestKTFStartedThreadReleasesWhenParentYields(t *testing.T) {
	parent := &Task{childStartGrace: ktfThreadStartGrace}
	child := &Task{startBlocker: parent}
	runtime := &Runtime{Tasks: []*Task{parent, child}}

	runtime.releaseStartedThreads(parent, "yield")
	if parent.childStartGrace != 0 {
		t.Fatalf("parent start grace = %d after yield", parent.childStartGrace)
	}
	if child.startBlocker != nil {
		t.Fatal("child remained blocked after parent yield")
	}
}

func TestKTFTaskRecordsPresentationAfterPaintReturns(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	task, err := runtime.NewTask(ktfReturnSentinel|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	task.presentOnReturn = true
	runtime.Tasks = append(runtime.Tasks, task)
	result := runtime.RunTaskSlice(context.Background(), 16)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != cpu.StopExited {
		t.Fatalf("paint task stopped as %v, want exited", result.Reason)
	}
	if runtime.PresentCount != 1 {
		t.Fatalf("paint task presentations = %d, want 1", runtime.PresentCount)
	}
}

func TestKTFJavaPresentationRetakesScreenFromWIPIC(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	javaSurface := runtime.GraphicsServices[graphics]
	if javaSurface == 0 {
		t.Fatal("Java screen graphics has no shared surface")
	}
	if _, err := runtime.EnsureWIPICScreenFramebuffer(); err != nil {
		t.Fatal(err)
	}
	if runtime.Services.Graphics.Screen() == javaSurface {
		t.Fatal("WIPI-C screen did not take presentation ownership")
	}
	if err := runtime.RecordPresentation(); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Services.Graphics.Screen(); got != javaSurface {
		t.Fatalf("presented surface = %s, want Java screen %s", got, javaSurface)
	}
}

func TestKTFPresentationRequestsTaskYield(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	runtime.activeTask = &Task{}

	if err := runtime.RecordPresentation(); err != nil {
		t.Fatal(err)
	}
	if !runtime.yieldRequested {
		t.Fatal("presentation did not request a task yield")
	}
}

func TestKTFReturningTaskDoesNotSkipNextRunnableTask(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runnable, err := runtime.NewTask(ImageBase|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	returning, err := runtime.NewTask(ktfReturnSentinel|1, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	runtime.Tasks = []*Task{runnable, returning}
	runtime.taskCursor = 1

	result := runtime.RunTaskSlice(context.Background(), 16)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != cpu.StopBudget || !returning.Done {
		t.Fatalf(
			"returning task result = %+v, done=%t",
			result,
			returning.Done,
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	procedure := runtime.RegisterHostCall(
		"synthetic.initial.paint",
		func(context.Context, *Runtime) (uint32, error) {
			return 0, &ktfUnhandledJavaException{
				name:    "java/lang/NullPointerException",
				detail:  0x10001000,
				Context: "synthetic initial paint",
			}
		},
	)
	task, err := runtime.NewTask(procedure|1, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	const card = uint32(0x10002000)
	task.bestEffortPaint = true
	task.paintCard = card
	runtime.Tasks = append(runtime.Tasks, task)
	runtime.PaintTasks[card] = task

	result := runtime.RunTaskSlice(context.Background(), 16)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if result.Reason != cpu.StopExited || !task.Done {
		t.Fatalf(
			"best-effort paint result = %+v, done=%t",
			result,
			task.Done,
		)
	}
	if runtime.PaintTasks[card] != nil {
		t.Fatal("discarded initial paint remained pending")
	}
	if runtime.paintInitializedCards[card] {
		t.Fatal("discarded initial paint closed its best-effort window")
	}
	found := false
	for _, trace := range runtime.HostTrace {
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
			runtime.HostTrace,
		)
	}
}

func TestKTFHostVTableCollisionRedispatchesToGuestReceiver(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client: []byte{
			0x7f, 0x20, // movs r0, #0x7f
			0x70, 0x47, // bx lr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.EnsureJavaClass("test/GuestRenderer")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.InspectJavaClass(classAddress)
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
	if err := runtime.WriteU32(methodAddress, ImageBase|1); err != nil {
		t.Fatal(err)
	}
	delete(runtime.hostJavaClass, classAddress)
	class, err = runtime.InspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	instance, err := runtime.NewJavaInstanceForClass(class)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, instance); err != nil {
		t.Fatal(err)
	}

	value, err := HostJavaMethod(
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
	if runtime.UnimplementedJava["java/lang/System.draw()I"] != 0 {
		t.Fatal("redispatched guest method was recorded as unimplemented")
	}
	found := false
	for _, trace := range runtime.HostTrace {
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
		t.Fatalf("guest redispatch missing from trace: %v", runtime.HostTrace)
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()

	const card = uint32(0x10001000)
	pending := &Task{}
	runtime.dirtyCards[card] = true
	runtime.PaintTasks[card] = pending

	if err := runtime.paintCard(context.Background(), card); err != nil {
		t.Fatal(err)
	}
	if runtime.dirtyCards[card] {
		t.Fatal("coalesced card remained dirty")
	}
	if runtime.PaintTasks[card] != pending {
		t.Fatal("coalesced card replaced its pending paint task")
	}
	if len(runtime.Tasks) != 0 {
		t.Fatalf("coalesced card queued %d additional tasks", len(runtime.Tasks))
	}
	if len(runtime.HostTrace) != 1 ||
		runtime.HostTrace[0] != "java_paint_coalesce:card=0x10001000" {
		t.Fatalf("coalesce trace = %v", runtime.HostTrace)
	}
}

func TestKTFPaintCardWaitsForPendingKeyCallback(t *testing.T) {
	const card = uint32(0x10001000)
	keyTask := &Task{KeyCard: card}
	runtime := &Runtime{
		Tasks:              []*Task{keyTask},
		dirtyCards:         map[uint32]bool{card: true},
		deferredPaintCards: make(map[*Task][]uint32),
		deferredShownCards: make(map[*Task]map[uint32]bool),
		PaintTasks:         make(map[uint32]*Task),
	}

	if err := runtime.paintCard(context.Background(), card); err != nil {
		t.Fatal(err)
	}
	if !runtime.dirtyCards[card] {
		t.Fatal("key-blocked card was cleared before its paint could run")
	}
	if got := runtime.deferredPaintCards[keyTask]; !slices.Equal(
		got,
		[]uint32{card},
	) {
		t.Fatalf("key-blocked paints = %08x, want [%08x]", got, card)
	}
	if len(runtime.PaintTasks) != 0 {
		t.Fatalf("key-blocked card queued a paint task: %#v", runtime.PaintTasks)
	}
	if len(runtime.HostTrace) != 1 ||
		runtime.HostTrace[0] !=
			"java_paint_defer_key:card=0x10001000" {
		t.Fatalf("key-blocked paint trace = %v", runtime.HostTrace)
	}
}

func TestKTFSystemStreamsAreInitializedHostObjects(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	systemAddress, err := runtime.EnsureJavaClass("java/lang/System")
	if err != nil {
		t.Fatal(err)
	}
	systemClass, err := runtime.InspectJavaClass(systemAddress)
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
		field, err := runtime.ResolveJavaField(
			systemClass,
			fieldSpec.name,
			fieldSpec.descriptor,
		)
		if err != nil {
			t.Fatal(err)
		}
		value, err := runtime.ReadU32(field + 12)
		if err != nil {
			t.Fatal(err)
		}
		if value == 0 {
			t.Fatalf("System.%s is null", fieldSpec.name)
		}
		words, err := runtime.ReadWords(value, 2)
		if err != nil {
			t.Fatal(err)
		}
		class, err := runtime.InspectJavaClass(words[1])
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.EnsureJavaClass("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	reference, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(reference, classAddress); err != nil {
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
	method, err := runtime.InspectJavaMethod(methodAddress)
	if err != nil {
		t.Fatal(err)
	}
	if method.Name != "repaint" || method.Descriptor != "()V" {
		t.Fatalf("resolved method = %s%s", method.Name, method.Descriptor)
	}
}

func TestKTFResolveJavaMethodAcceptsDirectVTable(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.EnsureJavaClass("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.InspectJavaClass(classAddress)
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
	method, err := runtime.InspectJavaMethod(methodAddress)
	if err != nil {
		t.Fatal(err)
	}
	if method.Name != "serviceRepaints" || method.Descriptor != "()V" {
		t.Fatalf("resolved method = %s%s", method.Name, method.Descriptor)
	}
}

func TestKTFJavaNewRunsClassInitializerOnce(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.EnsureJavaClass("test/Initialized")
	if err != nil {
		t.Fatal(err)
	}
	classWords, err := runtime.ReadWords(classAddress, 5)
	if err != nil {
		t.Fatal(err)
	}
	descriptorWords, err := runtime.ReadWords(classWords[2], 9)
	if err != nil {
		t.Fatal(err)
	}
	callCount := 0
	body := runtime.RegisterHostCall(
		"test.class_initializer",
		func(context.Context, *Runtime) (uint32, error) {
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
	method, err := runtime.AllocateWords(7)
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
	methods, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(methods, []uint32{method, 0}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(classWords[2]+12, methods); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(
		classWords[2]+24,
		descriptorWords[6]&0xffff0000|1,
	); err != nil {
		t.Fatal(err)
	}

	for index := 0; index < 2; index++ {
		if err := runtime.CPU.WriteRegister(
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	classAddress, err := runtime.EnsureJavaClass(
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
	classWords, err := runtime.ReadWords(classAddress, 5)
	if err != nil {
		t.Fatal(err)
	}
	descriptorWords, err := runtime.ReadWords(classWords[2], 9)
	if err != nil {
		t.Fatal(err)
	}
	fullName, err := runtime.allocateBytes([]byte("\x00()V+<init>"), true)
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := runtime.AllocateWords(7)
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
	methods, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(methods, []uint32{declaration, 0}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(classWords[2]+12, methods); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(
		classWords[2]+24,
		descriptorWords[6]&0xffff0000|1,
	); err != nil {
		t.Fatal(err)
	}
	class, err := runtime.InspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.implementBodylessPlatformMethods(class); err != nil {
		t.Fatal(err)
	}
	patched, err := runtime.InspectJavaMethod(declaration)
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
	method, err := runtime.InspectJavaMethod(methodAddress)
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, 0xdeadbeef); err != nil {
		t.Fatal(err)
	}
	low, err := HostJavaMethod(
		"java/lang/Runtime",
		"totalMemory",
		"()J",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	high, err := runtime.CPU.ReadRegister(cpu.RegisterR1)
	if err != nil {
		t.Fatal(err)
	}
	if low != guest.HeapSize || high != 0 {
		t.Fatalf(
			"Runtime.totalMemory = 0x%08x%08x, want 0x%016x",
			high,
			low,
			uint64(guest.HeapSize),
		)
	}

	const dateValue = uint64(0x1122334455667788)
	runtime.dates[1] = int64(dateValue)
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, 1); err != nil {
		t.Fatal(err)
	}
	low, err = HostJavaMethod(
		"java/util/Date",
		"getTime",
		"()J",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	high, err = runtime.CPU.ReadRegister(cpu.RegisterR1)
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, 23); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 17); err != nil {
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
	source := runtime.images[imageObject].(draw.Image)
	source.Set(4, 5, color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff})
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, imageObject); err != nil {
		t.Fatal(err)
	}
	copyObject, err := runtime.handleImageMethod(
		"createImage",
		"(Lorg/kwis/msp/lcdui/Image;)Lorg/kwis/msp/lcdui/Image;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if copyObject == imageObject ||
		runtime.imageServices[copyObject] == runtime.imageServices[imageObject] {
		t.Fatalf(
			"copied image aliases source: object=%#x service=%s",
			copyObject,
			runtime.imageServices[copyObject],
		)
	}
	if got := color.RGBAModel.Convert(runtime.images[copyObject].At(4, 5)); got != (color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}) {
		t.Fatalf("copied image pixel = %v", got)
	}
	source.Set(4, 5, color.RGBA{R: 0xff, A: 0xff})
	if got := color.RGBAModel.Convert(runtime.images[copyObject].At(4, 5)); got != (color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}) {
		t.Fatalf("copied image changed with source = %v", got)
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	value, err := runtime.NewJavaString("  가abc  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, value); err != nil {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, trimmed); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 1); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 4); err != nil {
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
	unicodeValue, err := runtime.NewJavaString(
		"\uac00\ub098\ub2e4\U0001f600",
	)
	if err != nil {
		t.Fatal(err)
	}
	needle, err := runtime.NewJavaString("\ub2e4")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, unicodeValue); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, needle); err != nil {
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
	delimited, err := runtime.NewJavaString("abc\x00def\x00")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, delimited); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 0); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 4); err != nil {
		t.Fatal(err)
	}
	index, err = runtime.handleStringMethod("indexOf", "(II)I")
	if err != nil {
		t.Fatal(err)
	}
	if index != 7 {
		t.Fatalf("String.indexOf(NUL, 4) = %d, want 7", int32(index))
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, unicodeValue); err != nil {
		t.Fatal(err)
	}
	length, err := runtime.handleStringMethod("length", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if length != 5 {
		t.Fatalf("UTF-16 String.length = %d, want 5", length)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 3); err != nil {
		t.Fatal(err)
	}
	character, err := runtime.handleStringMethod("charAt", "(I)C")
	if err != nil {
		t.Fatal(err)
	}
	if character != 0xd83d {
		t.Fatalf("UTF-16 String.charAt = 0x%04x, want high surrogate", character)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 5); err != nil {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, substring); err != nil {
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
	korean, err := runtime.NewHostJavaObject("java/lang/String")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, korean); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, koreanArray); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleStringMethod("<init>", "([B)V"); err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(korean); got != "가" {
		t.Fatalf("EUC-KR String(byte[]) = %q, want %q", got, "가")
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, korean); err != nil {
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
	empty, err := runtime.NewHostJavaObject("java/lang/String")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, empty); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleStringMethod("<init>", "([BII)V"); err != nil {
		t.Fatal(err)
	}
	if got := runtime.javaStringValue(empty); got != "" {
		t.Fatalf("String(null) = %q, want empty compatibility string", got)
	}
	if err := runtime.CPU.WriteRegister(
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
	stringClass, err := runtime.InspectJavaClass(
		runtime.JavaClasses["java/lang/String"],
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
	untracked, err := runtime.NewHostJavaObject("java/lang/String")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, untracked); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, untracked); err != nil {
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
	left, err := runtime.NewJavaString("/3")
	if err != nil {
		t.Fatal(err)
	}
	right, err := runtime.NewJavaString("/1")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, left); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, right); err != nil {
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
	supplementary, err := runtime.NewJavaString("\U0001f600")
	if err != nil {
		t.Fatal(err)
	}
	privateUse, err := runtime.NewJavaString("\ue000")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, supplementary); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, privateUse); err != nil {
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
	ascii, err := runtime.NewJavaString("abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, ascii); err != nil {
		t.Fatal(err)
	}
	hash, err := runtime.handleStringMethod("hashCode", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if int32(hash) != 96354 {
		t.Fatalf("String.hashCode(\"abc\") = %d, want 96354", int32(hash))
	}
	if err := runtime.CPU.WriteRegister(
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runtime.NewHostJavaObject("java/io/ByteArrayOutputStream")
	if err != nil {
		t.Fatal(err)
	}
	data, err := runtime.newJavaByteArray([]byte{4, 5, 6})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, output); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleByteArrayOutputStreamMethod("<init>", "()V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, data); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}

	text, err := runtime.NewJavaString("-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, text); err != nil {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, 0x79); err != nil {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, ^uint32(0)); err != nil {
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

	random, err := runtime.NewHostJavaObject("java/util/Random")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, random); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleRandomMethod("<init>", "()V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 7); err != nil {
		t.Fatal(err)
	}
	randomValue, err := runtime.handleRandomMethod("nextInt", "(I)I")
	if err != nil {
		t.Fatal(err)
	}
	if randomValue >= 7 {
		t.Fatalf("Random.nextInt(7) = %d", randomValue)
	}
	for range 128 {
		shortLived, valueErr := runtime.NewHostJavaObject("java/util/Random")
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		if valueErr := runtime.CPU.WriteRegister(
			cpu.RegisterR1,
			shortLived,
		); valueErr != nil {
			t.Fatal(valueErr)
		}
		if _, valueErr := runtime.handleRandomMethod(
			"<init>",
			"()V",
		); valueErr != nil {
			t.Fatal(valueErr)
		}
	}
	if streams := runtime.Services.Random.Snapshot().Streams; len(streams) != 0 {
		t.Fatalf("short-lived Java Random objects allocated service streams: %v", streams)
	}

	target, err := runtime.NewHostJavaObject("java/io/ByteArrayOutputStream")
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := runtime.NewHostJavaObject("java/io/DataOutputStream")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, wrapper); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, target); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleOutputStreamMethod(
		"<init>",
		"(Ljava/io/OutputStream;)V",
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 0x01020304); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleOutputStreamMethod("writeInt", "(I)V"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(runtime.outputStreams[target], []byte{1, 2, 3, 4}) {
		t.Fatalf("DataOutputStream bytes = %x", runtime.outputStreams[target])
	}

	input, err := runtime.NewHostJavaObject("java/io/ByteArrayInputStream")
	if err != nil {
		t.Fatal(err)
	}
	inputData, err := runtime.newJavaByteArray([]byte{0x01, 0x02, 0x03, 0x04})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, input); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, inputData); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client: []byte{
			0x7f, 0x20, // movs r0, #0x7f
			0x70, 0x47, // bx lr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}

	classAddress, err := runtime.EnsureJavaClass("test/ApplicationInputStream")
	if err != nil {
		t.Fatal(err)
	}
	class, err := runtime.InspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	method, err := runtime.addHostJavaMethod(class, "read", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(method, ImageBase|1); err != nil {
		t.Fatal(err)
	}
	delete(runtime.hostJavaClass, classAddress)
	class, err = runtime.InspectJavaClass(classAddress)
	if err != nil {
		t.Fatal(err)
	}
	source, err := runtime.NewJavaInstanceForClass(class)
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := runtime.NewHostJavaObject("java/io/DataInputStream")
	if err != nil {
		t.Fatal(err)
	}
	runtime.inputTargets[wrapper] = source
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, wrapper); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}

	file, err := runtime.NewHostJavaObject("org/kwis/msp/io/File")
	if err != nil {
		t.Fatal(err)
	}
	filename, err := runtime.NewJavaString("save/data.bin")
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: file,
		cpu.RegisterR2: filename,
		cpu.RegisterR3: 3,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
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
	stack := guest.DefaultStackBase + 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(stack, 3); err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: file,
		cpu.RegisterR2: source,
		cpu.RegisterR3: 0,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleFileMethod("seek", "(I)V"); err != nil {
		t.Fatal(err)
	}
	target, err := runtime.newJavaByteArray(make([]byte, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, target); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	file, err := runtime.NewHostJavaObject("org/kwis/msp/io/File")
	if err != nil {
		t.Fatal(err)
	}
	filename, err := runtime.NewJavaString("missing.dat")
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: file,
		cpu.RegisterR2: filename,
		cpu.RegisterR3: ktfFileReadOnly,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
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

	runtime.FileData["/missing.dat"] = []byte{}
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

func TestKTFFileSystemReportsAvailableStorage(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	free := runtime.ktfFreeStorageBytes()
	if free == 0 {
		t.Fatal("empty private storage reports no free space")
	}
	available, err := runtime.handleFileSystemMethod("available", "()I")
	if err != nil {
		t.Fatal(err)
	}
	if available != uint32(min(free, math.MaxInt32)) {
		t.Fatalf("FileSystem.available = %d, want %d", available, free)
	}
	low, err := runtime.handleFileSystemMethod("getFreeSpace", "()J")
	if err != nil {
		t.Fatal(err)
	}
	if uint64(runtime.JavaReturnHigh)<<32|uint64(low) != free {
		t.Fatalf(
			"getFreeSpace = 0x%08x%08x, want %d",
			runtime.JavaReturnHigh,
			low,
			free,
		)
	}
	// An unmodeled method must surface in diagnostics rather than passing a
	// silent zero back to the Clet as if it were an answer.
	if _, err := runtime.handleFileSystemMethod(
		"unmodeledProbe",
		"()I",
	); err != nil {
		t.Fatal(err)
	}
	const signature = "org/kwis/msp/io/FileSystem.unmodeledProbe()I"
	if runtime.UnimplementedJava[signature] != 1 ||
		runtime.LastUnimplementedJava != signature {
		t.Fatalf(
			"unimplemented record = %v / %q",
			runtime.UnimplementedJava,
			runtime.LastUnimplementedJava,
		)
	}
}

func TestKTFFileSystemRemoveClosesMatchingJavaFile(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	const filename = "/install.dat"
	if err := runtime.Services.Storage.WriteFile(
		shared.NamespacePrivate,
		filename,
		[]byte("installed"),
	); err != nil {
		t.Fatal(err)
	}
	runtime.FileData[filename] = []byte("installed")
	file, err := runtime.NewHostJavaObject("org/kwis/msp/io/File")
	if err != nil {
		t.Fatal(err)
	}
	name, err := runtime.NewJavaString(filename)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: file,
		cpu.RegisterR2: name,
		cpu.RegisterR3: ktfFileReadOnly,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleFileMethod(
		"<init>",
		"(Ljava/lang/String;I)V",
	); err != nil {
		t.Fatal(err)
	}
	serviceID := runtime.fileServices[file]
	if serviceID == 0 {
		t.Fatal("File constructor did not open shared storage")
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, name); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleFileSystemMethod(
		"remove",
		"(Ljava/lang/String;)V",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Services.Storage.Stat(
		shared.NamespacePrivate,
		filename,
	); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("removed file stat error = %v", err)
	}
	if !runtime.files[file].closed ||
		runtime.fileServices[file] != 0 ||
		runtime.FileData[filename] != nil {
		t.Fatalf(
			"removed Java file state: file=%+v service=%d data=%v",
			runtime.files[file],
			runtime.fileServices[file],
			runtime.FileData[filename],
		)
	}
	if err := runtime.Services.Storage.Close(
		runtime.ServiceOwner,
		serviceID,
	); !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("removed file service remained open: %v", err)
	}
}

func TestKTFDataBaseHonorsCreateFlag(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	name, err := runtime.NewJavaString("save")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, name); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 64); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 0); err != nil {
		t.Fatal(err)
	}
	database, err := runtime.handleDataBaseMethod(
		context.Background(),
		"openDataBase",
		"(Ljava/lang/String;IZ)Lorg/kwis/msp/db/DataBase;",
	)
	if err == nil ||
		!strings.Contains(err.Error(), "org/kwis/msp/db/DataBaseException") {
		t.Fatalf(
			"open missing database without create = 0x%08x, err=%v, store=%v",
			database,
			err,
			runtime.DatabaseStores["save"],
		)
	}
	if !strings.Contains(err.Error(), "detail=0x1000") {
		t.Fatalf("missing database exception has no guest object: %v", err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 1); err != nil {
		t.Fatal(err)
	}
	database, err = runtime.handleDataBaseMethod(
		context.Background(),
		"openDataBase",
		"(Ljava/lang/String;IZ)Lorg/kwis/msp/db/DataBase;",
	)
	if err != nil {
		t.Fatal(err)
	}
	if database == 0 || runtime.DatabaseStores["save"] == nil {
		t.Fatalf(
			"create database = 0x%08x, store=%v",
			database,
			runtime.DatabaseStores["save"],
		)
	}
}

func TestKTFCollectionsReturnStoredObjectsAndEnumeration(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := runtime.NewHostJavaObject("java/util/Vector")
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtime.NewJavaString("first")
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := runtime.NewJavaString("inserted")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, vector); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleVectorMethod("<init>", "(II)V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, first); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleVectorMethod(
		"addElement",
		"(Ljava/lang/Object;)V",
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, inserted); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleVectorMethod(
		"insertElementAt",
		"(Ljava/lang/Object;I)V",
	); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Vectors[vector]; !slices.Equal(
		got,
		[]uint32{inserted, first},
	) {
		t.Fatalf("Vector insertion = %08x", got)
	}
	target, err := runtime.newJavaReferenceArray(
		"[Ljava/lang/Object;",
		[]uint32{0, 0, 0},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, target); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleVectorMethod(
		"copyInto",
		"([Ljava/lang/Object;)V",
	); err != nil {
		t.Fatal(err)
	}
	fields, err := runtime.ReadU32(target)
	if err != nil {
		t.Fatal(err)
	}
	copied, err := runtime.ReadWords(fields+8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(copied, []uint32{inserted, first, 0}) {
		t.Fatalf("Vector.copyInto = %08x", copied)
	}

	table, err := runtime.NewHostJavaObject("java/util/Hashtable")
	if err != nil {
		t.Fatal(err)
	}
	key, err := runtime.NewJavaString("key")
	if err != nil {
		t.Fatal(err)
	}
	value, err := runtime.NewJavaString("value")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, table); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleHashtableMethod("<init>", "()V"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, key); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, value); err != nil {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, enumeration); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.JvmContext, err = runtime.AllocateWords(3 + 128)
	if err != nil {
		t.Fatal(err)
	}

	card, err := runtime.NewHostJavaObject("org/kwis/msp/lcdui/Card")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, card); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, ^uint32(4)); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 37); err != nil {
		t.Fatal(err)
	}
	if _, err := HostJavaMethod(
		"org/kwis/msp/lcdui/Card",
		"move",
		"(II)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	x, err := HostJavaMethod(
		"org/kwis/msp/lcdui/Card",
		"getX",
		"()I",
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	y, err := HostJavaMethod(
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

	source, err := runtime.NewHostJavaObject("java/io/InputStream")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := runtime.NewHostJavaObject("java/io/InputStreamReader")
	if err != nil {
		t.Fatal(err)
	}
	runtime.inputStreams[source] = &ktfInputStream{data: []byte("ABC")}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, reader); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, source); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleInputStreamReaderMethod(
		"<init>",
		"(Ljava/io/InputStream;)V",
	); err != nil {
		t.Fatal(err)
	}
	characters, err := runtime.NewJavaArray("[C", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, characters); err != nil {
		t.Fatal(err)
	}
	read, err := runtime.handleInputStreamReaderMethod("read", "([C)I")
	if err != nil {
		t.Fatal(err)
	}
	characterFields, err := runtime.ReadU32(characters)
	if err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, 6)
	if err := runtime.CPU.ReadMemory(characterFields+8, encoded); err != nil {
		t.Fatal(err)
	}
	if read != 3 ||
		binary.LittleEndian.Uint16(encoded[0:2]) != 'A' ||
		binary.LittleEndian.Uint16(encoded[2:4]) != 'B' ||
		binary.LittleEndian.Uint16(encoded[4:6]) != 'C' {
		t.Fatalf("Reader.read(char[]) = %d, %x", read, encoded)
	}

	number, err := runtime.NewJavaString("-9223372036854775808")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, number); err != nil {
		t.Fatal(err)
	}
	low, err := runtime.handleLongMethod(
		"parseLong",
		"(Ljava/lang/String;)J",
	)
	if err != nil {
		t.Fatal(err)
	}
	if low != 0 || runtime.JavaReturnHigh != 0x80000000 {
		t.Fatalf(
			"Long.parseLong = 0x%08x%08x, want 0x8000000000000000",
			runtime.JavaReturnHigh,
			low,
		)
	}

	throwable, err := runtime.NewHostJavaObject("java/lang/Throwable")
	if err != nil {
		t.Fatal(err)
	}
	message, err := runtime.NewJavaString("boom")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, throwable); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, message); err != nil {
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
	if trace := runtime.HostTrace[len(runtime.HostTrace)-1]; !strings.Contains(
		trace,
		`java_stack_trace:`,
	) || !strings.Contains(trace, `message="boom"`) {
		t.Fatalf("Throwable.printStackTrace trace = %q", trace)
	}

	clip, err := runtime.NewHostJavaObject("org/kwis/msp/media/Clip")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Pkg.Resources == nil {
		runtime.Pkg.Resources = make(map[string][]byte)
	}
	runtime.Pkg.Resources["snd/test.mmf"] = []byte{0x4d, 0x4d, 0x4d, 0x44}
	resourceName, err := runtime.NewJavaString("/snd/test.mmf")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, clip); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, resourceName); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleMediaMethod(
		"<init>",
		"(Ljava/lang/String;)V",
	); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(
		runtime.clips[clip].data,
		runtime.Pkg.Resources["snd/test.mmf"],
	) {
		t.Fatalf("Clip resource constructor data = %x", runtime.clips[clip].data)
	}
	clip, err = runtime.NewHostJavaObject("org/kwis/msp/media/Clip")
	if err != nil {
		t.Fatal(err)
	}
	sourceData, err := runtime.newJavaByteArray([]byte{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, clip); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, sourceData); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 1); err != nil {
		t.Fatal(err)
	}
	stack, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{2}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, targetData); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR3, 1); err != nil {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, clip); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin64",
		BSSSize:    64,
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	name, err := runtime.allocateBytes(
		[]byte("MXUserMemInterf"),
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	returnMajor, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	returnMinor, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	stack := guest.DefaultStackBase + 0x100
	if err := runtime.WriteU32(stack, returnMinor); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{
		name,
		^uint32(0),
		^uint32(0),
		returnMajor,
	} {
		if err := runtime.CPU.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
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
	versions, err := runtime.ReadWords(returnMajor, 1)
	if err != nil {
		t.Fatal(err)
	}
	minor, err := runtime.ReadU32(returnMinor)
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

	callbacks, err := runtime.ReadWords(address, 4)
	if err != nil {
		t.Fatal(err)
	}
	host, ok := runtime.hostCalls[callbacks[0]&^1]
	if !ok {
		t.Fatalf("MXUserMem add callback 0x%08x is not registered", callbacks[0])
	}
	const (
		regionBase = ImageBase + 8
		regionSize = 32
	)
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, regionBase); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, regionSize); err != nil {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, regionBase); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, 7); err != nil {
		t.Fatal(err)
	}
	allocation, err := allocate.handler(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if allocation != regionBase ||
		runtime.incrementalHeaps[regionBase].Allocations[allocation] != 8 {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, regionBase); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, allocation); err != nil {
		t.Fatal(err)
	}
	if value, err := free.handler(context.Background(), runtime); err != nil {
		t.Fatal(err)
	} else if value != 0 {
		t.Fatalf("MXUserMem free = 0x%08x", value)
	}
	if _, ok := runtime.incrementalHeaps[regionBase].Allocations[allocation]; ok {
		t.Fatalf("freed MXUserMem allocation 0x%08x remains registered", allocation)
	}
}

func TestKTFWIPICKernelMemoryIDCopiesResourceToIndirectBuffer(t *testing.T) {
	resource := []byte{0x18, 0xba, 0x72, 0x00, 0xff}
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
		Resources: map[string][]byte{
			"assets/18.BAR": resource,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}

	name, err := runtime.allocateBytes([]byte("18.bar"), true)
	if err != nil {
		t.Fatal(err)
	}
	sizeAddress, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, name); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, sizeAddress); err != nil {
		t.Fatal(err)
	}
	resourceID, err := ktfGetResourceID(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	size, err := runtime.ReadU32(sizeAddress)
	if err != nil {
		t.Fatal(err)
	}
	if resourceID == 0 || size != uint32(len(resource)) {
		t.Fatalf("resource ID = %d, size = %d", resourceID, size)
	}

	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, size); err != nil {
		t.Fatal(err)
	}
	memoryID, err := ktfKernelAllocate(true)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	header, err := runtime.ReadWords(memoryID, 2)
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
	if err := runtime.CPU.ReadMemory(allocation.data, before); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, make([]byte, len(resource))) {
		t.Fatalf("calloc data before resource copy = %x", before)
	}

	for register, value := range []uint32{resourceID, memoryID, size} {
		if err := runtime.CPU.WriteRegister(
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
	if err := runtime.CPU.ReadMemory(allocation.data, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, resource) {
		t.Fatalf("resource at indirect buffer = %x, want %x", got, resource)
	}

	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, memoryID); err != nil {
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

	guestHandle, err := runtime.Heap.Allocate(size+12, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(guestHandle, guestHandle+4); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{resourceID, guestHandle, size} {
		if err := runtime.CPU.WriteRegister(
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
	if head, err := runtime.ReadU32(guestHandle); err != nil {
		t.Fatal(err)
	} else if head != guestHandle+4 {
		t.Fatalf(
			"guest indirect buffer handle overwritten with 0x%08x",
			head,
		)
	}
	got = make([]byte, len(resource))
	if err := runtime.CPU.ReadMemory(guestHandle+12, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, resource) {
		t.Fatalf(
			"resource at guest indirect buffer = %x, want %x",
			got,
			resource,
		)
	}

	direct, err := runtime.Heap.Allocate(size, true)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{resourceID, direct, size} {
		if err := runtime.CPU.WriteRegister(
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
	if err := runtime.CPU.ReadMemory(direct, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, resource) {
		t.Fatalf("resource at direct buffer = %x, want %x", got, resource)
	}
}

func TestKTFWIPICKernelSprintkFormatsResourcePath(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	destination, err := runtime.Heap.Allocate(128, true)
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
		if err := runtime.CPU.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	stack, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{^uint32(6)}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	key, err := runtime.allocateBytes([]byte("PHONEMODEL"), true)
	if err != nil {
		t.Fatal(err)
	}
	output, err := runtime.Heap.Allocate(32, true)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{key, output, 32} {
		if err := runtime.CPU.WriteRegister(
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
		if err := runtime.CPU.WriteRegister(
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
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
		if err := runtime.CPU.WriteRegister(
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
		if err := runtime.CPU.WriteRegister(
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
		if err := runtime.CPU.WriteRegister(
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
		if err := runtime.CPU.WriteRegister(
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
	if err := runtime.CPU.ReadMemory(output, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Fatalf("MC_fsRead bytes = %v", got)
	}

	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, handle); err != nil {
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

func TestKTFWIPICMediaMA3LoadAndPlayback(t *testing.T) {
	for slot, want := range map[int]ktfHostHandler{
		0:  ktfWIPICMediaCreate,
		3:  ktfWIPICMediaDestroy,
		4:  ktfWIPICMediaPutData,
		7:  ktfWIPICMediaClearData,
		8:  ktfWIPICMediaPlay,
		9:  ktfWIPICMediaPause,
		10: ktfWIPICMediaResume,
		11: ktfWIPICMediaStop,
	} {
		if got := ktfWIPICHandler(ktfWIPICMasterMedia, slot); reflect.ValueOf(
			got,
		).Pointer() != reflect.ValueOf(want).Pointer() {
			t.Fatalf("WIPI-C media slot %d has the wrong handler", slot)
		}
	}

	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	writeParameters := func(values ...uint32) {
		t.Helper()
		for index, value := range values {
			if err := runtime.CPU.WriteRegister(
				cpu.RegisterR0+uint32(index),
				value,
			); err != nil {
				t.Fatal(err)
			}
		}
	}

	mediaType, err := runtime.allocateBytes([]byte("Yamaha_MA3"), true)
	if err != nil {
		t.Fatal(err)
	}
	writeParameters(mediaType, 4096, ImageBase|1)
	handle, err := ktfWIPICMediaCreate(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	clip := runtime.wipicMediaClips[handle]
	serviceID := runtime.wipicMediaServices[handle]
	if handle == 0 || clip == nil || serviceID == 0 ||
		clip.mediaType != "audio/x-smaf" ||
		clip.capacity != 4096 ||
		clip.callback != ImageBase|1 {
		t.Fatalf(
			"WIPI-C media clip handle=%08x service=%d clip=%+v",
			handle,
			serviceID,
			clip,
		)
	}

	source := []byte("MMMD-test")
	input, err := runtime.allocateBytes(source, false)
	if err != nil {
		t.Fatal(err)
	}
	writeParameters(handle)
	if result, err := ktfWIPICMediaClearData(
		context.Background(),
		runtime,
	); err != nil || result != 0 {
		t.Fatalf("clear result=%08x err=%v", result, err)
	}
	writeParameters(handle, input, uint32(len(source)))
	if result, err := ktfWIPICMediaPutData(
		context.Background(),
		runtime,
	); err != nil || result != 0 {
		t.Fatalf("put result=%08x err=%v", result, err)
	}
	stored, err := runtime.Services.Media.Source(runtime.ServiceOwner, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, source) {
		t.Fatalf("WIPI-C media source = %q", stored)
	}

	writeParameters(handle, 1)
	if result, err := ktfWIPICMediaPlay(
		context.Background(),
		runtime,
	); err != nil || result != 0 {
		t.Fatalf("play result=%08x err=%v", result, err)
	}
	info, err := runtime.Services.Media.Info(runtime.ServiceOwner, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != shared.ClipPlaying || info.RemainingPlays != -1 {
		t.Fatalf("WIPI-C media playback = %+v", info)
	}

	writeParameters(handle)
	if _, err := ktfWIPICMediaStop(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICMediaDestroy(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.wipicMediaClips[handle] != nil ||
		runtime.wipicMediaServices[handle] != 0 {
		t.Fatal("WIPI-C media clip survived destruction")
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

	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	writeParameters := func(values ...uint32) {
		t.Helper()
		for index, value := range values {
			if err := runtime.CPU.WriteRegister(
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
	if err := runtime.Services.Storage.WriteFile(
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
	if err := runtime.CPU.ReadMemory(output, listed); err != nil {
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
	if err := runtime.Services.Storage.Delete(
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 8, 6))

	framebufferAddress, err := runtime.EnsureWIPICScreenFramebuffer()
	if err != nil {
		t.Fatal(err)
	}
	framebuffer := runtime.wipicFramebuffers[framebufferAddress]
	objectBody, err := runtime.ReadU32(framebufferAddress)
	if err != nil {
		t.Fatal(err)
	}
	body, err := runtime.ReadWords(objectBody, 7)
	if err != nil {
		t.Fatal(err)
	}
	pixelHeader, err := runtime.ReadU32(body[6])
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

	graphicsContext, err := runtime.AllocateWords(15)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, graphicsContext); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICGraphicsInitContext(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(graphicsContext+20, 0xf800); err != nil {
		t.Fatal(err)
	}
	stack, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{2, graphicsContext}); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{framebufferAddress, 2, 1, 3} {
		if err := runtime.CPU.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
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
	if runtime.PresentCount != 1 {
		t.Fatalf("WIPI-C present count = %d", runtime.PresentCount)
	}
}

func TestKTFJavaPresentationConsumesPendingWIPICScreen(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	runtime.frame = image.NewRGBA(image.Rect(0, 0, 8, 6))
	graphics, err := runtime.EnsureScreenGraphics()
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.syncKTFGraphics(graphics); err != nil {
		t.Fatal(err)
	}
	framebufferAddress, err := runtime.EnsureWIPICScreenFramebuffer()
	if err != nil {
		t.Fatal(err)
	}
	framebuffer := runtime.wipicFramebuffers[framebufferAddress]
	var pixel [2]byte
	binary.LittleEndian.PutUint16(pixel[:], 0xf800)
	if err := runtime.CPU.WriteMemory(
		framebuffer.pixels+uint32(framebuffer.stride+2*2),
		pixel[:],
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.commitKTFWIPICFramebuffer(framebufferAddress); err != nil {
		t.Fatal(err)
	}
	if !runtime.WipicScreenPending {
		t.Fatal("WIPI-C screen write was not queued for the Java paint boundary")
	}
	if err := runtime.RecordPresentation(); err != nil {
		t.Fatal(err)
	}
	if got := runtime.frame.RGBAAt(2, 1); got != (color.RGBA{
		R: 0xff,
		A: 0xff,
	}) {
		t.Fatalf("merged WIPI-C screen pixel = %#v", got)
	}
	if runtime.WipicScreenPending {
		t.Fatal("presented WIPI-C screen remained pending")
	}
	if got := runtime.Services.Graphics.Screen(); got != runtime.GraphicsServices[graphics] {
		t.Fatalf(
			"presented service = %s, want Java screen %s",
			got,
			runtime.GraphicsServices[graphics],
		)
	}
	if runtime.PresentCount != 1 {
		t.Fatalf("Java present count = %d, want 1", runtime.PresentCount)
	}
}

func TestKTFWIPICOffscreenFramebufferLifecycle(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 32); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, 24); err != nil {
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
	screen, err := runtime.EnsureWIPICScreenFramebuffer()
	if err != nil {
		t.Fatal(err)
	}
	contextAddress, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(contextAddress, []uint32{
		0, 0, 32, 24, 1, 0xffff,
	}); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{object, 0, 0, 32} {
		if err := runtime.CPU.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	stack, err := runtime.Heap.Allocate(20, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{
		24, contextAddress, 0, 0, 0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
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
	if got := runtime.Services.Graphics.Screen(); got != screenSurface {
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
		if err := runtime.CPU.WriteRegister(
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
	if err := runtime.CPU.ReadMemory(
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, object); err != nil {
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
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(
		cpu.RegisterR0,
		uint32(encoded.Len()),
	); err != nil {
		t.Fatal(err)
	}
	memoryID, err := ktfKernelAllocate(true)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(
		runtime.wipicMemory[memoryID].data,
		encoded.Bytes(),
	); err != nil {
		t.Fatal(err)
	}
	output, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{
		output,
		memoryID,
		0,
		uint32(encoded.Len()),
	} {
		if err := runtime.CPU.WriteRegister(
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
	object, err := runtime.ReadU32(output)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range map[uint32]uint32{4: 2, 5: 1, 6: 16} {
		if err := runtime.CPU.WriteRegister(cpu.RegisterR0, object); err != nil {
			t.Fatal(err)
		}
		if err := runtime.CPU.WriteRegister(cpu.RegisterR1, index); err != nil {
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
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, object); err != nil {
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
	if err := runtime.CPU.ReadMemory(
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

	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(
		cpu.RegisterR0,
		uint32(len(encoded)),
	); err != nil {
		t.Fatal(err)
	}
	memoryID, err := ktfKernelAllocate(true)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(
		runtime.wipicMemory[memoryID].data,
		encoded,
	); err != nil {
		t.Fatal(err)
	}
	output, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{
		output,
		memoryID,
		0,
		uint32(len(encoded)),
	} {
		if err := runtime.CPU.WriteRegister(
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
	object, err := runtime.ReadU32(output)
	if err != nil {
		t.Fatal(err)
	}
	body, err := runtime.ReadU32(object)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.wipicImages[object].body; body != got {
		t.Fatalf("BMP image body = %08x; want %08x", body, got)
	}
	framebufferObject := runtime.wipicImages[object].framebuffer
	if got, err := runtime.ReadU32(body + 8); err != nil || got != framebufferObject {
		t.Fatalf(
			"BMP image nested framebuffer = %08x, err=%v; want %08x",
			got,
			err,
			framebufferObject,
		)
	}
	if got, err := runtime.ReadU32(body + 12); err != nil || got != 0 {
		t.Fatalf("BMP image nested mask = %08x, err=%v; want 00000000", got, err)
	}
	framebuffer := runtime.wipicFramebuffers[framebufferObject]
	var pixels [4]byte
	if err := runtime.CPU.ReadMemory(framebuffer.pixels, pixels[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(pixels[:2]); got != 0xf800 {
		t.Fatalf("decoded BMP red pixel = %04x", got)
	}
	if got := binary.LittleEndian.Uint16(pixels[2:]); got != 0x07e0 {
		t.Fatalf("decoded BMP green pixel = %04x", got)
	}
}

func TestKTFWIPICEncodeImageReturnsIndirectBMPBuffer(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	framebufferObject, err := runtime.createWIPICFramebuffer(2, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	framebuffer := runtime.wipicFramebuffers[framebufferObject]
	var pixels [4]byte
	binary.LittleEndian.PutUint16(pixels[0:2], 0xf800)
	binary.LittleEndian.PutUint16(pixels[2:4], 0x07e0)
	if err := runtime.CPU.WriteMemory(framebuffer.pixels, pixels[:]); err != nil {
		t.Fatal(err)
	}
	lengthAddress, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	stack, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{1, lengthAddress}); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{framebufferObject, 0, 0, 2} {
		if err := runtime.CPU.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	memoryID, err := ktfWIPICHandler(
		ktfWIPICMasterGraphics,
		35,
	)(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	allocation, ok := runtime.wipicMemory[memoryID]
	if memoryID == 0 || !ok {
		t.Fatalf("encoded memory ID = 0x%08x, registered=%t", memoryID, ok)
	}
	words, err := runtime.ReadWords(memoryID, 2)
	if err != nil {
		t.Fatal(err)
	}
	length, err := runtime.ReadU32(lengthAddress)
	if err != nil {
		t.Fatal(err)
	}
	if words[0] != allocation.base || words[1] != allocation.size ||
		length != allocation.size || allocation.data != allocation.base+8 {
		t.Fatalf(
			"encoded memory words=%08x allocation=%+v length=%d",
			words,
			allocation,
			length,
		)
	}
	if allocation.size < 26 {
		t.Fatalf("encoded BMP size = %d", allocation.size)
	}
	header := make([]byte, 26)
	if err := runtime.CPU.ReadMemory(allocation.data, header); err != nil {
		t.Fatal(err)
	}
	if string(header[:2]) != "BM" ||
		binary.LittleEndian.Uint32(header[18:22]) != 2 ||
		binary.LittleEndian.Uint32(header[22:26]) != 1 {
		t.Fatalf("encoded BMP header = %x", header)
	}
}

func TestKTFWIPICDrawImageBlitsAndClips(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	destination, err := runtime.createWIPICFramebuffer(16, 16, false)
	if err != nil {
		t.Fatal(err)
	}
	source, err := runtime.createWIPICFramebuffer(4, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	sourceBuffer := runtime.wipicFramebuffers[source]
	pixels := make([]byte, sourceBuffer.stride*sourceBuffer.height)
	for offset := 0; offset < len(pixels); offset += 2 {
		binary.LittleEndian.PutUint16(pixels[offset:], 0x1234)
	}
	if err := runtime.CPU.WriteMemory(sourceBuffer.pixels, pixels); err != nil {
		t.Fatal(err)
	}
	const imageObject = uint32(0x4000)
	runtime.wipicImages[imageObject] = &ktfWIPICImage{
		object:         imageObject,
		framebuffer:    source,
		transparentKey: -1,
	}
	contextAddress, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}

	draw := func(clip []uint32) {
		t.Helper()
		if err := runtime.writeWords(contextAddress, clip); err != nil {
			t.Fatal(err)
		}
		for register, value := range []uint32{destination, 2, 3, 4} {
			if err := runtime.CPU.WriteRegister(
				cpu.RegisterR0+uint32(register),
				value,
			); err != nil {
				t.Fatal(err)
			}
		}
		stack, err := runtime.Heap.Allocate(20, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.writeWords(stack, []uint32{
			4,
			imageObject,
			0,
			0,
			contextAddress,
		}); err != nil {
			t.Fatal(err)
		}
		if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
			t.Fatal(err)
		}
		if _, err := ktfWIPICGraphicsDrawImage(
			context.Background(),
			runtime,
		); err != nil {
			t.Fatal(err)
		}
	}

	pixelAt := func(x, y int) uint16 {
		t.Helper()
		buffer := runtime.wipicFramebuffers[destination]
		var encoded [2]byte
		if err := runtime.CPU.ReadMemory(
			buffer.pixels+uint32(y*buffer.stride+x*2),
			encoded[:],
		); err != nil {
			t.Fatal(err)
		}
		return binary.LittleEndian.Uint16(encoded[:])
	}

	draw([]uint32{0, 0, 0, 0, 0})
	for _, point := range [][2]int{{2, 3}, {5, 3}, {2, 6}, {5, 6}} {
		if got := pixelAt(point[0], point[1]); got != 0x1234 {
			t.Fatalf("blitted pixel at %v = %04x", point, got)
		}
	}
	for _, point := range [][2]int{{1, 3}, {6, 3}, {2, 2}, {2, 7}} {
		if got := pixelAt(point[0], point[1]); got != 0 {
			t.Fatalf("pixel outside the blit at %v = %04x", point, got)
		}
	}

	// A clip that admits one column must leave the rest of the run alone.
	if err := runtime.CPU.WriteMemory(
		runtime.wipicFramebuffers[destination].pixels,
		make([]byte, runtime.wipicFramebuffers[destination].stride*16),
	); err != nil {
		t.Fatal(err)
	}
	draw([]uint32{2, 3, 3, 7, 1})
	if got := pixelAt(2, 3); got != 0x1234 {
		t.Fatalf("clipped pixel = %04x", got)
	}
	if got := pixelAt(3, 3); got != 0 {
		t.Fatalf("pixel beyond the clip = %04x", got)
	}
}

func TestKTFColorKeyMagentaFamily(t *testing.T) {
	for _, keyed := range []uint16{0xf81f, 0xf816, 0xf818, 0xf010} {
		if !ktfIsColorKeyMagenta565(keyed) {
			t.Errorf("0x%04x should read as a transparent color key", keyed)
		}
	}
	for _, opaque := range []uint16{0x0000, 0xffff, 0x001f, 0x07e0, 0xf800, 0xfc1f} {
		if ktfIsColorKeyMagenta565(opaque) {
			t.Errorf("0x%04x should not be treated as a color key", opaque)
		}
	}
}

// A color-keyed sprite (magenta background, decoded from a bitmap) must leave
// the destination showing through wherever the source is the key color, the
// way 이노티아's signal and battery icons draw over the title bar.
func TestKTFWIPICDrawImageKeysOutMagenta(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	const (
		background = uint16(0x001f) // blue, already on the destination
		sprite     = uint16(0x07e0) // green, the opaque icon pixels
		key        = uint16(0xf81f) // magenta, the transparent background
	)
	destination, err := runtime.createWIPICFramebuffer(8, 8, false)
	if err != nil {
		t.Fatal(err)
	}
	destinationBuffer := runtime.wipicFramebuffers[destination]
	filled := make([]byte, destinationBuffer.stride*destinationBuffer.height)
	for offset := 0; offset < len(filled); offset += 2 {
		binary.LittleEndian.PutUint16(filled[offset:], background)
	}
	if err := runtime.CPU.WriteMemory(destinationBuffer.pixels, filled); err != nil {
		t.Fatal(err)
	}
	source, err := runtime.createWIPICFramebuffer(2, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	sourceBuffer := runtime.wipicFramebuffers[source]
	sourcePixels := []uint16{key, sprite, sprite, sprite}
	sourceBytes := make([]byte, sourceBuffer.stride*sourceBuffer.height)
	for i, pixel := range sourcePixels {
		binary.LittleEndian.PutUint16(sourceBytes[(i/2)*sourceBuffer.stride+(i%2)*2:], pixel)
	}
	if err := runtime.CPU.WriteMemory(sourceBuffer.pixels, sourceBytes); err != nil {
		t.Fatal(err)
	}
	const imageObject = uint32(0x4000)
	runtime.wipicImages[imageObject] = &ktfWIPICImage{
		object:         imageObject,
		framebuffer:    source,
		transparentKey: int32(key),
	}
	contextAddress, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{destination, 1, 1, 2} {
		if err := runtime.CPU.WriteRegister(cpu.RegisterR0+uint32(register), value); err != nil {
			t.Fatal(err)
		}
	}
	stack, err := runtime.Heap.Allocate(20, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{2, imageObject, 0, 0, contextAddress}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICGraphicsDrawImage(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	pixelAt := func(x, y int) uint16 {
		var encoded [2]byte
		if err := runtime.CPU.ReadMemory(
			destinationBuffer.pixels+uint32(y*destinationBuffer.stride+x*2),
			encoded[:],
		); err != nil {
			t.Fatal(err)
		}
		return binary.LittleEndian.Uint16(encoded[:])
	}
	if got := pixelAt(1, 1); got != background {
		t.Fatalf("key pixel = %04x, want the background %04x to show through", got, background)
	}
	for _, point := range [][2]int{{2, 1}, {1, 2}, {2, 2}} {
		if got := pixelAt(point[0], point[1]); got != sprite {
			t.Fatalf("opaque pixel at %v = %04x, want %04x", point, got, sprite)
		}
	}
	if got := pixelAt(0, 0); got != background {
		t.Fatalf("pixel outside the blit = %04x", got)
	}
}

func TestKTFWIPICDrawStringPaintsMeasuredRun(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 64); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, 32); err != nil {
		t.Fatal(err)
	}
	object, err := ktfWIPICGraphicsCreateOffscreenFramebuffer(
		context.Background(),
		runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	contextAddress, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(contextAddress, []uint32{
		0, 0, 64, 32, 1, 0xf800,
	}); err != nil {
		t.Fatal(err)
	}
	// "가A" in the handset's EUC-KR encoding, terminated for a negative length.
	textAddress, err := runtime.Heap.Allocate(4, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(
		textAddress,
		[]byte{0xb0, 0xa1, 'A', 0},
	); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{0, textAddress, ^uint32(0)} {
		if err := runtime.CPU.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	width, err := ktfWIPICGraphicsGetStringWidth(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if width == 0 || width > 64 {
		t.Fatalf("measured EUC-KR run width = %d", width)
	}
	for register, value := range []uint32{object, 1, 1, textAddress} {
		if err := runtime.CPU.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	stack, err := runtime.Heap.Allocate(8, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{
		^uint32(0),
		contextAddress,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICGraphicsDrawString(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	framebuffer := runtime.wipicFramebuffers[object]
	pixels := make([]byte, framebuffer.stride*framebuffer.height)
	if err := runtime.CPU.ReadMemory(framebuffer.pixels, pixels); err != nil {
		t.Fatal(err)
	}
	painted, antialiased, right := 0, 0, 0
	for y := 0; y < framebuffer.height; y++ {
		for x := 0; x < framebuffer.width; x++ {
			value := binary.LittleEndian.Uint16(
				pixels[y*framebuffer.stride+x*2:],
			)
			if value == 0 {
				continue
			}
			if value&0x07ff != 0 {
				t.Fatalf("glyph pixel at %d,%d is not red: %04x", x, y, value)
			}
			painted++
			if value != 0xf800 {
				antialiased++
			}
			right = max(right, x)
		}
	}
	if painted == 0 {
		t.Fatal("drawn EUC-KR run painted no pixels")
	}
	if antialiased == 0 {
		t.Fatal("drawn EUC-KR run has no antialiased edge pixels")
	}
	if right >= 1+int(width) {
		t.Fatalf("run reached x=%d beyond measured width %d", right, width)
	}
}

// 드래곤로드 positions each menu label at clipTop+8 with a clip rectangle of
// exactly one 10-pixel text row, so DrawString must treat y as the baseline:
// a top-left origin leaves only the first two glyph rows inside the clip.
func TestKTFWIPICDrawStringAnchorsTheBaseline(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 64); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, 32); err != nil {
		t.Fatal(err)
	}
	object, err := ktfWIPICGraphicsCreateOffscreenFramebuffer(
		context.Background(),
		runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	const clipTop, clipBottom = 2, 12
	contextAddress, err := runtime.Heap.Allocate(60, true)
	if err != nil {
		t.Fatal(err)
	}
	// The 10-pixel handset font (handle 8) ascends 8 rows, so a baseline at
	// clipTop+8 keeps the whole glyph inside the one-row clip rectangle.
	if err := runtime.writeWords(contextAddress, []uint32{
		0, clipTop, 64, clipBottom, 1, 0xf800, 0, 0, 0, 0, 8,
	}); err != nil {
		t.Fatal(err)
	}
	// "가" in the handset's EUC-KR encoding, terminated for a negative length.
	textAddress, err := runtime.Heap.Allocate(3, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteMemory(
		textAddress,
		[]byte{0xb0, 0xa1, 0},
	); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{object, 1, clipTop + 8, textAddress} {
		if err := runtime.CPU.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	stack, err := runtime.Heap.Allocate(8, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(stack, []uint32{
		^uint32(0),
		contextAddress,
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, stack); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICGraphicsDrawString(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	framebuffer := runtime.wipicFramebuffers[object]
	pixels := make([]byte, framebuffer.stride*framebuffer.height)
	if err := runtime.CPU.ReadMemory(framebuffer.pixels, pixels); err != nil {
		t.Fatal(err)
	}
	paintedRows := map[int]bool{}
	for y := 0; y < framebuffer.height; y++ {
		for x := 0; x < framebuffer.width; x++ {
			value := binary.LittleEndian.Uint16(
				pixels[y*framebuffer.stride+x*2:],
			)
			if value != 0 {
				paintedRows[y] = true
			}
		}
	}
	if len(paintedRows) == 0 {
		t.Fatal("baseline-anchored run painted no pixels")
	}
	for y := range paintedRows {
		if y < clipTop || y >= clipBottom {
			t.Fatalf("glyph row %d escaped clip [%d,%d)", y, clipTop, clipBottom)
		}
	}
	if len(paintedRows) < 6 {
		t.Fatalf(
			"only %d glyph rows survived the clip; the run is drawn below its baseline",
			len(paintedRows),
		)
	}
}

func TestKTFWIPICFontMetricsFollowTheHandsetHandle(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	for register, value := range []uint32{0x20, 16, 1} {
		if err := runtime.CPU.WriteRegister(
			cpu.RegisterR0+uint32(register),
			value,
		); err != nil {
			t.Fatal(err)
		}
	}
	font, err := ktfWIPICGraphicsGetFont(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if font != 0x20|1<<8|16 {
		t.Fatalf("packed font handle = %08x", font)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, font); err != nil {
		t.Fatal(err)
	}
	height, err := ktfWIPICGraphicsGetFontHeight(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	ascent, err := ktfWIPICGraphicsGetFontAscent(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	descent, err := ktfWIPICGraphicsGetFontDescent(context.Background(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	if height != uint32(guest.FontHeight(font)) || ascent+descent != height {
		t.Fatalf(
			"font metrics height=%d ascent=%d descent=%d",
			height,
			ascent,
			descent,
		)
	}
	// A second handle with the same height and style reuses one text service.
	services := len(runtime.FontServices)
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, font|0x40); err != nil {
		t.Fatal(err)
	}
	if _, err := ktfWIPICGraphicsGetFontHeight(
		context.Background(),
		runtime,
	); err != nil {
		t.Fatal(err)
	}
	if len(runtime.FontServices) != services {
		t.Fatalf("font services = %d, want %d", len(runtime.FontServices), services)
	}
}

func TestKTFWIPICStringRejectsUnterminatedRun(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	address, err := runtime.Heap.Allocate(ktfWIPICStringLimit+16, true)
	if err != nil {
		t.Fatal(err)
	}
	filled := make([]byte, ktfWIPICStringLimit+16)
	for index := range filled {
		filled[index] = 'A'
	}
	if err := runtime.CPU.WriteMemory(address, filled); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.wipicText(address, -1, false); err == nil {
		t.Fatal("unterminated WIPI-C string was accepted")
	}
	if _, err := runtime.wipicText(
		address,
		int32(ktfWIPICStringLimit)+1,
		false,
	); err == nil {
		t.Fatal("oversized WIPI-C string was accepted")
	}
}

func TestKTFWIPICTimerFiresAndReusesCompletedTask(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	timerAddress, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	callback := ImageBase | 1
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, timerAddress); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, callback); err != nil {
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
			if err := runtime.CPU.WriteRegister(
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

	runtime.TickMS = 100
	setTimer(25)
	runtime.TickMS = 124
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 0 {
		t.Fatalf("timer fired before deadline: %d tasks", len(runtime.Tasks))
	}
	runtime.TickMS = 125
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 1 {
		t.Fatalf("timer produced %d tasks, want 1", len(runtime.Tasks))
	}
	if err := runtime.CPU.RestoreContext(runtime.Tasks[0].Context); err != nil {
		t.Fatal(err)
	}
	gotTimer, err := runtime.CPU.ReadRegister(cpu.RegisterR0)
	if err != nil {
		t.Fatal(err)
	}
	gotParameter, err := runtime.CPU.ReadRegister(cpu.RegisterR1)
	if err != nil {
		t.Fatal(err)
	}
	gotPC, err := runtime.CPU.ReadRegister(cpu.RegisterPC)
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

	runtime.Tasks[0].Done = true
	runtime.TickMS = 200
	setTimer(1)
	runtime.TickMS = 201
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 1 || runtime.Tasks[0].Done {
		t.Fatalf(
			"timer task was not reused: count=%d done=%t",
			len(runtime.Tasks),
			runtime.Tasks[0].Done,
		)
	}

	runtime.Tasks = make([]*Task, MaxTasks)
	for index := range runtime.Tasks {
		runtime.Tasks[index] = &Task{}
	}
	runtime.TickMS = 300
	setTimer(1)
	runtime.TickMS = 301
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if !runtime.wipicTimers[timerAddress].active {
		t.Fatal("timer was consumed while the task pool was full")
	}

	const reusableTask = 7
	runtime.Tasks[reusableTask].Done = true
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if runtime.wipicTimers[timerAddress].active {
		t.Fatal("timer remained active after a task slot became available")
	}
	if runtime.Tasks[reusableTask].Done {
		t.Fatal("timer callback did not reuse the completed task slot")
	}

	runtime.Tasks = []*Task{{KeyCard: 0x10002000}}
	runtime.wipicTimers = map[uint32]*ktfWIPICTimer{
		0x10003000: {
			callback: callback,
			deadline: 400,
			active:   true,
		},
	}
	runtime.TickMS = 400
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 1 ||
		!runtime.wipicTimers[0x10003000].active {
		t.Fatal("timer callback overlapped a card key event")
	}
	runtime.Tasks[0].Done = true
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 1 ||
		runtime.Tasks[0].Done ||
		!runtime.Tasks[0].WipicTimer ||
		runtime.wipicTimers[0x10003000].active {
		t.Fatal("timer callback did not run after card key event completed")
	}

	runtime.Tasks = nil
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
	runtime.TickMS = 400
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 1 || !runtime.Tasks[0].WipicTimer {
		t.Fatalf("first serialized timer tasks = %+v", runtime.Tasks)
	}
	if runtime.wipicTimers[0x10003000].active ||
		!runtime.wipicTimers[0x10004000].active {
		t.Fatalf("serialized timer states = %+v", runtime.wipicTimers)
	}
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 1 ||
		!runtime.wipicTimers[0x10004000].active {
		t.Fatal("second timer ran while the first callback was live")
	}
	runtime.Tasks[0].Done = true
	if err := runtime.activateDueWIPICTimers(); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 1 ||
		runtime.Tasks[0].Done ||
		!runtime.Tasks[0].WipicTimer ||
		runtime.wipicTimers[0x10004000].active {
		t.Fatal("second timer did not run after the first callback completed")
	}
}

func TestKTFEncodedImageKeepsStraightAlphaTransparent(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 0xff, B: 0xff, A: 0})
	source.SetNRGBA(1, 0, color.NRGBA{G: 0xff, A: 0xff})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	instance, err := runtime.newJavaEncodedImage(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	red, green, blue, alpha := runtime.images[instance].At(0, 0).RGBA()
	if red != 0 || green != 0 || blue != 0 || alpha != 0 {
		t.Fatalf(
			"transparent magenta RGBA16 = %04x,%04x,%04x,%04x",
			red,
			green,
			blue,
			alpha,
		)
	}
	red, green, blue, alpha = runtime.images[instance].At(1, 0).RGBA()
	if red != 0 || green != 0xffff || blue != 0 || alpha != 0xffff {
		t.Fatalf(
			"opaque green RGBA16 = %04x,%04x,%04x,%04x",
			red,
			green,
			blue,
			alpha,
		)
	}
}

func TestKTFClipSetVolumeUsesPercentageAndPreservesPlayback(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	clip, err := runtime.NewHostJavaObject("org/kwis/msp/media/Clip")
	if err != nil {
		t.Fatal(err)
	}
	state := runtime.ensureKTFClip(clip)
	if state.volume != 100 {
		t.Fatalf("default Clip volume = %d, want 100", state.volume)
	}
	source := []byte("MMMD-test")
	state.playing = true
	state.data = append([]byte(nil), source...)
	if err := runtime.syncKTFClip(clip); err != nil {
		t.Fatal(err)
	}
	serviceID := runtime.clipServices[clip]
	if err := runtime.Services.Media.Play(
		runtime.ServiceOwner,
		serviceID,
		-1,
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, clip); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR2, 33); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleMediaMethod("setVolume", "(I)Z"); err != nil {
		t.Fatal(err)
	}
	info, err := runtime.Services.Media.Info(runtime.ServiceOwner, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := runtime.Services.Media.Source(runtime.ServiceOwner, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != shared.ClipPlaying || info.RemainingPlays != -1 ||
		info.Volume != 33 || !bytes.Equal(stored, source) {
		t.Fatalf("Clip after setVolume: info=%+v source=%q", info, stored)
	}
}

func TestKTFClipServiceRecyclingSurvivesTheMediaPoolCap(t *testing.T) {
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.CPU.Close()
	limit := int(runtime.Services.Config.Limits.Media.MaxClips)
	if limit == 0 {
		t.Fatal("media clip limit is unset")
	}
	first := uint32(0)
	for index := range limit + 8 {
		instance := uint32(0x10000000 + index*0x100)
		if index == 0 {
			first = instance
		}
		runtime.clips[instance] = &ktfClip{volume: 5}
		if _, err := runtime.ensureKTFClipService(instance); err != nil {
			t.Fatalf("clip %d: %v", index, err)
		}
	}
	if len(runtime.clipServices) != limit {
		t.Fatalf("live clip services = %d, want %d", len(runtime.clipServices), limit)
	}
	if runtime.clipServices[first] != 0 {
		t.Fatal("the oldest idle clip kept its host service")
	}
	// A playing clip must never be recycled out from under the guest.
	playing := uint32(0x20000000)
	runtime.clips[playing] = &ktfClip{volume: 5, playing: true}
	if _, err := runtime.ensureKTFClipService(playing); err != nil {
		t.Fatal(err)
	}
	service := runtime.clipServices[playing]
	for index := range 8 {
		instance := uint32(0x30000000 + index*0x100)
		runtime.clips[instance] = &ktfClip{volume: 5}
		if _, err := runtime.ensureKTFClipService(instance); err != nil {
			t.Fatalf("post-playing clip %d: %v", index, err)
		}
	}
	if runtime.clipServices[playing] != service {
		t.Fatal("a playing clip lost its host service to recycling")
	}
}

func newScratchKTFRuntime(t *testing.T) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.CPU.Close() })
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	return runtime
}

func TestKTFInputStreamReaderDecodesEUCKRCharacters(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	const (
		reader = uint32(0x1234)
		source = uint32(0x5678)
	)
	runtime.inputTargets[reader] = source
	runtime.inputStreams[source] = &ktfInputStream{
		data: []byte{0xb0, 0xa1, 'A'}, // "가A" in EUC-KR.
	}
	characters, err := runtime.NewJavaArray("[C", 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	runtime.NativeParameterBase, err = runtime.AllocateWords(4)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(
		runtime.NativeParameterBase,
		[]uint32{reader, characters, 1, 2},
	); err != nil {
		t.Fatal(err)
	}
	read, err := runtime.handleInputStreamReaderMethod("read", "([CII)I")
	if err != nil {
		t.Fatal(err)
	}
	fields, err := runtime.ReadU32(characters)
	if err != nil {
		t.Fatal(err)
	}
	encoded := make([]byte, 8)
	if err := runtime.CPU.ReadMemory(fields+8, encoded); err != nil {
		t.Fatal(err)
	}
	if read != 2 ||
		binary.LittleEndian.Uint16(encoded[0:2]) != 0 ||
		binary.LittleEndian.Uint16(encoded[2:4]) != '가' ||
		binary.LittleEndian.Uint16(encoded[4:6]) != 'A' ||
		binary.LittleEndian.Uint16(encoded[6:8]) != 0 ||
		runtime.inputStreams[source].position != 3 {
		t.Fatalf(
			"Reader.read(char[], 1, 2) = %d, %x, position=%d",
			read,
			encoded,
			runtime.inputStreams[source].position,
		)
	}
	runtime.inputStreams[source].position = 0
	if err := runtime.writeWords(
		runtime.NativeParameterBase,
		[]uint32{reader, 1, 0},
	); err != nil {
		t.Fatal(err)
	}
	skipped, err := runtime.handleInputStreamReaderMethod("skip", "(J)J")
	if err != nil || skipped != 1 || runtime.inputStreams[source].position != 2 {
		t.Fatalf(
			"Reader.skip(1) = %d, position=%d, %v",
			skipped,
			runtime.inputStreams[source].position,
			err,
		)
	}
	if err := runtime.writeWords(runtime.NativeParameterBase, []uint32{reader}); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.handleInputStreamReaderMethod("read", "()I")
	if err != nil || value != 'A' || runtime.inputStreams[source].position != 3 {
		t.Fatalf(
			"Reader.read() = 0x%08x, position=%d, %v",
			value,
			runtime.inputStreams[source].position,
			err,
		)
	}
	value, err = runtime.handleInputStreamReaderMethod("read", "()I")
	if err != nil || value != ^uint32(0) {
		t.Fatalf("Reader.read() at EOF = 0x%08x, %v", value, err)
	}
}

func TestKTFStringBufferEditsCharactersInPlace(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	const instance = uint32(0x1234)
	runtime.stringBuffers[instance] = string(make([]rune, 4))
	for index, character := range []rune("res/") {
		for register, value := range map[uint32]uint32{
			cpu.RegisterR1: instance,
			cpu.RegisterR2: uint32(index),
			cpu.RegisterR3: uint32(character),
		} {
			if err := runtime.CPU.WriteRegister(register, value); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := runtime.handleStringBufferMethod(
			"setCharAt",
			"(IC)V",
		); err != nil {
			t.Fatal(err)
		}
	}
	if got := runtime.stringBuffers[instance]; got != "res/" {
		t.Fatalf("StringBuffer after setCharAt = %q", got)
	}
	name, err := runtime.NewJavaString("map")
	if err != nil {
		t.Fatal(err)
	}
	for register, value := range map[uint32]uint32{
		cpu.RegisterR1: instance,
		cpu.RegisterR2: 4,
		cpu.RegisterR3: name,
	} {
		if err := runtime.CPU.WriteRegister(register, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleStringBufferMethod(
		"insert",
		"(ILjava/lang/String;)Ljava/lang/StringBuffer;",
	); err != nil {
		t.Fatal(err)
	}
	if got := runtime.stringBuffers[instance]; got != "res/map" {
		t.Fatalf("StringBuffer after insert = %q", got)
	}
}

func TestKTFStringBufferRecordsUnmodelledMethods(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	if _, err := runtime.handleStringBufferMethod(
		"substring",
		"(II)Ljava/lang/String;",
	); err != nil {
		t.Fatal(err)
	}
	if got := runtime.LastUnimplementedJava; got !=
		"java/lang/StringBuffer.substring(II)Ljava/lang/String;" {
		t.Fatalf("last unimplemented method = %q", got)
	}
}

func TestKTFReadsStringsTheGuestBuiltForItself(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	instance, err := runtime.NewJavaString("resource/map.bin")
	if err != nil {
		t.Fatal(err)
	}
	// Drop the host memo so only the guest-visible fields remain, which is
	// what a title that assembles a name through its own code leaves behind.
	delete(runtime.JavaStrings, instance)
	if got := runtime.javaText(instance); got != "resource/map.bin" {
		t.Fatalf("guest string = %q", got)
	}
	if got := runtime.javaStringValue(instance); got != "resource/map.bin" {
		t.Fatalf("guest string value = %q", got)
	}
	if got := runtime.javaText(0); got != "" {
		t.Fatalf("null string = %q", got)
	}
}

func TestKTFInputStreamResetReturnsToTheMark(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	stream, err := runtime.NewHostJavaObject("java/io/DataInputStream")
	if err != nil {
		t.Fatal(err)
	}
	runtime.inputStreams[stream] = &ktfInputStream{
		data: []byte{1, 2, 3, 4, 5, 6},
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR1, stream); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 3 {
		if _, err := runtime.handleInputStreamMethod(
			ctx,
			"readByte",
			"()B",
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runtime.handleInputStreamMethod(ctx, "mark", "(I)V"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleInputStreamMethod(ctx, "readByte", "()B"); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.handleInputStreamMethod(ctx, "reset", "()V"); err != nil {
		t.Fatal(err)
	}
	value, err := runtime.handleInputStreamMethod(ctx, "readByte", "()B")
	if err != nil {
		t.Fatal(err)
	}
	if value != 4 {
		t.Fatalf("byte after reset = %d, want 4", value)
	}
	supported, err := runtime.handleInputStreamMethod(
		ctx,
		"markSupported",
		"()Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	if supported != 1 {
		t.Fatalf("markSupported = %d", supported)
	}
}

func TestKTFExceptionDispatchMovesTheFrameToTheHandler(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	entry, err := runtime.AllocateWords(4)
	if err != nil {
		t.Fatal(err)
	}
	// Protected region [115,432] with its handler at 435, the shape emitted
	// for `try { ... } catch (Throwable t) { throw new Error(...); }`.
	if err := runtime.writeWords(entry, []uint32{115, 432, 435, 0}); err != nil {
		t.Fatal(err)
	}
	table, err := runtime.AllocateWords(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WriteU32(table, entry); err != nil {
		t.Fatal(err)
	}
	method, err := runtime.AllocateWords(7)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(method, []uint32{0, 0, table, 0, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	functions, err := runtime.AllocateWords(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(functions, []uint32{0, 0x00123457}); err != nil {
		t.Fatal(err)
	}
	frame, err := runtime.AllocateWords(17)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(frame, []uint32{method, 0, 0, 429, 0, functions}); err != nil {
		t.Fatal(err)
	}
	exceptionContext, err := runtime.AllocateWords(ktfJavaEnvironmentWords)
	if err != nil {
		t.Fatal(err)
	}
	runtime.exceptionContext = exceptionContext
	if err := runtime.WriteU32(exceptionContext+8*4, frame); err != nil {
		t.Fatal(err)
	}
	target, caught, err := runtime.dispatchJavaException("java/lang/Error", 0x11223344)
	if err != nil {
		t.Fatal(err)
	}
	if !caught || target.handler != 435 {
		t.Fatalf("first dispatch = target %+v, caught %t", target, caught)
	}
	bytecodePC, err := runtime.ReadU32(frame + 3*4)
	if err != nil {
		t.Fatal(err)
	}
	if bytecodePC != 435 {
		t.Fatalf("frame bytecode PC = %d, want 435", bytecodePC)
	}
	// An exception raised by the handler body must escape the try it belongs
	// to instead of being caught by it again.
	if _, caught, err = runtime.dispatchJavaException(
		"java/lang/Error",
		0x55667788,
	); err != nil {
		t.Fatal(err)
	} else if caught {
		t.Fatal("the handler caught the exception it raised itself")
	}
}

func TestKTFClipRecyclingTakesAPlayingClipWhenNoneAreIdle(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	limit := int(runtime.Services.Config.Limits.Media.MaxClips)
	first := uint32(0)
	for index := range limit {
		instance := uint32(0x10000000 + index*0x100)
		if index == 0 {
			first = instance
		}
		runtime.clips[instance] = &ktfClip{volume: 5, playing: true}
		if _, err := runtime.ensureKTFClipService(instance); err != nil {
			t.Fatalf("clip %d: %v", index, err)
		}
	}
	extra := uint32(0x20000000)
	runtime.clips[extra] = &ktfClip{volume: 5}
	if _, err := runtime.ensureKTFClipService(extra); err != nil {
		t.Fatalf("clip past a fully playing pool: %v", err)
	}
	if runtime.clipServices[first] != 0 {
		t.Fatal("the oldest playing clip kept its host service")
	}
	if runtime.clips[first].playing {
		t.Fatal("a recycled clip is still marked as playing")
	}
}
