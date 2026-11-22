package skvm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

// kwisTestMethod is one method of a class built by kwisTestClass. Its code may
// name the class's two static int fields through the constant-pool indexes
// kwisTestFirst and kwisTestSecond.
type kwisTestMethod struct {
	name, descriptor string
	maxLocals        uint16
	code             []byte
}

const (
	kwisTestFirst  = 11
	kwisTestSecond = 12
)

func kwisTestClass(t *testing.T, name, super string, methods ...kwisTestMethod) []byte {
	t.Helper()
	var output bytes.Buffer
	u2 := func(value uint16) { check(t, binary.Write(&output, binary.BigEndian, value)) }
	u4 := func(value uint32) { check(t, binary.Write(&output, binary.BigEndian, value)) }
	utf := func(value string) {
		output.WriteByte(constantUTF8)
		u2(uint16(len(value)))
		output.WriteString(value)
	}
	pair := func(tag byte, first, second uint16) {
		output.WriteByte(tag)
		u2(first)
		u2(second)
	}
	u4(0xcafebabe)
	u2(3)
	u2(45)
	u2(uint16(13 + 2*len(methods)))
	utf(name) // 1
	output.WriteByte(constantClass)
	u2(1)      // 2
	utf(super) // 3
	output.WriteByte(constantClass)
	u2(3)                            // 4
	utf("Code")                      // 5
	utf("first")                     // 6
	utf("second")                    // 7
	utf("I")                         // 8
	pair(constantNameAndType, 6, 8)  // 9
	pair(constantNameAndType, 7, 8)  // 10
	pair(constantFieldref, 2, 9)     // 11
	pair(constantFieldref, 2, 10)    // 12
	for _, method := range methods { // 13...
		utf(method.name)
		utf(method.descriptor)
	}
	u2(AccessPublic)
	u2(2)
	u2(4)
	u2(0) // interfaces
	u2(2) // fields
	for _, field := range []uint16{6, 7} {
		u2(AccessPublic | AccessStatic)
		u2(field)
		u2(8)
		u2(0)
	}
	u2(uint16(len(methods)))
	for index, method := range methods {
		u2(AccessPublic)
		u2(uint16(13 + 2*index))
		u2(uint16(14 + 2*index))
		u2(1)
		u2(5)
		u4(uint32(2 + 2 + 4 + len(method.code) + 2 + 2))
		u2(4)
		u2(method.maxLocals)
		u4(uint32(len(method.code)))
		output.Write(method.code)
		u2(0) // handlers
		u2(0) // code attributes
	}
	u2(0) // class attributes
	return output.Bytes()
}

func kwisTestStatic(t *testing.T, vm *VM, class, field string) int32 {
	t.Helper()
	value, ok := vm.classes[class].static[fieldStorageKey(class, field, "I")]
	if !ok {
		return 0
	}
	result, err := value.Int()
	check(t, err)
	return result
}

func kwisTestCardClass(t *testing.T) []byte {
	return kwisTestClass(t, "TestCard", kwisCardClass,
		kwisTestMethod{
			// second++ counts paints.
			name: "paint", descriptor: kwisPaintDescriptor, maxLocals: 2,
			code: []byte{
				0xb2, 0, kwisTestSecond, // getstatic second
				0x04,                    // iconst_1
				0x60,                    // iadd
				0xb3, 0, kwisTestSecond, // putstatic second
				0xb1, // return
			},
		},
		kwisTestMethod{
			// first = type*100 + key.
			name: "keyNotify", descriptor: "(II)Z", maxLocals: 3,
			code: []byte{
				0x1b,      // iload_1
				0x10, 100, // bipush 100
				0x68,                   // imul
				0x1c,                   // iload_2
				0x60,                   // iadd
				0xb3, 0, kwisTestFirst, // putstatic first
				0x03, // iconst_0
				0xac, // ireturn
			},
		},
	)
}

func TestKWISCardIsPaintedAndReceivesItsKeys(t *testing.T) {
	vm, err := New(map[string][]byte{"TestCard": kwisTestCardClass(t)})
	check(t, err)
	ctx := context.Background()
	card, err := vm.allocateObject("TestCard")
	check(t, err)
	display := invokeTestNative(
		t, vm, kwisDisplayClass, "getDefaultDisplay", "()Lorg/kwis/msp/lcdui/Display;", 0,
	)
	displayReference, err := display.Reference()
	check(t, err)

	invokeTestNative(
		t, vm, kwisDisplayClass, "pushCard", "(Lorg/kwis/msp/lcdui/Card;)V",
		displayReference, ReferenceValue(card),
	)
	if vm.CurrentDisplay() != card || !vm.RepaintPending() {
		t.Fatalf("pushed card: current=%d pending=%v, want %d and a pending paint",
			vm.CurrentDisplay(), vm.RepaintPending(), card)
	}
	check(t, vm.PaintCurrent(ctx))
	if got := kwisTestStatic(t, vm, "TestCard", "second"); got != 1 {
		t.Fatalf("Card.paint ran %d times, want 1", got)
	}
	if vm.RepaintPending() {
		t.Fatal("paint left its repaint request pending")
	}
	invokeTestNative(t, vm, kwisCardClass, "repaint", "()V", card)
	if !vm.RepaintPending() {
		t.Fatal("Card.repaint() did not request a paint")
	}

	// The host delivers SKT keypad codes; a Card reads WIPI action codes.
	check(t, vm.KeyEvent(ctx, 148, true))
	if got := kwisTestStatic(t, vm, "TestCard", "first"); got != 1*100+8 {
		t.Fatalf("FIRE press reached keyNotify as %d, want %d", got, 1*100+8)
	}
	check(t, vm.KeyEvent(ctx, 131, false))
	if got := kwisTestStatic(t, vm, "TestCard", "first"); got != 2*100+90 {
		t.Fatalf("SOFT1 release reached keyNotify as %d, want %d", got, 2*100+90)
	}
	check(t, vm.KeyEvent(ctx, '7', true))
	if got := kwisTestStatic(t, vm, "TestCard", "first"); got != 1*100+'7' {
		t.Fatalf("digit press reached keyNotify as %d, want %d", got, 1*100+'7')
	}

	// The card stack survives a save state.
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	removed := invokeTestNative(
		t, vm, kwisDisplayClass, "removeCard", "(Lorg/kwis/msp/lcdui/Card;)Z",
		displayReference, ReferenceValue(card),
	)
	if value, _ := removed.Int(); value != 1 || vm.CurrentDisplay() != 0 {
		t.Fatalf("removeCard = %d with current display %d, want 1 and none",
			value, vm.CurrentDisplay())
	}
}

func TestKWISGameActionCoversHandsetKeys(t *testing.T) {
	for key, want := range map[int32]int32{
		-1: 1, -2: 6, -3: 2, -4: 5, -5: 8,
		-6: 90, -7: 91, -16: 99, -10: 110, -11: 0,
		1: 1, 8: 8, '#': '#', '2': '2', 90: 90, 99: 99,
	} {
		if got := kwisGameAction(key); got != want {
			t.Errorf("getGameAction(%d) = %d, want %d", key, got, want)
		}
	}
}

func TestKWISGraphicsResetRestoresClip(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	image := invokeTestNative(
		t, vm, "org/kwis/msp/lcdui/Image", "createImage", "(II)Lorg/kwis/msp/lcdui/Image;",
		0, IntValue(4), IntValue(4),
	)
	imageReference, err := image.Reference()
	check(t, err)
	graphics := invokeTestNative(
		t, vm, "org/kwis/msp/lcdui/Image", "getGraphics", "()Lorg/kwis/msp/lcdui/Graphics;",
		imageReference,
	)
	graphicsReference, err := graphics.Reference()
	check(t, err)
	// A title draws one clipped sprite, resets, then clears the whole buffer.
	invokeTestNative(
		t, vm, kwisGraphicsClass, "setClip", "(IIII)V", graphicsReference,
		IntValue(0), IntValue(0), IntValue(1), IntValue(1),
	)
	invokeTestNative(t, vm, kwisGraphicsClass, "reset", "()V", graphicsReference)
	invokeTestNative(
		t, vm, kwisGraphicsClass, "setColor", "(I)V", graphicsReference, IntValue(0x00ff00),
	)
	invokeTestNative(
		t, vm, kwisGraphicsClass, "fillRect", "(IIII)V", graphicsReference,
		IntValue(0), IntValue(0), IntValue(4), IntValue(4),
	)
	state, err := vm.image(imageReference)
	check(t, err)
	pixel, err := vm.services.Graphics.Pixel(vm.serviceOwner, state.surface, 3, 3)
	check(t, err)
	if pixel != shared.RGB(0, 0xff, 0) {
		t.Fatalf("pixel outside the earlier clip = %+v; reset kept the clip", pixel)
	}
}

func TestKWISFileModesAndStreams(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	check(t, vm.SetXFileResourcesChecked(map[string][]byte{"score.dat": []byte("bundled")}))
	exists := func(name string) bool {
		value := invokeTestNative(
			t, vm, kwisFileSystemClass, "exists", "(Ljava/lang/String;)Z",
			0, ReferenceValue(vm.NewString(name)),
		)
		result, intErr := value.Int()
		check(t, intErr)
		return result != 0
	}
	open := func(name string, mode int32) uint32 {
		file := vm.NewObject(kwisFileClass, nil)
		invokeTestNative(
			t, vm, kwisFileClass, "<init>", "(Ljava/lang/String;II)V", file,
			ReferenceValue(vm.NewString(name)), IntValue(mode), IntValue(1),
		)
		return file
	}
	read := func(file uint32) []byte {
		size := invokeTestNative(t, vm, kwisFileClass, "sizeOf", "()I", file)
		length, intErr := size.Int()
		check(t, intErr)
		buffer := vm.NewByteArray(make([]byte, length))
		invokeTestNative(t, vm, kwisFileClass, "read", "([B)I", file, ReferenceValue(buffer))
		data, dataErr := vm.ByteArray(buffer)
		check(t, dataErr)
		return data
	}

	// A file delivered beside the application is there before any save.
	if !exists("score.dat") || exists("missing.dat") {
		t.Fatalf("exists(score.dat)=%v exists(missing.dat)=%v, want true and false",
			exists("score.dat"), exists("missing.dat"))
	}
	if got := read(open("score.dat", kwisFileReadOnly)); !bytes.Equal(got, []byte("bundled")) {
		t.Fatalf("READ_ONLY read %q, want the bundled file", got)
	}
	missing := vm.NewObject(kwisFileClass, nil)
	_, _, err = vm.natives[nativeKey{kwisFileClass, "<init>", "(Ljava/lang/String;II)V"}](
		context.Background(), vm, missing,
		[]Value{ReferenceValue(vm.NewString("missing.dat")), IntValue(kwisFileReadOnly), IntValue(1)},
	)
	if failure, ok := err.(*thrown); !ok || failure.class != "java/io/IOException" {
		t.Fatalf("READ_ONLY open of a missing file = %v, want java.io.IOException", err)
	}

	// WRITE_TRUNC replaces the contents through a DataOutputStream.
	file := open("score.dat", kwisFileWriteTrunc)
	stream := invokeTestNative(
		t, vm, kwisFileClass, "openDataOutputStream", "()Ljava/io/DataOutputStream;", file,
	)
	streamReference, err := stream.Reference()
	check(t, err)
	invokeTestNative(
		t, vm, "java/io/DataOutputStream", "write", "([BII)V", streamReference,
		ReferenceValue(vm.NewByteArray([]byte("new"))), IntValue(0), IntValue(3),
	)
	stored, err := vm.services.Storage.ReadFile(shared.NamespacePrivate, "/score.dat")
	if err != nil || !bytes.Equal(stored, []byte("new")) {
		t.Fatalf("WRITE_TRUNC stored %q, %v; want %q", stored, err, "new")
	}

	// WRITE appends; READ_WRITE overwrites from the first byte.
	invokeTestNative(
		t, vm, kwisFileClass, "write", "([B)I", open("score.dat", kwisFileWrite),
		ReferenceValue(vm.NewByteArray([]byte("er"))),
	)
	invokeTestNative(
		t, vm, kwisFileClass, "write", "([B)I", open("score.dat", kwisFileReadWrite),
		ReferenceValue(vm.NewByteArray([]byte("f"))),
	)
	if got := read(open("score.dat", kwisFileReadOnly)); !bytes.Equal(got, []byte("fewer")) {
		t.Fatalf("after WRITE and READ_WRITE the file holds %q, want %q", got, "fewer")
	}
}

func TestKWISClipTellsItsListenerWhenPlaybackEnds(t *testing.T) {
	listenerClass := kwisTestClass(t, "Listener", "java/lang/Object",
		kwisTestMethod{
			// first = event; second++ counts callbacks.
			name: "playUpdate", descriptor: kwisPlayUpdateDescriptor, maxLocals: 4,
			code: []byte{
				0x1c,                   // iload_2
				0xb3, 0, kwisTestFirst, // putstatic first
				0xb2, 0, kwisTestSecond, // getstatic second
				0x04,                    // iconst_1
				0x60,                    // iadd
				0xb3, 0, kwisTestSecond, // putstatic second
				0xb1, // return
			},
		},
	)
	vm, err := New(map[string][]byte{"Listener": listenerClass})
	check(t, err)
	listener, err := vm.allocateObject("Listener")
	check(t, err)
	clip := vm.NewObject(kwisClipClass, nil)
	invokeTestNative(
		t, vm, kwisClipClass, "<init>", "(Ljava/lang/String;[B)V", clip,
		ReferenceValue(vm.NewString("Yamaha_MA2")),
		ReferenceValue(vm.NewByteArray(skvmTestWave())),
	)
	invokeTestNative(
		t, vm, kwisClipClass, "setListener", "(Lorg/kwis/msp/media/PlayListener;)V",
		clip, ReferenceValue(listener),
	)
	invokeTestNative(t, vm, kwisClipClass, "setVolume", "(I)V", clip, IntValue(50))
	played := invokeTestNative(
		t, vm, kwisPlayerClass, "play", "(Lorg/kwis/msp/media/Clip;Z)Z",
		0, ReferenceValue(clip), IntValue(0),
	)
	if value, _ := played.Int(); value != 1 {
		t.Fatalf("Player.play = %d, want true", value)
	}
	if got := kwisTestStatic(t, vm, "Listener", "second"); got != 0 {
		t.Fatalf("listener ran %d times before the clip ended", got)
	}
	check(t, vm.Advance(context.Background(), time.Second, nil))
	if calls, event := kwisTestStatic(t, vm, "Listener", "second"),
		kwisTestStatic(t, vm, "Listener", "first"); calls != 1 || event != kwisPlayEndOfData {
		t.Fatalf("playUpdate ran %d times with event %d, want once with END_OF_DATA",
			calls, event)
	}
}

// TestKWISHybridTitleNativesAreRegistered pins the org.kwis.msp entry points
// the SKT corpus's WIPI Jlets call. One missing registration is a fatal
// "native method is unavailable" for the title that reaches it.
func TestKWISHybridTitleNativesAreRegistered(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	for _, key := range []nativeKey{
		{kwisFileClass, "<init>", "(Ljava/lang/String;II)V"},
		{kwisFileClass, "openDataOutputStream", "()Ljava/io/DataOutputStream;"},
		{kwisFileSystemClass, "exists", "(Ljava/lang/String;)Z"},
		{kwisFileSystemClass, "isFile", "(Ljava/lang/String;)Z"},
		{kwisFileSystemClass, "remove", "(Ljava/lang/String;)V"},
		{kwisCardClass, "<init>", "(Lorg/kwis/msp/lcdui/Display;)V"},
		{kwisCardClass, "repaint", "()V"},
		{kwisCardClass, "repaint", "(IIII)V"},
		{kwisCardClass, "serviceRepaints", "()V"},
		{kwisCardClass, "isShown", "()Z"},
		{kwisDisplayClass, "removeCard", "(Lorg/kwis/msp/lcdui/Card;)Z"},
		{kwisDisplayClass, "callSerially", "(Ljava/lang/Runnable;)V"},
		{kwisDisplayClass, "isDoubleBuffered", "()Z"},
		{kwisFontClass, "charWidth", "(C)I"},
		{kwisFontClass, "getHeight", "()I"},
		{kwisFontClass, "substringWidth", "(Ljava/lang/String;II)I"},
		{kwisGraphicsClass, "drawChar", "(CIII)V"},
		{kwisGraphicsClass, "getFont", "()Lorg/kwis/msp/lcdui/Font;"},
		{kwisGraphicsClass, "getClipX", "()I"},
		{kwisGraphicsClass, "reset", "()V"},
		{"org/kwis/msp/lcdui/Jlet", "getAppProperty", "(Ljava/lang/String;)Ljava/lang/String;"},
		{kwisClipClass, "<init>", "(Ljava/lang/String;[B)V"},
		{kwisClipClass, "setListener", "(Lorg/kwis/msp/media/PlayListener;)V"},
		{kwisClipClass, "setVolume", "(I)V"},
		{kwisPlayerClass, "play", "(Lorg/kwis/msp/media/Clip;Z)Z"},
		{kwisPlayerClass, "stop", "(Lorg/kwis/msp/media/Clip;)Z"},
	} {
		if vm.natives[key] == nil {
			t.Errorf("native %s.%s%s is not registered", key.class, key.name, key.descriptor)
		}
	}
}
