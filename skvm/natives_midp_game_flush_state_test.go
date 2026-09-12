package skvm

import (
	"bytes"
	"fmt"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

// Supplemental service-state boundary coverage, distinct from the public guest
// conformance matrix: these raster/opacity fields need not be guest MIDP APIs.
func TestGameCanvasFlushServiceState(t *testing.T) {
	for _, policy := range []NativePolicy{NativePolicyJ2ME, NativePolicySKT, NativePolicyLGT} {
		for _, rectangle := range []bool{false, true} {
			for _, poison := range []string{"raster", "alpha", "transparency", "all"} {
				t.Run(fmt.Sprintf("%d/rect_%t/%s", policy, rectangle, poison), func(t *testing.T) {
					vm := policyRegressionVM(t, policy)
					game := vm.NewObject("javax/microedition/lcdui/game/GameCanvas", nil)
					invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "<init>", "(Z)V", game, IntValue(0))
					invokeTestNative(t, vm, "javax/microedition/lcdui/Display", "setCurrent", "(Ljavax/microedition/lcdui/Displayable;)V", 0, ReferenceValue(game))
					v := invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "getGraphics", "()Ljavax/microedition/lcdui/Graphics;", game)
					ref, err := v.Reference()
					check(t, err)
					buffer, err := vm.graphics(ref)
					check(t, err)
					check(t, vm.Services().Graphics.Clear(vm.ServiceOwner(), buffer.surface, shared.Color{R: 55, G: 91, B: 145, A: 255}))
					check(t, vm.Services().Graphics.Clear(vm.ServiceOwner(), vm.ScreenSurface(), shared.Color{R: 18, G: 52, B: 86, A: 255}))
					before := append([]byte(nil), vm.FrameRGBA()...)
					state, err := vm.Services().Graphics.DrawState(vm.ServiceOwner(), vm.ScreenSurface())
					check(t, err)
					switch poison {
					case "raster":
						state.Raster = shared.RasterXOR
					case "alpha":
						state.GlobalAlpha = 37
						state.Transparency = true
					case "transparency":
						state.GlobalTransparency256 = 193
						state.Transparency = true
					case "all":
						state.Raster = shared.RasterXOR
						state.GlobalAlpha = 37
						state.Transparency = true
						state.GlobalTransparency256 = 193
					}
					check(t, vm.Services().Graphics.SetDrawState(vm.ServiceOwner(), vm.ScreenSurface(), state))
					snapshot, err := vm.MarshalBinary()
					check(t, err)
					flush := func() {
						if rectangle {
							invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "flushGraphics", "(IIII)V", game, IntValue(1), IntValue(1), IntValue(2), IntValue(2))
						} else {
							invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "flushGraphics", "()V", game)
						}
					}
					flush()
					want := append([]byte(nil), before...)
					for y := 0; y < buffer.height; y++ {
						for x := 0; x < buffer.width; x++ {
							if !rectangle || (x >= 1 && x < 3 && y >= 1 && y < 3) {
								i := (y*vm.ScreenWidth + x) * 4
								copy(want[i:i+4], []byte{55, 91, 145, 255})
							}
						}
					}
					if !bytes.Equal(vm.FrameRGBA(), want) {
						t.Fatal("flush inherited runtime raster/opacity state")
					}
					got, err := vm.Services().Graphics.DrawState(vm.ServiceOwner(), vm.ScreenSurface())
					check(t, err)
					if got != state {
						t.Fatalf("draw state not restored: got %+v want %+v", got, state)
					}
					after, err := vm.MarshalBinary()
					check(t, err)
					check(t, vm.UnmarshalBinary(snapshot))
					flush()
					replay, err := vm.MarshalBinary()
					check(t, err)
					if !bytes.Equal(after, replay) || !bytes.Equal(vm.FrameRGBA(), want) {
						t.Fatal("poisoned state flush replay differs")
					}
				})
			}
		}
	}
}
