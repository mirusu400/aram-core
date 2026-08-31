package skvm

import (
	"context"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

// midpGraphicsSurface hands back the screen Graphics object plus its state so a
// test can read the pixels a native wrote.
func midpGraphicsSurface(t *testing.T, vm *VM) (uint32, *graphicsState) {
	t.Helper()
	reference := vm.ScreenGraphics()
	state, err := vm.graphics(reference)
	if err != nil {
		t.Fatal(err)
	}
	return reference, state
}

func midpPixel(t *testing.T, vm *VM, surface shared.ServiceID, x, y int32) shared.Color {
	t.Helper()
	color, err := vm.services.Graphics.Pixel(vm.serviceOwner, surface, x, y)
	if err != nil {
		t.Fatal(err)
	}
	return color
}

// TestSKVMMIDPGraphicsExposesEveryDrawingEntryPoint guards the whole
// javax.microedition.lcdui.Graphics surface. A method the class declares but
// the VM never registered is not a silent no-op: the interpreter faults the
// title with "native method ... is unavailable", which is how 고래사냥2 died the
// moment a menu drew an arc (aram-core issue #117, aram-frontend issue #20).
func TestSKVMMIDPGraphicsExposesEveryDrawingEntryPoint(t *testing.T) {
	vm, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []struct {
		name       string
		descriptor string
	}{
		{"clipRect", "(IIII)V"},
		{"copyArea", "(IIIIIII)V"},
		{"drawArc", "(IIIIII)V"},
		{"drawChar", "(CIII)V"},
		{"drawChars", "([CIIIII)V"},
		{"drawImage", "(Ljavax/microedition/lcdui/Image;III)V"},
		{"drawLine", "(IIII)V"},
		{"drawRGB", "([IIIIIIIZ)V"},
		{"drawRect", "(IIII)V"},
		{"drawRegion", "(Ljavax/microedition/lcdui/Image;IIIIIIII)V"},
		{"drawRoundRect", "(IIIIII)V"},
		{"drawString", "(Ljava/lang/String;III)V"},
		{"drawSubstring", "(Ljava/lang/String;IIIII)V"},
		{"fillArc", "(IIIIII)V"},
		{"fillRect", "(IIII)V"},
		{"fillRoundRect", "(IIIIII)V"},
		{"fillTriangle", "(IIIIII)V"},
		{"getBlueComponent", "()I"},
		{"getClipHeight", "()I"},
		{"getClipWidth", "()I"},
		{"getClipX", "()I"},
		{"getClipY", "()I"},
		{"getColor", "()I"},
		{"getDisplayColor", "(I)I"},
		{"getFont", "()Ljavax/microedition/lcdui/Font;"},
		{"getGrayScale", "()I"},
		{"getGreenComponent", "()I"},
		{"getRedComponent", "()I"},
		{"getStrokeStyle", "()I"},
		{"getTranslateX", "()I"},
		{"getTranslateY", "()I"},
		{"setClip", "(IIII)V"},
		{"setColor", "(I)V"},
		{"setColor", "(III)V"},
		{"setFont", "(Ljavax/microedition/lcdui/Font;)V"},
		{"setGrayScale", "(I)V"},
		{"setStrokeStyle", "(I)V"},
		{"translate", "(II)V"},
	} {
		key := nativeKey{
			class:      "javax/microedition/lcdui/Graphics",
			name:       method.name,
			descriptor: method.descriptor,
		}
		if vm.natives[key] == nil {
			t.Errorf(
				"MIDP Graphics.%s%s is unavailable",
				method.name,
				method.descriptor,
			)
		}
	}
}

// TestSKVMMIDPDrawArcOutlinesWithoutFilling proves drawArc reaches the shared
// rasterizer and stays an outline, unlike its fillArc sibling.
func TestSKVMMIDPDrawArcOutlinesWithoutFilling(t *testing.T) {
	vm, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	graphics, state := midpGraphicsSurface(t, vm)
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "setColor", "(I)V",
		graphics, IntValue(0x00ff0000),
	)
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "drawArc", "(IIIIII)V",
		graphics,
		IntValue(0), IntValue(0), IntValue(10), IntValue(10),
		IntValue(0), IntValue(360),
	)
	red := shared.Color{R: 0xff, A: 0xff}
	if edge := midpPixel(t, vm, state.surface, 0, 5); edge != red {
		t.Fatalf("arc left edge = %+v, want %+v", edge, red)
	}
	if center := midpPixel(t, vm, state.surface, 5, 5); center == red {
		t.Fatalf("drawArc filled its interior at (5,5)")
	}

	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "fillArc", "(IIIIII)V",
		graphics,
		IntValue(0), IntValue(0), IntValue(10), IntValue(10),
		IntValue(0), IntValue(360),
	)
	if center := midpPixel(t, vm, state.surface, 5, 5); center != red {
		t.Fatalf("fillArc interior = %+v, want %+v", center, red)
	}
}

// midpTwoPixelImage builds a one-row image whose left pixel is red and whose
// right pixel is green, so a transform is readable from two pixels.
func midpTwoPixelImage(t *testing.T, vm *VM) uint32 {
	t.Helper()
	image, err := vm.newImageState(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	for index, color := range []shared.Color{
		{R: 0xff, A: 0xff},
		{G: 0xff, A: 0xff},
	} {
		if err := vm.services.Graphics.SetPixel(
			vm.serviceOwner,
			image.surface,
			int32(index),
			0,
			color,
		); err != nil {
			t.Fatal(err)
		}
	}
	return vm.NewObject("javax/microedition/lcdui/Image", image)
}

func TestSKVMMIDPDrawRegionAppliesTransform(t *testing.T) {
	red := shared.Color{R: 0xff, A: 0xff}
	green := shared.Color{G: 0xff, A: 0xff}
	for _, testCase := range []struct {
		name      string
		transform int32
		first     [2]int32
		second    [2]int32
	}{
		{"none", transNone, [2]int32{4, 6}, [2]int32{5, 6}},
		{"mirror", transMirror, [2]int32{5, 6}, [2]int32{4, 6}},
		{"rot90", transRot90, [2]int32{4, 6}, [2]int32{4, 7}},
		{"rot270", transRot270, [2]int32{4, 7}, [2]int32{4, 6}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			vm, err := New(nil)
			if err != nil {
				t.Fatal(err)
			}
			graphics, state := midpGraphicsSurface(t, vm)
			image := midpTwoPixelImage(t, vm)
			invokeTestNative(
				t, vm,
				"javax/microedition/lcdui/Graphics",
				"drawRegion",
				"(Ljavax/microedition/lcdui/Image;IIIIIIII)V",
				graphics,
				ReferenceValue(image),
				IntValue(0), IntValue(0), IntValue(2), IntValue(1),
				IntValue(testCase.transform),
				IntValue(4), IntValue(6), IntValue(0),
			)
			got := midpPixel(t, vm, state.surface, testCase.first[0], testCase.first[1])
			if got != red {
				t.Fatalf("red pixel at %v = %+v", testCase.first, got)
			}
			got = midpPixel(t, vm, state.surface, testCase.second[0], testCase.second[1])
			if got != green {
				t.Fatalf("green pixel at %v = %+v", testCase.second, got)
			}
		})
	}
}

func TestSKVMMIDPCopyAreaMovesPixels(t *testing.T) {
	vm, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	graphics, state := midpGraphicsSurface(t, vm)
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "setColor", "(I)V",
		graphics, IntValue(0x000000ff),
	)
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "fillRect", "(IIII)V",
		graphics, IntValue(1), IntValue(1), IntValue(2), IntValue(2),
	)
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "copyArea", "(IIIIIII)V",
		graphics,
		IntValue(1), IntValue(1), IntValue(2), IntValue(2),
		IntValue(20), IntValue(30), IntValue(0),
	)
	blue := shared.Color{B: 0xff, A: 0xff}
	for _, point := range [][2]int32{{20, 30}, {21, 30}, {20, 31}, {21, 31}} {
		if got := midpPixel(t, vm, state.surface, point[0], point[1]); got != blue {
			t.Fatalf("copied pixel at %v = %+v, want %+v", point, got, blue)
		}
	}
	if got := midpPixel(t, vm, state.surface, 22, 30); got == blue {
		t.Fatalf("copyArea wrote past its region at (22,30)")
	}
}

// TestSKVMMIDPDrawRGBHonoursAlphaFlagAndScanLength covers the two parts of
// drawRGB a guest can get wrong for us: an opaque block requested with
// processAlpha false, and the negative scan length MIDP allows so a block can
// be drawn bottom-up without being copied first.
func TestSKVMMIDPDrawRGBHonoursAlphaFlagAndScanLength(t *testing.T) {
	vm, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	graphics, state := midpGraphicsSurface(t, vm)
	// Fully transparent red and green: with processAlpha false both must land
	// opaque.
	pixels := vm.newArray("[I", []Value{
		IntValue(0x00ff0000),
		IntValue(0x0000ff00),
	})
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "drawRGB", "([IIIIIIIZ)V",
		graphics,
		ReferenceValue(pixels),
		IntValue(0), IntValue(1),
		IntValue(2), IntValue(3),
		IntValue(1), IntValue(2),
		IntValue(0),
	)
	red := shared.Color{R: 0xff, A: 0xff}
	green := shared.Color{G: 0xff, A: 0xff}
	if got := midpPixel(t, vm, state.surface, 2, 3); got != red {
		t.Fatalf("drawRGB row 0 = %+v, want %+v", got, red)
	}
	if got := midpPixel(t, vm, state.surface, 2, 4); got != green {
		t.Fatalf("drawRGB row 1 = %+v, want %+v", got, green)
	}

	// A negative scan length walks the same array bottom-up.
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "drawRGB", "([IIIIIIIZ)V",
		graphics,
		ReferenceValue(pixels),
		IntValue(1), IntValue(-1),
		IntValue(8), IntValue(3),
		IntValue(1), IntValue(2),
		IntValue(0),
	)
	if got := midpPixel(t, vm, state.surface, 8, 3); got != green {
		t.Fatalf("mirrored drawRGB row 0 = %+v, want %+v", got, green)
	}
	if got := midpPixel(t, vm, state.surface, 8, 4); got != red {
		t.Fatalf("mirrored drawRGB row 1 = %+v, want %+v", got, red)
	}
}

func TestSKVMMIDPFillTriangleRastersOntoSharedSurface(t *testing.T) {
	vm, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	graphics, state := midpGraphicsSurface(t, vm)
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "setColor", "(I)V",
		graphics, IntValue(0x0000ff00),
	)
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "fillTriangle", "(IIIIII)V",
		graphics,
		IntValue(0), IntValue(0),
		IntValue(10), IntValue(0),
		IntValue(0), IntValue(10),
	)
	green := shared.Color{G: 0xff, A: 0xff}
	if got := midpPixel(t, vm, state.surface, 1, 1); got != green {
		t.Fatalf("triangle interior = %+v, want %+v", got, green)
	}
	if got := midpPixel(t, vm, state.surface, 9, 9); got == green {
		t.Fatalf("triangle covered the opposite corner at (9,9)")
	}
}

func TestSKVMMIDPColorAndStrokeAccessors(t *testing.T) {
	vm, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	graphics, _ := midpGraphicsSurface(t, vm)
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "setColor", "(I)V",
		graphics, IntValue(0x00123456),
	)
	for _, component := range []struct {
		name string
		want int32
	}{
		{"getRedComponent", 0x12},
		{"getGreenComponent", 0x34},
		{"getBlueComponent", 0x56},
	} {
		got := invokeTestNative(
			t, vm, "javax/microedition/lcdui/Graphics", component.name, "()I",
			graphics,
		)
		if got != IntValue(component.want) {
			t.Fatalf("%s = %+v, want %d", component.name, got, component.want)
		}
	}
	got := invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "getDisplayColor", "(I)I",
		graphics, IntValue(0x00abcdef),
	)
	if got != IntValue(0x00abcdef) {
		t.Fatalf("getDisplayColor = %+v, want the requested color", got)
	}

	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "setGrayScale", "(I)V",
		graphics, IntValue(0x40),
	)
	got = invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "getColor", "()I",
		graphics,
	)
	if got != IntValue(0x00404040) {
		t.Fatalf("setGrayScale color = %+v, want 0x404040", got)
	}
	got = invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "getGrayScale", "()I",
		graphics,
	)
	if got != IntValue(0x40) {
		t.Fatalf("getGrayScale = %+v, want 0x40", got)
	}

	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "setStrokeStyle", "(I)V",
		graphics, IntValue(strokeDotted),
	)
	got = invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "getStrokeStyle", "()I",
		graphics,
	)
	if got != IntValue(strokeDotted) {
		t.Fatalf("getStrokeStyle = %+v, want DOTTED", got)
	}
	native := vm.natives[nativeKey{
		class:      "javax/microedition/lcdui/Graphics",
		name:       "setStrokeStyle",
		descriptor: "(I)V",
	}]
	if _, _, err := native(
		context.Background(),
		vm,
		graphics,
		[]Value{IntValue(7)},
	); err == nil {
		t.Fatal("setStrokeStyle(7) was accepted, want IllegalArgumentException")
	}
}

// TestSKVMGraphicsStrokeStyleSurvivesStateRoundTrip keeps the stroke style in
// the save state: a title that sets it once at startup would otherwise draw
// with a different style after a restore.
func TestSKVMGraphicsStrokeStyleSurvivesStateRoundTrip(t *testing.T) {
	vm, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	graphics, state := midpGraphicsSurface(t, vm)
	invokeTestNative(
		t, vm, "javax/microedition/lcdui/Graphics", "setStrokeStyle", "(I)V",
		graphics, IntValue(strokeDotted),
	)
	saved, err := snapshotNative(graphics, state, map[any]uint32{state: graphics})
	if err != nil {
		t.Fatal(err)
	}
	restored, _, err := restoreNative(saved)
	if err != nil {
		t.Fatal(err)
	}
	loaded, ok := restored.(*graphicsState)
	if !ok {
		t.Fatalf("restored native state = %T, want *graphicsState", restored)
	}
	if loaded.stroke != strokeDotted {
		t.Fatalf("restored stroke = %d, want %d", loaded.stroke, strokeDotted)
	}
}
