package skvm

import (
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func TestLGTGraphicsXTriangleAlphaEntireFootprint(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyLGT)
	ref, state := midpGraphicsSurface(t, vm)
	check(t, vm.services.Graphics.Clear(vm.serviceOwner, state.surface, shared.RGB(0, 0, 0)))
	invokeTestNative(t, vm, lgtBaseGraphics, "setColor", "(I)V", ref, IntValue(0xffffff))
	lgtAlpha(t, vm, ref, 128)
	invokeTestNative(t, vm, lgtBaseGraphics, "fillTriangle", "(IIIIII)V", ref,
		IntValue(1), IntValue(1), IntValue(5), IntValue(1), IntValue(1), IntValue(5))
	// Independently specified full inclusive footprint, including the bottom
	// vertex omitted by the fill scanlines and restored by the outline.
	rows := []string{"........", ".#####..", ".####...", ".###....", ".##.....", ".#......", "........", "........"}
	for y, row := range rows {
		for x, c := range row {
			want := shared.RGB(0, 0, 0)
			if c == '#' {
				want = shared.RGB(128, 128, 128)
			}
			if got := midpPixel(t, vm, state.surface, int32(x), int32(y)); got != want {
				t.Errorf("(%d,%d) = %+v, want %+v", x, y, got, want)
			}
		}
	}
}
