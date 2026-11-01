package skvm

import (
	"context"
	"math"
	"testing"
	"time"

	shared "github.com/mirusu400/aram-core/runtime"
)

// sktCorpusExtensionMethods is every com/skt/m entry point referenced by the
// SKT titles in the private corpus. A missing registration is a hard
// "SKVM native method ... is unavailable" fault the moment a title reaches it
// (issues #559/#560: (SKT) 교실이데아 died on Graphics2D.invertRect).
var sktCorpusExtensionMethods = []nativeKey{
	{"com/skt/m/AudioClip", "close", "()V"},
	{"com/skt/m/AudioClip", "loop", "()V"},
	{"com/skt/m/AudioClip", "open", "([BII)V"},
	{"com/skt/m/AudioClip", "pause", "()V"},
	{"com/skt/m/AudioClip", "play", "()V"},
	{"com/skt/m/AudioClip", "resume", "()V"},
	{"com/skt/m/AudioClip", "stop", "()V"},
	{"com/skt/m/AudioSystem", "getAudioClip", "(Ljava/lang/String;)Lcom/skt/m/AudioClip;"},
	{"com/skt/m/AudioSystem", "getMaxVolume", "(Ljava/lang/String;)I"},
	{"com/skt/m/AudioSystem", "getVolume", "(Ljava/lang/String;)I"},
	{"com/skt/m/AudioSystem", "setVolume", "(Ljava/lang/String;I)V"},
	{"com/skt/m/BackLight", "off", "()V"},
	{"com/skt/m/BackLight", "on", "(I)V"},
	{"com/skt/m/Device", "enableRestoreLCD", "(Z)V"},
	{"com/skt/m/Device", "invokeWapBrowser", "(Ljava/lang/String;)V"},
	{"com/skt/m/Device", "isBacklightEnabled", "()Z"},
	{"com/skt/m/Device", "isKeyToneEnabled", "()Z"},
	{"com/skt/m/Device", "setBacklightEnabled", "(Z)V"},
	{"com/skt/m/Device", "setColorMode", "(I)V"},
	{"com/skt/m/Device", "setKeyRepeatTime", "(II)V"},
	{"com/skt/m/Device", "setKeyToneEnabled", "(Z)V"},
	{"com/skt/m/Device", "setMelody", "(ILjava/lang/String;[B)Z"},
	{"com/skt/m/Graphics2D", "captureLCD", "(IIII)Ljavax/microedition/lcdui/Image;"},
	{"com/skt/m/Graphics2D", "createMaskableImage", "(II)Ljavax/microedition/lcdui/Image;"},
	{"com/skt/m/Graphics2D", "drawImage", "(IILjavax/microedition/lcdui/Image;IIIII)V"},
	{"com/skt/m/Graphics2D", "getGraphics2D", "(Ljavax/microedition/lcdui/Graphics;)Lcom/skt/m/Graphics2D;"},
	{"com/skt/m/Graphics2D", "getPixel", "(II)I"},
	{"com/skt/m/Graphics2D", "invertRect", "(IIII)V"},
	{"com/skt/m/Graphics2D", "setPixel", "(III)V"},
	{"com/skt/m/MathFP", "add", "(JJ)J"},
	{"com/skt/m/MathFP", "atan", "(J)J"},
	{"com/skt/m/MathFP", "cos", "(J)J"},
	{"com/skt/m/MathFP", "divide", "(JJ)J"},
	{"com/skt/m/MathFP", "multiply", "(JJ)J"},
	{"com/skt/m/MathFP", "parseFP", "(J)J"},
	{"com/skt/m/MathFP", "parseFPString", "(Ljava/lang/String;)J"},
	{"com/skt/m/MathFP", "round", "(J)J"},
	{"com/skt/m/MathFP", "sin", "(J)J"},
	{"com/skt/m/MathFP", "sqrt", "(J)J"},
	{"com/skt/m/MathFP", "sub", "(JJ)J"},
	{"com/skt/m/MathFP", "toLong", "(J)J"},
	{"com/skt/m/ProgressBar", "<init>", "(Ljava/lang/String;)V"},
	{"com/skt/m/ProgressBar", "getValue", "()I"},
	{"com/skt/m/ProgressBar", "setMaxValue", "(I)V"},
	{"com/skt/m/ProgressBar", "setValue", "(I)V"},
	{"com/skt/m/Vibration", "getLevelNum", "()I"},
	{"com/skt/m/Vibration", "isSupported", "()Z"},
	{"com/skt/m/Vibration", "start", "(II)V"},
	{"com/skt/m/Vibration", "stop", "()V"},
}

func TestSKTCorpusExtensionMethodsAreRegistered(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	for _, key := range sktCorpusExtensionMethods {
		if vm.natives[key] == nil {
			t.Errorf("SKT native %s.%s%s is unavailable", key.class, key.name, key.descriptor)
		}
	}
}

// TestSKTGraphics2DInvertRectInvertsTranslatedClippedArea covers (SKT)
// 교실이데아 (#559/#560), which inverts its menu highlight. The inversion
// follows the Graphics translation and clip, ignores the current colour, and
// leaves the receiver's raster mode as it was.
func TestSKTGraphics2DInvertRectInvertsTranslatedClippedArea(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	graphicsReference, graphics := midpGraphicsSurface(t, vm)
	graphics2D := invokeTestNative(
		t, vm,
		"com/skt/m/Graphics2D", "getGraphics2D",
		"(Ljavax/microedition/lcdui/Graphics;)Lcom/skt/m/Graphics2D;",
		0, ReferenceValue(graphicsReference),
	)
	graphics2DReference, err := graphics2D.Reference()
	check(t, err)

	// Paint a known background, then pick a colour the inversion must ignore.
	graphics.color = 0xff123456
	check(t, fillRectangle(vm, graphics, 0, 0, 20, 20, graphics.color))
	graphics.color = 0xff00ff00
	invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "translate", "(II)V",
		graphicsReference, IntValue(2), IntValue(3))
	invokeTestNative(t, vm, "javax/microedition/lcdui/Graphics", "clipRect", "(IIII)V",
		graphicsReference, IntValue(0), IntValue(0), IntValue(4), IntValue(100))

	invokeTestNative(t, vm, "com/skt/m/Graphics2D", "invertRect", "(IIII)V",
		graphics2DReference, IntValue(1), IntValue(1), IntValue(10), IntValue(2))

	inverted := shared.Color{R: 0xed, G: 0xcb, B: 0xa9, A: 0xff}
	untouched := shared.Color{R: 0x12, G: 0x34, B: 0x56, A: 0xff}
	for _, probe := range []struct {
		x, y int32
		want shared.Color
	}{
		{3, 4, inverted},  // (1,1) translated by (2,3)
		{5, 5, inverted},  // last clipped column, last row
		{6, 4, untouched}, // right of the 4-wide clip
		{3, 6, untouched}, // below the 2-high rectangle
		{2, 4, untouched}, // left of the rectangle
	} {
		if got := midpPixel(t, vm, graphics.surface, probe.x, probe.y); got != probe.want {
			t.Errorf("pixel (%d,%d) = %+v, want %+v", probe.x, probe.y, got, probe.want)
		}
	}
	state, err := vm.services.Graphics.DrawState(vm.serviceOwner, graphics.surface)
	check(t, err)
	if state.Raster != shared.RasterCopy {
		t.Fatalf("raster after invertRect = %v, want copy", state.Raster)
	}

	// Inverting twice restores the original pixels.
	invokeTestNative(t, vm, "com/skt/m/Graphics2D", "invertRect", "(IIII)V",
		graphics2DReference, IntValue(1), IntValue(1), IntValue(10), IntValue(2))
	if got := midpPixel(t, vm, graphics.surface, 3, 4); got != untouched {
		t.Fatalf("double inversion = %+v, want %+v", got, untouched)
	}
	// An empty rectangle draws nothing and does not fault.
	invokeTestNative(t, vm, "com/skt/m/Graphics2D", "invertRect", "(IIII)V",
		graphics2DReference, IntValue(1), IntValue(1), IntValue(-4), IntValue(2))
}

// TestSKTMathFPUsesDoubleBits pins the SKT MathFP representation. Titles
// inline MathFP.PI as 4614256656552045848L (미니스포츠클럽, 로맨스소드), the
// IEEE-754 bits of Math.PI, so operations act on doubles carried in a long.
func TestSKTMathFPUsesDoubleBits(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	call := func(name, descriptor string, args ...Value) Value {
		t.Helper()
		return invokeTestNative(t, vm, sktMathClass, name, descriptor, 0, args...)
	}
	fp := func(value Value) float64 {
		t.Helper()
		bits, err := value.Long()
		check(t, err)
		return math.Float64frombits(uint64(bits))
	}
	long := func(value Value) int64 {
		t.Helper()
		result, err := value.Long()
		check(t, err)
		return result
	}
	const inlinedPI = int64(4614256656552045848)
	if got := int64(math.Float64bits(math.Pi)); got != inlinedPI {
		t.Fatalf("Math.PI bits = %d, want the inlined title constant %d", got, inlinedPI)
	}
	if got := vm.hostStatic[fieldStorageKey(sktMathClass, "PI", "J")]; got != LongValue(inlinedPI) {
		t.Fatalf("MathFP.PI = %+v, want %d", got, inlinedPI)
	}

	// 미니스포츠클럽: radian = PI / parseFP(180); dirAngle = toLong(angle / radian).
	radian := call("divide", "(JJ)J", LongValue(inlinedPI), call("parseFP", "(J)J", LongValue(180)))
	if got := fp(radian); got != math.Pi/180 {
		t.Fatalf("PI/180 = %v", got)
	}
	angle := call("multiply", "(JJ)J", call("parseFP", "(J)J", LongValue(30)), radian)
	sine := call("sin", "(J)J", angle)
	scaled := call("multiply", "(JJ)J", sine, call("parseFP", "(J)J", LongValue(300)))
	if got := long(call("toLong", "(J)J", call("round", "(J)J", scaled))); got != 150 {
		t.Fatalf("toLong(round(sin(30deg)*300)) = %d, want 150", got)
	}
	degrees := call("divide", "(JJ)J",
		call("atan", "(J)J", call("parseFP", "(J)J", LongValue(1))), radian)
	if got := long(call("toLong", "(J)J", call("round", "(J)J", degrees))); got != 45 {
		t.Fatalf("atan(1) in degrees = %d, want 45", got)
	}

	// 코인마스터 / 컴투스프로야구2 parse decimal strings.
	if got := fp(call("parseFPString", "(Ljava/lang/String;)J",
		ReferenceValue(vm.NewString("13.3")))); got != 13.3 {
		t.Fatalf("parseFPString(13.3) = %v", got)
	}
	difference := call("sub", "(JJ)J",
		call("parseFPString", "(Ljava/lang/String;)J", ReferenceValue(vm.NewString("1.5"))),
		call("parseFP", "(J)J", LongValue(-1)))
	if got := fp(call("add", "(JJ)J", difference, call("parseFP", "(J)J", LongValue(1)))); got != 3.5 {
		t.Fatalf("1.5 - -1 + 1 = %v", got)
	}

	// 로맨스소드: toLong(sqrt(parseFP(n))). toLong truncates like a Java cast.
	if got := long(call("toLong", "(J)J", call("sqrt", "(J)J", call("parseFP", "(J)J", LongValue(50))))); got != 7 {
		t.Fatalf("toLong(sqrt(50)) = %d, want 7", got)
	}
	if got := long(call("toLong", "(J)J", call("parseFPString", "(Ljava/lang/String;)J",
		ReferenceValue(vm.NewString("-2.9"))))); got != -2 {
		t.Fatalf("toLong(-2.9) = %d, want -2", got)
	}

	// A malformed decimal is a Java exception, not a VM fault.
	native := vm.natives[nativeKey{sktMathClass, "parseFPString", "(Ljava/lang/String;)J"}]
	if _, _, err := native(context.Background(), vm, 0, []Value{
		ReferenceValue(vm.NewString("1.2.3")),
	}); err == nil {
		t.Fatal("parseFPString accepted a malformed decimal")
	}
}

// TestSKTAudioClipPauseKeepsTheWorkerParked covers AudioClip.pause/resume
// (크레이지버스). A paused clip has not stopped, so the worker blocked in play
// or loop stays parked; resume continues it and stop still releases it.
// Either call in the wrong state is a harmless no-op.
func TestSKTAudioClipPauseKeepsTheWorkerParked(t *testing.T) {
	vm, clip, clipReference := newAudioWorkerVM(t)
	// Neither call faults before the clip has ever played.
	invokeTestNative(t, vm, "com/skt/m/AudioClip", "resume", "()V", clipReference)
	invokeTestNative(t, vm, "com/skt/m/AudioClip", "pause", "()V", clipReference)

	worker := vm.NewObject("Worker", nil)
	thread := vm.NewObject("java/lang/Thread", nil)
	invokeTestNative(t, vm, "java/lang/Thread", "<init>", "(Ljava/lang/Runnable;)V",
		thread, ReferenceValue(worker))
	invokeTestNative(t, vm, "java/lang/Thread", "start", "()V", thread)
	if clip.clip == 0 {
		t.Fatal("the worker closed its clip before the sound played")
	}

	invokeTestNative(t, vm, "com/skt/m/AudioClip", "pause", "()V", clipReference)
	invokeTestNative(t, vm, "com/skt/m/AudioClip", "pause", "()V", clipReference)
	info, err := vm.services.Media.Info(vm.serviceOwner, clip.clip)
	check(t, err)
	if info.State != shared.ClipPaused {
		t.Fatalf("clip state after pause = %v, want paused", info.State)
	}
	check(t, vm.Advance(context.Background(), 5*time.Second, nil))
	if clip.clip == 0 {
		t.Fatal("the worker resumed and closed its clip while it was paused")
	}

	invokeTestNative(t, vm, "com/skt/m/AudioClip", "resume", "()V", clipReference)
	info, err = vm.services.Media.Info(vm.serviceOwner, clip.clip)
	check(t, err)
	if info.State != shared.ClipPlaying {
		t.Fatalf("clip state after resume = %v, want playing", info.State)
	}

	invokeTestNative(t, vm, "com/skt/m/AudioClip", "stop", "()V", clipReference)
	if clip.clip != 0 {
		t.Fatalf("stopped clip = %s, want the worker to have closed it", clip.clip)
	}
	// Both calls on a closed clip are no-ops too.
	invokeTestNative(t, vm, "com/skt/m/AudioClip", "pause", "()V", clipReference)
	invokeTestNative(t, vm, "com/skt/m/AudioClip", "resume", "()V", clipReference)
}

func TestSKTDeviceKeyRepeatAndMelody(t *testing.T) {
	vm, err := New(map[string][]byte{"Game": syntheticClass(t)})
	check(t, err)
	invokeTestNative(t, vm, "com/skt/m/Device", "setKeyRepeatTime", "(II)V", 0,
		IntValue(300), IntValue(100))
	installed := invokeTestNative(t, vm, "com/skt/m/Device", "setMelody",
		"(ILjava/lang/String;[B)Z", 0,
		IntValue(0), ReferenceValue(vm.NewString("bell")),
		ReferenceValue(vm.NewByteArray([]byte{1, 2, 3})))
	if installed != boolValue(false) {
		t.Fatalf("setMelody = %+v, want false: there is no handset melody store", installed)
	}
}
