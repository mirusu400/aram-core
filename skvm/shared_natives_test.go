package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestGameActionForSKTKeyCodes(t *testing.T) {
	tests := []struct {
		key  int32
		want int32
	}{
		{key: 141, want: 1},
		{key: 142, want: 2},
		{key: 145, want: 5},
		{key: 146, want: 6},
		{key: 148, want: 8},
	}
	for _, test := range tests {
		if got := gameActionForKey(test.key); got != test.want {
			t.Errorf("gameActionForKey(%d) = %d, want %d", test.key, got, test.want)
		}
	}
}

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
	check(t, err)
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
	check(t, err)
	check(t, vm.services.Storage.WriteFile(
		shared.NamespacePrivate,
		"/save/game.dat",
		[]byte("changed"),
	))
	check(t, vm.UnmarshalBinary(state))
	stored, err = vm.services.Storage.ReadFile(
		shared.NamespacePrivate,
		"/save/game.dat",
	)
	if err != nil || !bytes.Equal(stored, []byte("persistent")) {
		t.Fatalf("restored shared file = %q, %v", stored, err)
	}
}

func TestSKVMProgressBarRetainsValue(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	bar := vm.NewObject("com/skt/m/ProgressBar", nil)
	label := vm.NewString("loading")
	invokeTestNative(
		t, vm,
		"com/skt/m/ProgressBar", "<init>", "(Ljava/lang/String;)V",
		bar, ReferenceValue(label),
	)
	invokeTestNative(
		t, vm,
		"com/skt/m/ProgressBar", "setMaxValue", "(I)V",
		bar, IntValue(100),
	)
	invokeTestNative(
		t, vm,
		"com/skt/m/ProgressBar", "setValue", "(I)V",
		bar, IntValue(42),
	)
	got := invokeTestNative(t, vm, "com/skt/m/ProgressBar", "getValue", "()I", bar)
	if value, valueErr := got.Int(); valueErr != nil || value != 42 {
		t.Fatalf("getValue = %d, %v; want 42", value, valueErr)
	}
	// The load level has to survive a save-state round-trip so a resumed title
	// does not see its progress bar snap back to zero.
	state, err := vm.MarshalBinary()
	check(t, err)
	invokeTestNative(t, vm, "com/skt/m/ProgressBar", "setValue", "(I)V", bar, IntValue(7))
	check(t, vm.UnmarshalBinary(state))
	got = invokeTestNative(t, vm, "com/skt/m/ProgressBar", "getValue", "()I", bar)
	if value, valueErr := got.Int(); valueErr != nil || value != 42 {
		t.Fatalf("restored getValue = %d, %v; want 42", value, valueErr)
	}
}

func TestSKVMDeviceAndAudioNativesUseSharedServices(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	check(t, vm.SetResourcesChecked(map[string][]byte{"tone.wav": skvmTestWave()}))
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
	check(t, err)
	invokeTestNative(
		t,
		vm,
		"com/skt/m/AudioClip",
		"play",
		"()V",
		clipReference,
	)
	clip, err := vm.audioClip(clipReference)
	check(t, err)
	info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
	if err != nil || info.State != shared.ClipPlaying {
		t.Fatalf("shared audio clip = %+v, %v", info, err)
	}
}

func skvmTestWave() []byte {
	data := make([]byte, 48)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1)
	binary.LittleEndian.PutUint32(data[24:28], 8_000)
	binary.LittleEndian.PutUint32(data[28:32], 16_000)
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], 4)
	binary.LittleEndian.PutUint16(data[44:46], 1)
	binary.LittleEndian.PutUint16(data[46:48], 2)
	return data
}

func TestSKVMAudioClipCanReopenAfterClose(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
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
	check(t, err)
	clip, err := vm.audioClip(clipReference)
	check(t, err)
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
	check(t, err)
	if !bytes.Equal(source, payload) {
		t.Fatalf("reopened clip source = %q, want %q", source, payload)
	}
}

func TestSKVMStringsUseSharedLegacyEncoding(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
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
	check(t, err)
	data, err := vm.ByteArray(encodedReference)
	check(t, err)
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
	check(t, err)
	data, err = vm.ByteArray(encodedReference)
	check(t, err)
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
	check(t, err)
	if value != "ARAM 가" {
		t.Fatalf("default String(byte[]) = %q, want %q", value, "ARAM 가")
	}
}

func TestSKVMSystemGCReleasesUnreachableImageSurfaces(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	unreachableState, err := vm.newImageState(2, 2)
	check(t, err)
	unreachable := vm.NewObject(
		"javax/microedition/lcdui/Image",
		unreachableState,
	)
	unreachableSurface := unreachableState.surface
	retainedState, err := vm.newImageState(2, 2)
	check(t, err)
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

func TestSKVMSystemGCRetainsActiveApplicationRoot(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	application := vm.NewObject("Game", nil)
	vm.hostStatic[fieldStorageKey(
		applicationRootClass,
		applicationRootField,
		applicationRootDescriptor,
	)] = ReferenceValue(application)
	invokeTestNative(t, vm, "java/lang/System", "gc", "()V", 0)
	if _, ok := vm.Object(application); !ok {
		t.Fatal("active application root was collected")
	}
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	if _, ok := vm.Object(application); !ok {
		t.Fatal("active application root was not restored")
	}
}

func TestSKVMXDisplayCopyLCDUsesSharedGraphics(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	source := vm.ScreenGraphics()
	sourceState, err := vm.graphics(source)
	check(t, err)
	want := shared.Color{R: 0x11, G: 0x22, B: 0x33, A: 0xff}
	check(t, vm.services.Graphics.SetPixel(
		vm.serviceOwner,
		sourceState.surface,
		1,
		1,
		want,
	))
	imageState, err := vm.newImageState(2, 2)
	check(t, err)
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
	check(t, err)
	if got != want {
		t.Fatalf("copied LCD pixel = %+v, want %+v", got, want)
	}
}

func TestSKVMConnectorUsesSharedDeterministicNetwork(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
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
	check(t, err)
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
	check(t, err)
	info, err := vm.services.Network.SocketInfo(
		vm.serviceOwner,
		connection.socket,
	)
	check(t, err)
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
	check(t, err)
	invokeTestNative(
		t,
		vm,
		"java/io/DataOutputStream",
		"writeInt",
		"(I)V",
		output,
		IntValue(0x01020304),
	)
	outerOutput := vm.NewObject("java/io/DataOutputStream", nil)
	invokeTestNative(
		t,
		vm,
		"java/io/DataOutputStream",
		"<init>",
		"(Ljava/io/OutputStream;)V",
		outerOutput,
		ReferenceValue(output),
	)
	invokeTestNative(
		t,
		vm,
		"java/io/DataOutputStream",
		"writeInt",
		"(I)V",
		outerOutput,
		IntValue(0x11121314),
	)
	written, err := vm.services.Network.SocketWritten(
		vm.serviceOwner,
		connection.socket,
	)
	check(t, err)
	if !bytes.Equal(written, []byte{1, 2, 3, 4, 0x11, 0x12, 0x13, 0x14}) {
		t.Fatalf("shared socket write = %v", written)
	}

	check(t, vm.services.InjectSocketResponse(
		vm.serviceOwner,
		connection.socket,
		[]byte{5, 6, 7, 8},
		vm.services.Clock.Monotonic(),
	))
	inputValue := invokeTestNative(
		t,
		vm,
		"javax/microedition/io/SocketConnection",
		"openDataInputStream",
		"()Ljava/io/DataInputStream;",
		connectionReference,
	)
	input, err := inputValue.Reference()
	check(t, err)
	outerInput := vm.NewObject("java/io/DataInputStream", nil)
	invokeTestNative(
		t,
		vm,
		"java/io/DataInputStream",
		"<init>",
		"(Ljava/io/InputStream;)V",
		outerInput,
		ReferenceValue(input),
	)
	got := invokeTestNative(
		t,
		vm,
		"java/io/DataInputStream",
		"readInt",
		"()I",
		outerInput,
	)
	integer, err := got.Int()
	check(t, err)
	if integer != 0x05060708 {
		t.Fatalf("shared socket read = %#x", integer)
	}

	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	connection, err = vm.openSocketConnection(connectionReference)
	check(t, err)
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

func TestSKVMHTTPConnectionUsesSharedDeterministicNetwork(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	name := vm.NewString("https://example.test/game")
	connectionValue := invokeTestNative(
		t,
		vm,
		"javax/microedition/io/Connector",
		"open",
		"(Ljava/lang/String;IZ)Ljavax/microedition/io/Connection;",
		0,
		ReferenceValue(name),
		IntValue(3),
		IntValue(1),
	)
	connectionReference, err := connectionValue.Reference()
	check(t, err)
	for _, class := range []string{
		"javax/microedition/io/Connection",
		"javax/microedition/io/InputConnection",
		"javax/microedition/io/OutputConnection",
		"javax/microedition/io/StreamConnection",
		"javax/microedition/io/ContentConnection",
		"javax/microedition/io/HttpConnection",
	} {
		if !vm.IsInstance(connectionReference, class) {
			t.Fatalf("HTTP connection is not an instance of %s", class)
		}
	}
	method := vm.NewString("POST")
	invokeTestNative(
		t,
		vm,
		"javax/microedition/io/HttpConnection",
		"setRequestMethod",
		"(Ljava/lang/String;)V",
		connectionReference,
		ReferenceValue(method),
	)
	headerName := vm.NewString("X-Test")
	headerValue := vm.NewString("aram")
	invokeTestNative(
		t,
		vm,
		"javax/microedition/io/HttpConnection",
		"setRequestProperty",
		"(Ljava/lang/String;Ljava/lang/String;)V",
		connectionReference,
		ReferenceValue(headerName),
		ReferenceValue(headerValue),
	)
	outputValue := invokeTestNative(
		t,
		vm,
		"javax/microedition/io/HttpConnection",
		"openOutputStream",
		"()Ljava/io/OutputStream;",
		connectionReference,
	)
	output, err := outputValue.Reference()
	check(t, err)
	body := vm.NewByteArray([]byte("payload"))
	invokeTestNative(
		t,
		vm,
		"java/io/OutputStream",
		"write",
		"([B)V",
		output,
		ReferenceValue(body),
	)
	connection, err := vm.openHTTPConnection(connectionReference)
	check(t, err)
	request, err := vm.httpRequestSnapshot(connection.request)
	check(t, err)
	if request.State != shared.ConnectionNew ||
		request.Method != "POST" ||
		!bytes.Equal(request.RequestBody, []byte("payload")) ||
		len(request.RequestHeaders) != 1 ||
		request.RequestHeaders[0] != (shared.HTTPProperty{Name: "x-test", Value: "aram"}) {
		t.Fatalf("shared HTTP request = %+v", request)
	}

	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	length := invokeTestNative(
		t,
		vm,
		"javax/microedition/io/HttpConnection",
		"getLength",
		"()J",
		connectionReference,
	)
	if value, valueErr := length.Long(); valueErr != nil || value != 0 {
		t.Fatalf("HTTP response length = %d, %v", value, valueErr)
	}
	contentTypeValue := invokeTestNative(
		t,
		vm,
		"javax/microedition/io/HttpConnection",
		"getType",
		"()Ljava/lang/String;",
		connectionReference,
	)
	contentTypeReference, err := contentTypeValue.Reference()
	check(t, err)
	contentType, err := vm.String(contentTypeReference)
	if err != nil || contentType != "application/octet-stream" {
		t.Fatalf("HTTP response content type = %q, %v", contentType, err)
	}
	inputValue := invokeTestNative(
		t,
		vm,
		"javax/microedition/io/HttpConnection",
		"openInputStream",
		"()Ljava/io/InputStream;",
		connectionReference,
	)
	input, err := inputValue.Reference()
	check(t, err)
	read := invokeTestNative(
		t,
		vm,
		"java/io/InputStream",
		"read",
		"()I",
		input,
	)
	if value, valueErr := read.Int(); valueErr != nil || value != -1 {
		t.Fatalf("empty HTTP response read = %d, %v", value, valueErr)
	}
	invokeTestNative(
		t,
		vm,
		"javax/microedition/io/HttpConnection",
		"close",
		"()V",
		connectionReference,
	)
	connection, ok := vm.heap[connectionReference].Native.(*httpConnectionState)
	if !ok || !connection.closed || connection.request != 0 {
		t.Fatalf("closed HTTP connection = %+v", connection)
	}
}

func TestSKVMCompatibilityGraphicsUseSharedServices(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	graphicsReference := vm.ScreenGraphics()
	graphics, err := vm.graphics(graphicsReference)
	check(t, err)
	graphics2D := vm.NewObject("com/skt/m/Graphics2D", graphics)
	invokeTestNative(
		t,
		vm,
		"com/skt/m/Graphics2D",
		"setPixel",
		"(III)V",
		graphics2D,
		IntValue(2),
		IntValue(3),
		IntValue(0x123456),
	)
	pixel, err := vm.services.Graphics.Pixel(
		vm.serviceOwner,
		graphics.surface,
		2,
		3,
	)
	check(t, err)
	if pixel != (shared.Color{R: 0x12, G: 0x34, B: 0x56, A: 0xff}) {
		t.Fatalf("Graphics2D pixel = %+v", pixel)
	}

	kwisGraphics := vm.NewObject("org/kwis/msp/lcdui/Graphics", graphics)
	firstColor := int32(-5588020)   // 0xffaabbcc
	secondColor := int32(-16711165) // 0xff010203
	values := vm.newArray("[I", []Value{
		IntValue(firstColor),
		IntValue(secondColor),
	})
	invokeTestNative(
		t,
		vm,
		"org/kwis/msp/lcdui/Graphics",
		"setRGBPixels",
		"(IIII[III)V",
		kwisGraphics,
		IntValue(4),
		IntValue(5),
		IntValue(2),
		IntValue(1),
		ReferenceValue(values),
		IntValue(0),
		IntValue(2),
	)
	roundTrip := vm.newArray("[I", []Value{IntValue(0), IntValue(0)})
	invokeTestNative(
		t,
		vm,
		"org/kwis/msp/lcdui/Graphics",
		"getRGBPixels",
		"(IIII[III)V",
		kwisGraphics,
		IntValue(4),
		IntValue(5),
		IntValue(2),
		IntValue(1),
		ReferenceValue(roundTrip),
		IntValue(0),
		IntValue(2),
	)
	object, ok := vm.Object(roundTrip)
	if !ok || object.Array == nil ||
		object.Array.Elements[0] != IntValue(firstColor) ||
		object.Array.Elements[1] != IntValue(secondColor) {
		t.Fatalf("KWIS RGB round trip = %+v", object)
	}
}

func TestSKVMGraphicsClipAndTranslationUseSharedDrawState(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	graphicsReference := vm.ScreenGraphics()
	graphics, err := vm.graphics(graphicsReference)
	check(t, err)
	check(t, vm.services.Graphics.Clear(
		vm.serviceOwner,
		graphics.surface,
		shared.RGB(0, 0, 0),
	))

	invokeTestNative(
		t,
		vm,
		"javax/microedition/lcdui/Graphics",
		"setClip",
		"(IIII)V",
		graphicsReference,
		IntValue(2),
		IntValue(3),
		IntValue(2),
		IntValue(2),
	)
	invokeTestNative(
		t,
		vm,
		"javax/microedition/lcdui/Graphics",
		"setColor",
		"(I)V",
		graphicsReference,
		IntValue(0xff0000),
	)
	invokeTestNative(
		t,
		vm,
		"javax/microedition/lcdui/Graphics",
		"fillRect",
		"(IIII)V",
		graphicsReference,
		IntValue(0),
		IntValue(0),
		IntValue(10),
		IntValue(10),
	)
	inside, err := vm.services.Graphics.Pixel(
		vm.serviceOwner,
		graphics.surface,
		2,
		3,
	)
	check(t, err)
	outside, err := vm.services.Graphics.Pixel(
		vm.serviceOwner,
		graphics.surface,
		1,
		3,
	)
	check(t, err)
	if inside != shared.RGB(0xff, 0, 0) || outside != shared.RGB(0, 0, 0) {
		t.Fatalf("clipped fill pixels = inside %+v, outside %+v", inside, outside)
	}

	invokeTestNative(
		t,
		vm,
		"javax/microedition/lcdui/Graphics",
		"translate",
		"(II)V",
		graphicsReference,
		IntValue(1),
		IntValue(-1),
	)
	getter := func(name string) int32 {
		t.Helper()
		value := invokeTestNative(
			t,
			vm,
			"javax/microedition/lcdui/Graphics",
			name,
			"()I",
			graphicsReference,
		)
		result, valueErr := value.Int()
		if valueErr != nil {
			t.Fatal(valueErr)
		}
		return result
	}
	if getter("getClipX") != 1 ||
		getter("getClipY") != 4 ||
		getter("getClipWidth") != 2 ||
		getter("getClipHeight") != 2 ||
		getter("getTranslateX") != 1 ||
		getter("getTranslateY") != -1 {
		t.Fatalf(
			"translated graphics state = clip (%d,%d %dx%d), translate (%d,%d)",
			getter("getClipX"),
			getter("getClipY"),
			getter("getClipWidth"),
			getter("getClipHeight"),
			getter("getTranslateX"),
			getter("getTranslateY"),
		)
	}

	invokeTestNative(
		t,
		vm,
		"javax/microedition/lcdui/Graphics",
		"clipRect",
		"(IIII)V",
		graphicsReference,
		IntValue(1),
		IntValue(4),
		IntValue(1),
		IntValue(1),
	)
	if getter("getClipX") != 1 ||
		getter("getClipY") != 4 ||
		getter("getClipWidth") != 1 ||
		getter("getClipHeight") != 1 {
		t.Fatalf(
			"intersected clip = (%d,%d %dx%d)",
			getter("getClipX"),
			getter("getClipY"),
			getter("getClipWidth"),
			getter("getClipHeight"),
		)
	}
}

func TestSKVMGraphics2DDrawImageAppliesRasterMode(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	graphicsReference := vm.ScreenGraphics()
	graphics, err := vm.graphics(graphicsReference)
	check(t, err)
	graphics2D := vm.NewObject("com/skt/m/Graphics2D", graphics)
	image, err := vm.newImageState(1, 1)
	check(t, err)
	imageReference := vm.NewObject("javax/microedition/lcdui/Image", image)
	source := shared.RGB(0xf0, 0x0f, 0xaa)
	check(t, vm.services.Graphics.SetPixel(
		vm.serviceOwner,
		image.surface,
		0,
		0,
		source,
	))
	destination := shared.RGB(0x55, 0x33, 0x0f)
	check(t, vm.services.Graphics.SetPixel(
		vm.serviceOwner,
		graphics.surface,
		0,
		0,
		destination,
	))

	invokeTestNative(
		t,
		vm,
		"com/skt/m/Graphics2D",
		"drawImage",
		"(IILjavax/microedition/lcdui/Image;IIIII)V",
		graphics2D,
		IntValue(0),
		IntValue(0),
		ReferenceValue(imageReference),
		IntValue(0),
		IntValue(0),
		IntValue(1),
		IntValue(1),
		IntValue(3),
	)
	pixel, err := vm.services.Graphics.Pixel(
		vm.serviceOwner,
		graphics.surface,
		0,
		0,
	)
	check(t, err)
	want := shared.Color{
		R: destination.R ^ source.R,
		G: destination.G ^ source.G,
		B: destination.B ^ source.B,
		A: 0xff,
	}
	if pixel != want {
		t.Fatalf("Graphics2D XOR pixel = %+v, want %+v", pixel, want)
	}
	state, err := vm.services.Graphics.DrawState(
		vm.serviceOwner,
		graphics.surface,
	)
	check(t, err)
	if state.Raster != shared.RasterCopy {
		t.Fatalf("Graphics2D raster state leaked as %v", state.Raster)
	}
}

func TestSKVMThreadsRunCooperativelyOnVirtualTime(t *testing.T) {
	vm, err := New(map[string][]byte{"Worker": syntheticThreadClass(t)})
	check(t, err)
	target, err := vm.allocateObject("Worker")
	check(t, err)
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
	check(t, err)
	if !state.active || state.wakeAt != time.Millisecond ||
		len(state.continuation) != 2 {
		t.Fatalf("thread after start = %+v", state)
	}
	invokeTestNative(
		t,
		vm,
		"java/lang/Thread",
		"start",
		"()V",
		thread,
	)
	value, err = vm.classes["Worker"].static[counter].Int()
	if err != nil || value != 1 {
		t.Fatalf("counter after duplicate active start = %d, %v; want 1", value, err)
	}

	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	roundTripped, err := vm.MarshalBinary()
	check(t, err)
	if !bytes.Equal(roundTripped, saved) {
		t.Fatal("thread continuation state changed across round trip")
	}
	check(t, vm.Advance(context.Background(), time.Millisecond, nil))
	value, err = vm.classes["Worker"].static[counter].Int()
	if err != nil || value != 11 {
		t.Fatalf("counter after advance = %d, %v; want 11", value, err)
	}
	state, err = vm.thread(thread)
	check(t, err)
	if state.active || len(state.continuation) != 0 {
		t.Fatalf("thread after return = %+v", state)
	}
}

func TestSKVMThreadPreemptsNonYieldingWorker(t *testing.T) {
	// Start from the established Runnable fixture, but replace its sleep call
	// with a branch back to its first instruction. This stays a valid compact
	// class file while modelling an SKT-style polling worker.
	spin := append([]byte(nil), syntheticThreadClass(t)...)
	sleep := bytes.Index(spin, []byte{0xb8, 0, 21})
	if sleep < 0 {
		t.Fatal("synthetic worker sleep call is missing")
	}
	spin[sleep] = 0xa7 // goto
	spin[sleep+1] = 0xff
	spin[sleep+2] = 0xf8 // from PC 8 back to PC 0

	vm, err := New(map[string][]byte{"Worker": spin})
	check(t, err)
	target, err := vm.allocateObject("Worker")
	check(t, err)
	thread := vm.NewObject("java/lang/Thread", nil)
	invokeTestNative(
		t, vm,
		"java/lang/Thread", "<init>", "(Ljava/lang/Runnable;)V",
		thread, ReferenceValue(target),
	)
	invokeTestNative(t, vm, "java/lang/Thread", "start", "()V", thread)

	counter := fieldStorageKey("Worker", "counter", "I")
	first, err := vm.classes["Worker"].static[counter].Int()
	if err != nil || first <= 0 {
		t.Fatalf("counter after first worker slice = %d, %v", first, err)
	}
	state, err := vm.thread(thread)
	check(t, err)
	if !state.active || len(state.continuation) == 0 || state.wakeAt != time.Nanosecond {
		t.Fatalf("spinning worker was not preempted: %+v", state)
	}
	if vm.Instructions != threadInstructionQuantum {
		t.Fatalf("first worker slice used %d instructions, want %d", vm.Instructions, threadInstructionQuantum)
	}

	check(t, vm.Advance(context.Background(), time.Nanosecond, nil))
	second, err := vm.classes["Worker"].static[counter].Int()
	if err != nil || second <= first {
		t.Fatalf("counter after resumed worker slice = %d, %v; first=%d", second, err, first)
	}
	if vm.Instructions != 2*threadInstructionQuantum {
		t.Fatalf("second worker slice used %d instructions, want %d", vm.Instructions, 2*threadInstructionQuantum)
	}
}

func TestDisplayCallSeriallyDefersRunnable(t *testing.T) {
	vm, err := New(map[string][]byte{"Worker": syntheticThreadClass(t)})
	check(t, err)
	target, err := vm.allocateObject("Worker")
	check(t, err)
	display := vm.NewObject("javax/microedition/lcdui/Display", nil)
	invokeTestNative(
		t,
		vm,
		"javax/microedition/lcdui/Display",
		"callSerially",
		"(Ljava/lang/Runnable;)V",
		display,
		ReferenceValue(target),
	)
	counter := fieldStorageKey("Worker", "counter", "I")
	value, err := vm.classes["Worker"].static[counter].Int()
	if err != nil || value != 0 {
		t.Fatalf("counter before advance = %d, %v; want 0", value, err)
	}
	event, ok := vm.services.Events.Peek()
	if !ok || event.Kind != shared.EventApplication ||
		event.Name != callSeriallyEventName || event.Value != int64(target) ||
		event.At != time.Nanosecond {
		t.Fatalf("callSerially event = %+v, present=%v", event, ok)
	}
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	check(t, vm.Advance(context.Background(), time.Nanosecond, nil))
	value, err = vm.classes["Worker"].static[counter].Int()
	if err != nil || value != 11 {
		t.Fatalf("counter after advance = %d, %v; want 11", value, err)
	}
}

func syntheticThreadClass(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	u2 := func(value uint16) {
		t.Helper()
		check(t, binary.Write(&output, binary.BigEndian, value))
	}
	u4 := func(value uint32) {
		t.Helper()
		check(t, binary.Write(&output, binary.BigEndian, value))
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
	u2(22)
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
	u2(16)              // 17
	utf("pause")        // 18
	utf("()I")          // 19
	nameAndType(18, 19) // 20
	output.WriteByte(constantMethodref)
	u2(2)
	u2(20) // 21

	u2(AccessPublic)
	u2(2)
	u2(4)
	u2(0) // interfaces
	u2(1) // fields
	u2(AccessPublic | AccessStatic)
	u2(8)
	u2(9)
	u2(0)
	u2(2) // methods
	writeMethod := func(
		access, name, descriptor, maxStack, maxLocals uint16,
		code []byte,
	) {
		t.Helper()
		u2(access)
		u2(name)
		u2(descriptor)
		u2(1)
		u2(7)
		u4(uint32(2 + 2 + 4 + len(code) + 2 + 2))
		u2(maxStack)
		u2(maxLocals)
		u4(uint32(len(code)))
		output.Write(code)
		u2(0) // handlers
		u2(0) // code attributes
	}
	runCode := []byte{
		0xb2, 0, 11, // getstatic Worker.counter
		0x04,        // iconst_1
		0x60,        // iadd
		0xb3, 0, 11, // putstatic Worker.counter
		0xb8, 0, 21, // invokestatic Worker.pause
		0xb2, 0, 11, // getstatic Worker.counter
		0x60,        // iadd
		0xb3, 0, 11, // putstatic Worker.counter
		0xb1, // return
	}
	writeMethod(AccessPublic, 5, 6, 2, 1, runCode)
	pauseCode := []byte{
		0x0a,        // lconst_1
		0xb8, 0, 17, // invokestatic Thread.sleep
		0x10, 10, // bipush 10
		0xac, // ireturn
	}
	writeMethod(AccessPublic|AccessStatic, 18, 19, 2, 0, pauseCode)
	u2(0) // class attributes
	return output.Bytes()
}

// TestSKVMBrowserNativeOutlivesAFullOutbox covers what random key fuzzing hit
// on 노리타이쿤: nothing in the product acknowledges external requests, so a
// menu the player can open again and again eventually met a full outbox, and
// the native handed that refusal back as a fatal VM error. invokeWapBrowser
// returns void - the handset cannot tell the title its browser did not open -
// so neither a full outbox nor a target the title built badly may stop it.
func TestSKVMBrowserNativeOutlivesAFullOutbox(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	target := vm.NewString("https://example.invalid/")
	for visit := 0; visit < 600; visit++ {
		invokeTestNative(
			t, vm,
			"com/skt/m/Device", "invokeWapBrowser", "(Ljava/lang/String;)V",
			0, ReferenceValue(target),
		)
	}
	if requests := vm.services.Device.Requests(); len(requests) == 0 {
		t.Fatal("the outbox kept nothing at all")
	}
	invokeTestNative(
		t, vm,
		"com/skt/m/Device", "invokeWapBrowser", "(Ljava/lang/String;)V",
		0, ReferenceValue(vm.NewString("")),
	)
}
