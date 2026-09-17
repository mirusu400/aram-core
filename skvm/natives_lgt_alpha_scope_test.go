package skvm

import (
	"bytes"
	"context"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func lgtFillWhite(t *testing.T, vm *VM, ref uint32, x int32) {
	t.Helper()
	invokeTestNative(t, vm, lgtBaseGraphics, "setColor", "(I)V", ref, IntValue(0xffffff))
	invokeTestNative(t, vm, lgtBaseGraphics, "fillRect", "(IIII)V", ref, IntValue(x), IntValue(0), IntValue(1), IntValue(1))
}

func TestLGTGraphicsAlphaContextIsolation(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	image := invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "createImage", "(II)Ljavax/microedition/lcdui/Image;", 0, IntValue(8), IntValue(8))
	imageRef, err := image.Reference()
	check(t, err)
	get := func() uint32 {
		v := invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "getGraphics", "()Ljavax/microedition/lcdui/Graphics;", imageRef)
		r, e := v.Reference()
		check(t, e)
		return r
	}
	first, second := get(), get()
	state, err := vm.graphics(first)
	check(t, err)
	check(t, vm.services.Graphics.Clear(vm.serviceOwner, state.surface, shared.Color{A: 255}))
	lgtAlpha(t, vm, first, 0)
	lgtFillWhite(t, vm, first, 0)
	lgtFillWhite(t, vm, second, 1) // Fresh context defaults opaque, on same surface.
	lgtAlpha(t, vm, second, 128)
	lgtFillWhite(t, vm, second, 2)
	lgtFillWhite(t, vm, first, 3)
	for x, want := range []uint8{0, 255, 128, 0} {
		if got := midpPixel(t, vm, state.surface, int32(x), 0); got != (shared.Color{R: want, G: want, B: want, A: 255}) {
			t.Fatalf("context pixel%d = %+v", x, got)
		}
	}
	saved, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	lgtFillWhite(t, vm, first, 4)
	lgtFillWhite(t, vm, second, 5)
	if got := midpPixel(t, vm, state.surface, 4, 0); got != (shared.Color{A: 255}) {
		t.Fatalf("explicit zero lost: %+v", got)
	}
	if got := midpPixel(t, vm, state.surface, 5, 0); got != (shared.Color{R: 128, G: 128, B: 128, A: 255}) {
		t.Fatalf("context alpha restore lost: %+v", got)
	}
	// The per-draw service scope must have ended; direct presentation copies do
	// not inherit either object's alpha, including after restoring a snapshot.
	draw, err := vm.services.Graphics.DrawState(vm.serviceOwner, state.surface)
	check(t, err)
	if draw.GlobalTransparency256 != 0 {
		t.Fatal("object alpha leaked into shared surface")
	}
}

func TestLGTGraphicsAlphaSourceCoverageClipAndErrors(t *testing.T) {
	for _, process := range []int32{0, 1} {
		vm := policyRegressionVM(t, NativePolicyLGT)
		ref, state := midpGraphicsSurface(t, vm)
		check(t, vm.services.Graphics.Clear(vm.serviceOwner, state.surface, shared.Color{A: 255}))
		lgtAlpha(t, vm, ref, 128)
		invokeTestNative(t, vm, lgtBaseGraphics, "translate", "(II)V", ref, IntValue(2), IntValue(3))
		invokeTestNative(t, vm, lgtBaseGraphics, "setClip", "(IIII)V", ref, IntValue(0), IntValue(0), IntValue(1), IntValue(1))
		source := vm.newArray("[I", []Value{IntValue(-2130706433), IntValue(-1)}) // 0x80ffffff, 0xffffffff
		invokeTestNative(t, vm, lgtBaseGraphics, "drawRGB", "([IIIIIIIZ)V", ref, ReferenceValue(source), IntValue(0), IntValue(2), IntValue(0), IntValue(0), IntValue(2), IntValue(1), IntValue(process))
		want := uint8(128)
		if process != 0 {
			want = 64
		}
		if got := midpPixel(t, vm, state.surface, 2, 3); got != (shared.Color{R: want, G: want, B: want, A: 255}) {
			t.Fatalf("source flag%d = %+v", process, got)
		}
		if got := midpPixel(t, vm, state.surface, 3, 3); got != (shared.Color{A: 255}) {
			t.Fatalf("clip leaked: %+v", got)
		}
		original, err := vm.services.Graphics.DrawState(vm.serviceOwner, state.surface)
		check(t, err)
		native := vm.natives[nativeKey{class: lgtBaseGraphics, name: "drawRGB", descriptor: "([IIIIIIIZ)V"}]
		_, _, err = native(context.Background(), vm, ref, []Value{ReferenceValue(0), IntValue(0), IntValue(1), IntValue(0), IntValue(0), IntValue(1), IntValue(1), IntValue(1)})
		if err == nil {
			t.Fatal("null drawRGB succeeded")
		}
		after, err := vm.services.Graphics.DrawState(vm.serviceOwner, state.surface)
		check(t, err)
		if after != original {
			t.Fatal("failed draw leaked temporary state")
		}
	}
}

func TestLGTGraphicsAlphaImageAndDirectRegionSourceCoverage(t *testing.T) {
	for _, alpha := range []int32{0, 128, 256} {
		vm := policyRegressionVM(t, NativePolicyLGT)
		ref, state := midpGraphicsSurface(t, vm)
		check(t, vm.services.Graphics.Clear(vm.serviceOwner, state.surface, shared.Color{A: 255}))
		image, err := vm.newImageState(2, 1)
		check(t, err)
		// Raw synthetic storage retains straight source RGB and its alpha. Plotting
		// into the source would blend it prematurely and make this a weak test.
		check(t, vm.services.Graphics.WritePixelBytes(vm.serviceOwner, image.surface, 0, []byte{255, 255, 255, 128, 255, 255, 255, 0}))
		source := vm.NewObject("javax/microedition/lcdui/Image", image)
		lgtAlpha(t, vm, ref, alpha)
		invokeTestNative(t, vm, lgtBaseGraphics, "drawImage", "(Ljavax/microedition/lcdui/Image;III)V", ref, ReferenceValue(source), IntValue(0), IntValue(0), IntValue(0))
		_, _, err = nativeDrawRegion(context.Background(), vm, ref, []Value{ReferenceValue(source), IntValue(0), IntValue(0), IntValue(2), IntValue(1), IntValue(2), IntValue(2), IntValue(0), IntValue(0)})
		check(t, err)
		want := uint8(alpha / 2)
		for _, x := range []int32{0, 3} {
			if got := midpPixel(t, vm, state.surface, x, 0); got != (shared.Color{R: want, G: want, B: want, A: 255}) {
				t.Fatalf("alpha%d image pixel%d = %+v", alpha, x, got)
			}
		}
		for _, x := range []int32{1, 2} {
			if got := midpPixel(t, vm, state.surface, x, 0); got != (shared.Color{A: 255}) {
				t.Fatalf("transparent texel%d = %+v", x, got)
			}
		}
	}
}

func TestLGTGraphicsAlphaLegacyAndMalformedNativeState(t *testing.T) {
	// Offset was zero/unused in the old graphics native payload. Other native
	// kinds have independent Offset meanings and are unaffected.
	legacy := nativeState{Kind: "graphics", Width: 1, Height: 1, Color: 0xff000000}
	restored, _, err := restoreNative(legacy)
	check(t, err)
	if restored.(*graphicsState).transparency256 != 0 {
		t.Fatal("old state is not opaque")
	}
	for _, offset := range []int64{-1, 257, 1 << 40} {
		bad := legacy
		bad.Offset = offset
		if _, _, err := restoreNative(bad); err == nil {
			t.Fatalf("accepted graphics offset%d", offset)
		}
	}
	for _, alpha := range []int32{0, 256} {
		vm := policyRegressionVM(t, NativePolicyLGT)
		ref := vm.ScreenGraphics()
		lgtAlpha(t, vm, ref, alpha)
		saved, err := vm.MarshalBinary()
		check(t, err)
		check(t, vm.UnmarshalBinary(saved))
		after, err := vm.MarshalBinary()
		check(t, err)
		if !bytes.Equal(saved, after) {
			t.Fatalf("alpha%d changed snapshot", alpha)
		}
	}
}

func TestLGTGraphicsAlphaLegacySnapshotMigration(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	ref, state := midpGraphicsSurface(t, vm)
	// Reproduce the pre-extension host table and zero/unused graphics Offset.
	key := fieldStorageKey(lgtGraphicsClass, "DEFAULT_ALPHA", "I")
	delete(vm.hostStatic, key)
	saved, err := vm.MarshalBinary()
	check(t, err)
	vm.RegisterStaticField(lgtGraphicsClass, "DEFAULT_ALPHA", "I", IntValue(256))
	check(t, vm.UnmarshalBinary(saved))
	lgtFillWhite(t, vm, ref, 0)
	if got := midpPixel(t, vm, state.surface, 0, 0); got != (shared.Color{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("legacy snapshot default = %+v", got)
	}
}

func TestLGTGraphicsAlphaGameCanvasPaintAndFlush(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	game := vm.NewObject("javax/microedition/lcdui/game/GameCanvas", nil)
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "<init>", "(Z)V", game, IntValue(0))
	v := invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "getGraphics", "()Ljavax/microedition/lcdui/Graphics;", game)
	ref, err := v.Reference()
	check(t, err)
	state, err := vm.graphics(ref)
	check(t, err)
	check(t, vm.services.Graphics.Clear(vm.serviceOwner, state.surface, shared.Color{A: 255}))
	lgtFillWhite(t, vm, ref, 0) // backing context default is opaque
	lgtAlpha(t, vm, ref, 128)
	lgtFillWhite(t, vm, ref, 1)
	screen, screenState := midpGraphicsSurface(t, vm)
	lgtAlpha(t, vm, screen, 0)
	// flushGraphics presents only the currently shown canvas.
	invokeTestNative(t, vm, "javax/microedition/lcdui/Display", "setCurrent", "(Ljavax/microedition/lcdui/Displayable;)V", 0, ReferenceValue(game))
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "flushGraphics", "()V", game)
	if got := midpPixel(t, vm, screenState.surface, 0, 0); got != (shared.Color{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("flush inherited screen alpha: %+v", got)
	}
	if got := midpPixel(t, vm, screenState.surface, 1, 0); got != (shared.Color{R: 128, G: 128, B: 128, A: 255}) {
		t.Fatalf("flush applied backing alpha twice: %+v", got)
	}
	check(t, vm.services.Graphics.Clear(vm.serviceOwner, screenState.surface, shared.Color{A: 255}))
	lgtAlpha(t, vm, screen, 128)
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "paint", "(Ljavax/microedition/lcdui/Graphics;)V", game, ReferenceValue(screen))
	if got := midpPixel(t, vm, screenState.surface, 0, 0); got != (shared.Color{R: 128, G: 128, B: 128, A: 255}) {
		t.Fatalf("paint bypassed destination alpha: %+v", got)
	}
}

func TestLGTGraphicsAlphaOutlineCoverageOnce(t *testing.T) {
	for _, name := range []string{"drawRect", "drawRoundRect"} {
		for _, size := range [][2]int32{{10, 10}, {1, 1}, {1, 10}, {10, 1}, {0, 0}, {0, 10}, {10, 0}} {
			vm := policyRegressionVM(t, NativePolicyLGT)
			ref, state := midpGraphicsSurface(t, vm)
			check(t, vm.services.Graphics.Clear(vm.serviceOwner, state.surface, shared.Color{A: 255}))
			lgtAlpha(t, vm, ref, 128)
			invokeTestNative(t, vm, lgtBaseGraphics, "setColor", "(I)V", ref, IntValue(0xffffff))
			args := []Value{IntValue(0), IntValue(0), IntValue(size[0]), IntValue(size[1])}
			desc := "(IIII)V"
			if name == "drawRoundRect" {
				desc = "(IIIIII)V"
				args = append(args, IntValue(0), IntValue(0))
			}
			invokeTestNative(t, vm, lgtBaseGraphics, name, desc, ref, args...)
			want := uint8(128)
			if name == "drawRoundRect" && (size[0] == 0 || size[1] == 0) {
				want = 0
			}
			if got := midpPixel(t, vm, state.surface, 0, 0); got != (shared.Color{R: want, G: want, B: want, A: 255}) {
				t.Fatalf("%s%v corner = %+v, want%d", name, size, got, want)
			}
		}
	}
}

func TestLGTGraphicsAlphaGameLayerPaths(t *testing.T) {
	for _, kind := range []string{"Sprite", "TiledLayer"} {
		vm := policyRegressionVM(t, NativePolicyLGT)
		ref, state := midpGraphicsSurface(t, vm)
		check(t, vm.services.Graphics.Clear(vm.serviceOwner, state.surface, shared.Color{A: 255}))
		image, err := vm.newImageState(1, 1)
		check(t, err)
		check(t, vm.services.Graphics.Clear(vm.serviceOwner, image.surface, shared.Color{R: 255, G: 255, B: 255, A: 255}))
		source := vm.NewObject("javax/microedition/lcdui/Image", image)
		class := "javax/microedition/lcdui/game/" + kind
		layer := vm.NewObject(class, nil)
		if kind == "Sprite" {
			invokeTestNative(t, vm, class, "<init>", "(Ljavax/microedition/lcdui/Image;)V", layer, ReferenceValue(source))
		} else {
			invokeTestNative(t, vm, class, "<init>", "(IILjavax/microedition/lcdui/Image;II)V", layer, IntValue(1), IntValue(1), ReferenceValue(source), IntValue(1), IntValue(1))
			invokeTestNative(t, vm, class, "setCell", "(III)V", layer, IntValue(0), IntValue(0), IntValue(1))
		}
		lgtAlpha(t, vm, ref, 128)
		invokeTestNative(t, vm, class, "paint", "(Ljavax/microedition/lcdui/Graphics;)V", layer, ReferenceValue(ref))
		if got := midpPixel(t, vm, state.surface, 0, 0); got != (shared.Color{R: 128, G: 128, B: 128, A: 255}) {
			t.Fatalf("%s bypass alpha: %+v", kind, got)
		}
		managerClass := "javax/microedition/lcdui/game/LayerManager"
		manager := vm.NewObject(managerClass, nil)
		invokeTestNative(t, vm, managerClass, "<init>", "()V", manager)
		invokeTestNative(t, vm, managerClass, "append", "(Ljavax/microedition/lcdui/game/Layer;)V", manager, ReferenceValue(layer))
		invokeTestNative(t, vm, managerClass, "setViewWindow", "(IIII)V", manager, IntValue(0), IntValue(0), IntValue(1), IntValue(1))
		before, err := vm.services.Graphics.DrawState(vm.serviceOwner, state.surface)
		check(t, err)
		invokeTestNative(t, vm, managerClass, "paint", "(Ljavax/microedition/lcdui/Graphics;II)V", manager, ReferenceValue(ref), IntValue(2), IntValue(3))
		if got := midpPixel(t, vm, state.surface, 2, 3); got != (shared.Color{R: 128, G: 128, B: 128, A: 255}) {
			t.Fatalf("nested %s alpha: %+v", kind, got)
		}
		after, err := vm.services.Graphics.DrawState(vm.serviceOwner, state.surface)
		check(t, err)
		if before != after {
			t.Fatal("nested layer scope leaked surface state")
		}
	}
}
