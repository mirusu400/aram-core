package skvm

import (
	"context"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestLGTGraphicsCapture(t *testing.T) {
	for _, target := range []string{"screen", "image", "game"} {
		t.Run(target, func(t *testing.T) {
			vm := policyRegressionVM(t, NativePolicyLGT)
			ref := vm.ScreenGraphics()
			if target == "image" {
				v := invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "createImage", "(II)Ljavax/microedition/lcdui/Image;", 0, IntValue(8), IntValue(8))
				r, err := v.Reference()
				check(t, err)
				v = invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "getGraphics", "()Ljavax/microedition/lcdui/Graphics;", r)
				ref, err = v.Reference()
				check(t, err)
			} else if target == "game" {
				r := vm.NewObject("javax/microedition/lcdui/game/GameCanvas", nil)
				invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "<init>", "(Z)V", r, IntValue(0))
				v := invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "getGraphics", "()Ljavax/microedition/lcdui/Graphics;", r)
				var err error
				ref, err = v.Reference()
				check(t, err)
			}
			state, err := vm.graphics(ref)
			check(t, err)
			for y := int32(0); y < 8; y++ {
				for x := int32(0); x < 8; x++ {
					check(t, vm.services.Graphics.SetPixel(vm.serviceOwner, state.surface, x, y, shared.RGB(uint8(x*20), uint8(y*20), 70)))
				}
			}
			invokeTestNative(t, vm, lgtBaseGraphics, "translate", "(II)V", ref, IntValue(2), IntValue(1))
			invokeTestNative(t, vm, lgtBaseGraphics, "setClip", "(IIII)V", ref, IntValue(1), IntValue(2), IntValue(3), IntValue(2))
			lgtAlpha(t, vm, ref, 0)
			before, err := vm.services.Graphics.DrawState(vm.serviceOwner, state.surface)
			check(t, err)
			v := invokeTestNative(t, vm, lgtGraphicsClass, "capture", "(IIII)Ljavax/microedition/lcdui/Image;", ref, IntValue(1), IntValue(2), IntValue(3), IntValue(2))
			imageRef, err := v.Reference()
			check(t, err)
			captured, err := vm.image(imageRef)
			check(t, err)
			if captured.width != 3 || captured.height != 2 || captured.surface == state.surface {
				t.Fatalf("invalid captured image: %+v", captured)
			}
			check(t, vm.services.Graphics.Clear(vm.serviceOwner, state.surface, shared.RGB(0, 0, 0)))
			for y := int32(0); y < 2; y++ {
				for x := int32(0); x < 3; x++ {
					want := shared.RGB(uint8((x+3)*20), uint8((y+3)*20), 70)
					if got := midpPixel(t, vm, captured.surface, x, y); got != want {
						t.Errorf("pixel (%d,%d) = %+v, want %+v", x, y, got, want)
					}
				}
			}
			after, err := vm.services.Graphics.DrawState(vm.serviceOwner, state.surface)
			check(t, err)
			if before != after || state.transparency256 != 256 {
				t.Fatal("capture changed source drawing state")
			}
			for _, region := range [][4]int32{{1, 2, 0, 1}, {1, 2, 1, -1}, {0, 2, 1, 1}, {1, 1, 1, 1}, {1, 2, 4, 1}, {1, 2, 1, 3}, {2147483647, 2, 1, 1}, {1, 2, 2147483647, 1}, {-2147483648, 2, 1, 1}} {
				native := vm.natives[nativeKey{class: lgtGraphicsClass, name: "capture", descriptor: "(IIII)Ljavax/microedition/lcdui/Image;"}]
				_, _, err := native(context.Background(), vm, ref, []Value{IntValue(region[0]), IntValue(region[1]), IntValue(region[2]), IntValue(region[3])})
				thrown, ok := err.(*thrown)
				if !ok || thrown.class != "java/lang/IllegalArgumentException" {
					t.Errorf("region %v error = %v", region, err)
				}
			}
		})
	}
	for _, policy := range []NativePolicy{NativePolicyJ2ME, NativePolicySKT} {
		if policyRegressionVM(t, policy).SupportsNativeReference(lgtGraphicsClass, "capture", "(IIII)Ljavax/microedition/lcdui/Image;") {
			t.Fatal("LGT capture leaked into another policy")
		}
	}
}
