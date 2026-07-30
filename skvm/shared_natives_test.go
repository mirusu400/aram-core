package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func invokeTestNative(
	t *testing.T,
	vm *VM,
	class, name, descriptor string,
	receiver uint32,
	args ...Value,
) Value {
	t.Helper()
	native := vm.natives[nativeKey{
		class: class, name: name, descriptor: descriptor,
	}]
	if native == nil {
		t.Fatalf("native %s.%s%s is missing", class, name, descriptor)
	}
	value, _, err := native(context.Background(), vm, receiver, args)
	if err != nil {
		t.Fatalf("native %s.%s%s: %v", class, name, descriptor, err)
	}
	return value
}

func TestSKVMFileNativesUseSharedStorage(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	name := vm.NewString("save/game.dat")
	file := vm.NewObject("com/xce/io/XFile", nil)
	invokeTestNative(
		t,
		vm,
		"com/xce/io/XFile",
		"<init>",
		"(Ljava/lang/String;I)V",
		file,
		ReferenceValue(name),
		IntValue(1),
	)
	data := vm.NewByteArray([]byte("persistent"))
	invokeTestNative(
		t,
		vm,
		"com/xce/io/XFile",
		"write",
		"([BII)I",
		file,
		ReferenceValue(data),
		IntValue(0),
		IntValue(10),
	)
	stored, err := vm.services.Storage.ReadFile(
		shared.NamespacePrivate,
		"/save/game.dat",
	)
	if err != nil || !bytes.Equal(stored, []byte("persistent")) {
		t.Fatalf("shared file = %q, %v", stored, err)
	}
	state, err := vm.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.services.Storage.WriteFile(
		shared.NamespacePrivate,
		"/save/game.dat",
		[]byte("changed"),
	); err != nil {
		t.Fatal(err)
	}
	if err := vm.UnmarshalBinary(state); err != nil {
		t.Fatal(err)
	}
	stored, err = vm.services.Storage.ReadFile(
		shared.NamespacePrivate,
		"/save/game.dat",
	)
	if err != nil || !bytes.Equal(stored, []byte("persistent")) {
		t.Fatalf("restored shared file = %q, %v", stored, err)
	}
}

func TestSKVMDeviceAndAudioNativesUseSharedServices(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	invokeTestNative(
		t,
		vm,
		"com/skt/m/Device",
		"setBacklightEnabled",
		"(Z)V",
		0,
		IntValue(1),
	)
	enabled, _ := vm.services.Device.Backlight()
	if !enabled {
		t.Fatal("SKVM backlight native did not update the device service")
	}
	invokeTestNative(
		t,
		vm,
		"com/skt/m/Vibration",
		"start",
		"(II)V",
		0,
		IntValue(75),
		IntValue(500),
	)
	level, until := vm.services.Device.Vibration()
	if level != 75 || until != 500*time.Millisecond {
		t.Fatalf("vibration = %d until %s", level, until)
	}
	target := vm.NewString("https://example.invalid/")
	invokeTestNative(
		t,
		vm,
		"com/skt/m/Device",
		"invokeWapBrowser",
		"(Ljava/lang/String;)V",
		0,
		ReferenceValue(target),
	)
	requests := vm.services.Device.Requests()
	if len(requests) != 1 || requests[0].Kind != shared.RequestBrowser {
		t.Fatalf("device requests = %+v", requests)
	}

	name := vm.NewString("tone.wav")
	clipValue := invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioSystem",
		"getAudioClip",
		"(Ljava/lang/String;)Lcom/skt/m/AudioClip;",
		0,
		ReferenceValue(name),
	)
	clipReference, err := clipValue.Reference()
	if err != nil {
		t.Fatal(err)
	}
	invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioClip",
		"play",
		"()V",
		clipReference,
	)
	clip, err := vm.audioClip(clipReference)
	if err != nil {
		t.Fatal(err)
	}
	info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
	if err != nil || info.State != shared.ClipPlaying {
		t.Fatalf("shared audio clip = %+v, %v", info, err)
	}
}

func TestSKVMAudioClipCanReopenAfterClose(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	name := vm.NewString("tone.wav")
	clipValue := invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioSystem",
		"getAudioClip",
		"(Ljava/lang/String;)Lcom/skt/m/AudioClip;",
		0,
		ReferenceValue(name),
	)
	clipReference, err := clipValue.Reference()
	if err != nil {
		t.Fatal(err)
	}
	clip, err := vm.audioClip(clipReference)
	if err != nil {
		t.Fatal(err)
	}
	closedService := clip.clip
	invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioClip",
		"close",
		"()V",
		clipReference,
	)
	if clip.clip != 0 {
		t.Fatalf("closed clip service = %s, want zero", clip.clip)
	}
	invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioClip",
		"stop",
		"()V",
		clipReference,
	)

	payload := []byte("reopened")
	data := vm.NewByteArray(payload)
	invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioClip",
		"open",
		"([BII)V",
		clipReference,
		ReferenceValue(data),
		IntValue(0),
		IntValue(int32(len(payload))),
	)
	if clip.clip == 0 || clip.clip == closedService {
		t.Fatalf("reopened clip service = %s, closed service = %s", clip.clip, closedService)
	}
	source, err := vm.services.Media.Source(vm.serviceOwner, clip.clip)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, payload) {
		t.Fatalf("reopened clip source = %q, want %q", source, payload)
	}
}

func TestSKVMStringsUseSharedLegacyEncoding(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	text := vm.NewString("ARAM 가")
	encoded := invokeTestNative(
		t,
		vm,
		"java/lang/String",
		"getBytes",
		"()[B",
		text,
	)
	encodedReference, err := encoded.Reference()
	if err != nil {
		t.Fatal(err)
	}
	data, err := vm.ByteArray(encodedReference)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{'A', 'R', 'A', 'M', ' ', 0xb0, 0xa1}
	if !bytes.Equal(data, want) {
		t.Fatalf("default String.getBytes = %x, want EUC-KR %x", data, want)
	}

	utf8Name := vm.NewString("UTF-8")
	encoded = invokeTestNative(
		t,
		vm,
		"java/lang/String",
		"getBytes",
		"(Ljava/lang/String;)[B",
		text,
		ReferenceValue(utf8Name),
	)
	encodedReference, err = encoded.Reference()
	if err != nil {
		t.Fatal(err)
	}
	data, err = vm.ByteArray(encodedReference)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte("ARAM 가")) {
		t.Fatalf("UTF-8 String.getBytes = %x", data)
	}

	decoded := vm.NewObject("java/lang/String", nil)
	source := vm.NewByteArray(want)
	invokeTestNative(
		t,
		vm,
		"java/lang/String",
		"<init>",
		"([B)V",
		decoded,
		ReferenceValue(source),
	)
	value, err := vm.String(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if value != "ARAM 가" {
		t.Fatalf("default String(byte[]) = %q, want %q", value, "ARAM 가")
	}
}

func TestSKVMSystemGCReleasesUnreachableImageSurfaces(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	unreachableState, err := vm.newImageState(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	unreachable := vm.NewObject(
		"javax/microedition/lcdui/Image",
		unreachableState,
	)
	unreachableSurface := unreachableState.surface
	retainedState, err := vm.newImageState(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	retained := vm.NewObject("javax/microedition/lcdui/Image", retainedState)
	retainedSurface := retainedState.surface
	graphics := vm.NewObject(
		"javax/microedition/lcdui/Graphics",
		&graphicsState{surface: retainedState.surface, width: 2, height: 2},
	)
	vm.RegisterStaticField(
		"Game",
		"retainedGraphics",
		"Ljavax/microedition/lcdui/Graphics;",
		ReferenceValue(graphics),
	)

	invokeTestNative(t, vm, "java/lang/System", "gc", "()V", 0)
	if _, ok := vm.Object(unreachable); ok {
		t.Fatal("unreachable image survived System.gc")
	}
	if _, err := vm.services.Graphics.Descriptor(
		vm.serviceOwner,
		unreachableSurface,
	); err == nil {
		t.Fatal("unreachable image surface survived System.gc")
	}
	if _, ok := vm.Object(retained); !ok {
		t.Fatal("graphics alias did not retain its image")
	}
	if _, err := vm.services.Graphics.Descriptor(
		vm.serviceOwner,
		retainedSurface,
	); err != nil {
		t.Fatalf("retained image surface: %v", err)
	}

	vm.RegisterStaticField(
		"Game",
		"retainedGraphics",
		"Ljavax/microedition/lcdui/Graphics;",
		ReferenceValue(0),
	)
	invokeTestNative(t, vm, "java/lang/System", "gc", "()V", 0)
	if _, ok := vm.Object(retained); ok {
		t.Fatal("image survived after its graphics alias became unreachable")
	}
	if _, err := vm.services.Graphics.Descriptor(
		vm.serviceOwner,
		retainedSurface,
	); err == nil {
		t.Fatal("image surface survived after its graphics alias became unreachable")
	}
}

func TestSKVMXDisplayCopyLCDUsesSharedGraphics(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	source := vm.ScreenGraphics()
	sourceState, err := vm.graphics(source)
	if err != nil {
		t.Fatal(err)
	}
	want := shared.Color{R: 0x11, G: 0x22, B: 0x33, A: 0xff}
	if err := vm.services.Graphics.SetPixel(
		vm.serviceOwner,
		sourceState.surface,
		1,
		1,
		want,
	); err != nil {
		t.Fatal(err)
	}
	imageState, err := vm.newImageState(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	image := vm.NewObject("javax/microedition/lcdui/Image", imageState)
	invokeTestNative(
		t,
		vm,
		"com/xce/lcdui/XDisplay",
		"copyLCD",
		"(Ljavax/microedition/lcdui/Graphics;Ljavax/microedition/lcdui/Image;IIII)V",
		0,
		ReferenceValue(source),
		ReferenceValue(image),
		IntValue(0),
		IntValue(0),
		IntValue(2),
		IntValue(2),
	)
	got, err := vm.services.Graphics.Pixel(
		vm.serviceOwner,
		imageState.surface,
		1,
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("copied LCD pixel = %+v, want %+v", got, want)
	}
}

func TestSKVMConnectorUsesSharedDeterministicNetwork(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	name := vm.NewString("socket://127.0.0.1:7821")
	connectionValue := invokeTestNative(
		t,
		vm,
		"javax/microedition/io/Connector",
		"open",
		"(Ljava/lang/String;)Ljavax/microedition/io/Connection;",
		0,
		ReferenceValue(name),
	)
	connectionReference, err := connectionValue.Reference()
	if err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{
		"javax/microedition/io/Connection",
		"javax/microedition/io/InputConnection",
		"javax/microedition/io/OutputConnection",
		"javax/microedition/io/SocketConnection",
	} {
		if !vm.IsInstance(connectionReference, class) {
			t.Fatalf("socket connection is not an instance of %s", class)
		}
	}
	connection, err := vm.openSocketConnection(connectionReference)
	if err != nil {
		t.Fatal(err)
	}
	info, err := vm.services.Network.SocketInfo(
		vm.serviceOwner,
		connection.socket,
	)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != shared.ConnectionConnected ||
		info.Host != "127.0.0.1" || info.Port != 7821 {
		t.Fatalf("shared socket = %+v", info)
	}

	outputValue := invokeTestNative(
		t,
		vm,
		"javax/microedition/io/SocketConnection",
		"openDataOutputStream",
		"()Ljava/io/DataOutputStream;",
		connectionReference,
	)
	output, err := outputValue.Reference()
	if err != nil {
		t.Fatal(err)
	}
	invokeTestNative(
		t,
		vm,
		"java/io/DataOutputStream",
		"writeInt",
		"(I)V",
		output,
		IntValue(0x01020304),
	)
	written, err := vm.services.Network.SocketWritten(
		vm.serviceOwner,
		connection.socket,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, []byte{1, 2, 3, 4}) {
		t.Fatalf("shared socket write = %v", written)
	}

	if err := vm.services.InjectSocketResponse(
		vm.serviceOwner,
		connection.socket,
		[]byte{5, 6, 7, 8},
		vm.services.Clock.Monotonic(),
	); err != nil {
		t.Fatal(err)
	}
	inputValue := invokeTestNative(
		t,
		vm,
		"javax/microedition/io/SocketConnection",
		"openDataInputStream",
		"()Ljava/io/DataInputStream;",
		connectionReference,
	)
	input, err := inputValue.Reference()
	if err != nil {
		t.Fatal(err)
	}
	got := invokeTestNative(
		t,
		vm,
		"java/io/DataInputStream",
		"readInt",
		"()I",
		input,
	)
	integer, err := got.Int()
	if err != nil {
		t.Fatal(err)
	}
	if integer != 0x05060708 {
		t.Fatalf("shared socket read = %#x", integer)
	}

	saved, err := vm.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.UnmarshalBinary(saved); err != nil {
		t.Fatal(err)
	}
	connection, err = vm.openSocketConnection(connectionReference)
	if err != nil {
		t.Fatal(err)
	}
	info, err = vm.services.Network.SocketInfo(
		vm.serviceOwner,
		connection.socket,
	)
	if err != nil || info.State != shared.ConnectionConnected {
		t.Fatalf("restored shared socket = %+v, %v", info, err)
	}

	invokeTestNative(
		t,
		vm,
		"javax/microedition/io/SocketConnection",
		"close",
		"()V",
		connectionReference,
	)
	if !connection.closed || connection.socket != 0 {
		t.Fatalf("closed connection = %+v", connection)
	}
}

func TestSKVMThreadsRunCooperativelyOnVirtualTime(t *testing.T) {
	vm, err := New(map[string][]byte{"Worker": syntheticThreadClass(t)})
	if err != nil {
		t.Fatal(err)
	}
	target, err := vm.allocateObject("Worker")
	if err != nil {
		t.Fatal(err)
	}
	thread := vm.NewObject("java/lang/Thread", nil)
	invokeTestNative(
		t,
		vm,
		"java/lang/Thread",
		"<init>",
		"(Ljava/lang/Runnable;)V",
		thread,
		ReferenceValue(target),
	)
	invokeTestNative(
		t,
		vm,
		"java/lang/Thread",
		"start",
		"()V",
		thread,
	)
	counter := fieldStorageKey("Worker", "counter", "I")
	value, err := vm.classes["Worker"].static[counter].Int()
	if err != nil || value != 1 {
		t.Fatalf("counter after start = %d, %v; want 1", value, err)
	}
	state, err := vm.thread(thread)
	if err != nil {
		t.Fatal(err)
	}
	if !state.active || state.wakeAt != time.Millisecond {
		t.Fatalf("thread after start = %+v", state)
	}

	saved, err := vm.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.UnmarshalBinary(saved); err != nil {
		t.Fatal(err)
	}
	if err := vm.Advance(context.Background(), time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	value, err = vm.classes["Worker"].static[counter].Int()
	if err != nil || value != 2 {
		t.Fatalf("counter after advance = %d, %v; want 2", value, err)
	}
}

func syntheticThreadClass(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	u2 := func(value uint16) {
		t.Helper()
		if err := binary.Write(&output, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	u4 := func(value uint32) {
		t.Helper()
		if err := binary.Write(&output, binary.BigEndian, value); err != nil {
			t.Fatal(err)
		}
	}
	utf := func(value string) {
		output.WriteByte(constantUTF8)
		u2(uint16(len(value)))
		output.WriteString(value)
	}
	class := func(name uint16) {
		output.WriteByte(constantClass)
		u2(name)
	}
	nameAndType := func(name, descriptor uint16) {
		output.WriteByte(constantNameAndType)
		u2(name)
		u2(descriptor)
	}

	u4(0xcafebabe)
	u2(3)
	u2(45)
	u2(18)
	utf("Worker")           // 1
	class(1)                // 2
	utf("java/lang/Object") // 3
	class(3)                // 4
	utf("run")              // 5
	utf("()V")              // 6
	utf("Code")             // 7
	utf("counter")          // 8
	utf("I")                // 9
	nameAndType(8, 9)       // 10
	output.WriteByte(constantFieldref)
	u2(2)
	u2(10)                  // 11
	utf("java/lang/Thread") // 12
	class(12)               // 13
	utf("sleep")            // 14
	utf("(J)V")             // 15
	nameAndType(14, 15)     // 16
	output.WriteByte(constantMethodref)
	u2(13)
	u2(16) // 17

	u2(AccessPublic)
	u2(2)
	u2(4)
	u2(0) // interfaces
	u2(1) // fields
	u2(AccessPublic | AccessStatic)
	u2(8)
	u2(9)
	u2(0)
	u2(1) // methods
	u2(AccessPublic)
	u2(5)
	u2(6)
	u2(1)
	u2(7)
	code := []byte{
		0xb2, 0, 11, // getstatic Worker.counter
		0x04,        // iconst_1
		0x60,        // iadd
		0xb3, 0, 11, // putstatic Worker.counter
		0x0a,        // lconst_1
		0xb8, 0, 17, // invokestatic Thread.sleep
		0xb1, // return
	}
	u4(uint32(2 + 2 + 4 + len(code) + 2 + 2))
	u2(2)
	u2(1)
	u4(uint32(len(code)))
	output.Write(code)
	u2(0) // handlers
	u2(0) // code attributes
	u2(0) // class attributes
	return output.Bytes()
}
