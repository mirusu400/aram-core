package skvm

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

const lgtBaseGraphics = "javax/microedition/lcdui/Graphics"

func lgtAlpha(t *testing.T, vm *VM, ref uint32, alpha int32) {
	t.Helper()
	invokeTestNative(t, vm, lgtGraphicsClass, "setAlpha", "(I)V", ref, IntValue(alpha))
}

// Expected pixels below are literal independently calculated RGB values, not
// values obtained by running the compositor or an opaque reference rendering.
func TestLGTGraphicsAlphaAllInheritedDrawing(t *testing.T) {
	for _, alpha := range []int32{0, 1, 64, 128, 255, 256} {
		for _, name := range []string{"fillRect", "drawRect", "drawLine", "drawArc", "fillArc", "drawRoundRect", "fillRoundRect", "fillTriangle", "drawImage", "drawRegion", "drawRGB", "copyArea", "drawString", "drawSubstring", "drawChars", "drawChar"} {
			t.Run(fmt.Sprintf("%s/%d", name, alpha), func(t *testing.T) {
				vm := policyRegressionVM(t, NativePolicyLGT)
				ref, state := midpGraphicsSurface(t, vm)
				check(t, vm.services.Graphics.Clear(vm.serviceOwner, state.surface, shared.Color{R: 20, G: 40, B: 60, A: 255}))
				invokeTestNative(t, vm, lgtBaseGraphics, "setColor", "(I)V", ref, IntValue(0xdc8c64))
				image, err := vm.newImageState(1, 1)
				check(t, err)
				check(t, vm.services.Graphics.Clear(vm.serviceOwner, image.surface, shared.Color{R: 220, G: 140, B: 100, A: 255}))
				imageRef := vm.NewObject("javax/microedition/lcdui/Image", image)
				text := vm.NewString("A")
				ints := func(values ...int32) []Value {
					r := make([]Value, len(values))
					for i, v := range values {
						r[i] = IntValue(v)
					}
					return r
				}
				desc := "(IIIIII)V"
				args := ints(0, 0, 10, 10, 0, 360)
				x, y := int32(5), int32(5)
				switch name {
				case "fillRect":
					desc = "(IIII)V"
					args = ints(0, 0, 10, 10)
				case "drawRect":
					desc = "(IIII)V"
					args = ints(0, 0, 10, 10)
					x, y = 5, 0
				case "drawLine":
					desc = "(IIII)V"
					args = ints(0, 0, 10, 0)
					x, y = 5, 0
				case "drawArc":
					x, y = 0, 5
				case "drawRoundRect":
					args = ints(0, 0, 10, 10, 4, 4)
					x, y = 5, 0
				case "fillRoundRect":
					args = ints(0, 0, 10, 10, 4, 4)
				case "fillTriangle":
					args = ints(0, 0, 10, 0, 0, 10)
					x, y = 1, 1
				case "drawImage":
					desc = "(Ljavax/microedition/lcdui/Image;III)V"
					args = append([]Value{ReferenceValue(imageRef)}, ints(5, 5, 0)...)
				case "drawRegion":
					desc = "(Ljavax/microedition/lcdui/Image;IIIIIIII)V"
					args = append([]Value{ReferenceValue(imageRef)}, ints(0, 0, 1, 1, 5, 5, 5, 0)...)
				case "drawRGB":
					desc = "([IIIIIIIZ)V"
					pixels := vm.newArray("[I", []Value{IntValue(-2323356)})
					args = append([]Value{ReferenceValue(pixels)}, ints(0, 1, 5, 5, 1, 1, 0)...)
				case "copyArea":
					desc = "(IIIIIII)V"
					check(t, vm.services.Graphics.SetPixel(vm.serviceOwner, state.surface, 0, 0, shared.Color{R: 220, G: 140, B: 100, A: 255}))
					args = ints(0, 0, 1, 1, 5, 5, 0)
				case "drawString":
					desc = "(Ljava/lang/String;III)V"
					args = append([]Value{ReferenceValue(text)}, ints(5, 5, 0)...)
				case "drawSubstring":
					desc = "(Ljava/lang/String;IIIII)V"
					args = append([]Value{ReferenceValue(text)}, ints(0, 1, 5, 5, 0)...)
				case "drawChars":
					desc = "([CIIIII)V"
					chars := vm.newArray("[C", ints('A'))
					args = append([]Value{ReferenceValue(chars)}, ints(0, 1, 5, 5, 0)...)
				case "drawChar":
					desc = "(CIII)V"
					args = ints('A', 5, 5, 0)
				}
				if name == "drawString" || name == "drawSubstring" || name == "drawChars" || name == "drawChar" {
					glyph, err := vm.services.Text.Glyph(vm.serviceOwner, vm.defaultFont, 'A')
					check(t, err)
					found := false
					for i, coverage := range glyph.Alpha {
						if coverage == 255 {
							x = 5 + glyph.BearingX + int32(i)%glyph.Width
							y = 5 + glyph.BearingY + int32(i)/glyph.Width
							found = true
							break
						}
					}
					if !found {
						t.Fatal("fallback A lacks opaque coverage")
					}
				}
				lgtAlpha(t, vm, ref, alpha)
				invokeTestNative(t, vm, lgtBaseGraphics, name, desc, ref, args...)
				want := map[int32]shared.Color{0: {R: 20, G: 40, B: 60, A: 255}, 1: {R: 21, G: 40, B: 60, A: 255}, 64: {R: 70, G: 65, B: 70, A: 255}, 128: {R: 120, G: 90, B: 80, A: 255}, 255: {R: 219, G: 140, B: 100, A: 255}, 256: {R: 220, G: 140, B: 100, A: 255}}[alpha]
				if got := midpPixel(t, vm, state.surface, x, y); got != want {
					t.Fatalf("pixel = %+v, want %+v", got, want)
				}
				if got := midpPixel(t, vm, state.surface, 20, 20); got != (shared.Color{R: 20, G: 40, B: 60, A: 255}) {
					t.Fatalf("outside changed: %+v", got)
				}
			})
		}
	}
}

func TestLGTGraphicsAlphaBoundsStateAndReset(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	ref, state := midpGraphicsSurface(t, vm)
	lgtAlpha(t, vm, ref, 128)
	for _, a := range []int32{-2147483648, -1, 257, 2147483647} {
		native := vm.natives[nativeKey{class: lgtGraphicsClass, name: "setAlpha", descriptor: "(I)V"}]
		_, _, err := native(context.Background(), vm, ref, []Value{IntValue(a)})
		thrown, ok := err.(*thrown)
		if !ok || thrown.class != "java/lang/IllegalArgumentException" {
			t.Fatalf("alpha %d error = %v", a, err)
		}
	}
	draw := func() {
		invokeTestNative(t, vm, lgtBaseGraphics, "setColor", "(I)V", ref, IntValue(0xffffff))
		invokeTestNative(t, vm, lgtBaseGraphics, "fillRect", "(IIII)V", ref, IntValue(0), IntValue(0), IntValue(1), IntValue(1))
	}
	check(t, vm.services.Graphics.Clear(vm.serviceOwner, state.surface, shared.Color{A: 255}))
	saved, err := vm.MarshalBinary()
	check(t, err)
	draw()
	if got := midpPixel(t, vm, state.surface, 0, 0); got != (shared.Color{R: 128, G: 128, B: 128, A: 255}) {
		t.Fatalf("invalid bound changed alpha: %+v", got)
	}
	after, err := vm.MarshalBinary()
	check(t, err)
	check(t, vm.UnmarshalBinary(saved))
	draw()
	replayed, err := vm.MarshalBinary()
	check(t, err)
	if !bytes.Equal(after, replayed) {
		t.Fatal("save/replay differs")
	}
	check(t, vm.resetScreenGraphics())
	draw()
	if got := midpPixel(t, vm, state.surface, 0, 0); got != (shared.Color{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("paint reset alpha = %+v", got)
	}
}
