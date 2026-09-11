package skvm

import (
	"bytes"
	"testing"
)

func TestLGTGraphicsAllocationContract(t *testing.T) {
	const graphicsX = "mmpp/microedition/lcdui/GraphicsX"
	const graphics = "javax/microedition/lcdui/Graphics"
	for _, policy := range []NativePolicy{NativePolicyLGT, NativePolicyJ2ME, NativePolicySKT} {
		t.Run(map[NativePolicy]string{NativePolicyLGT: "LGT", NativePolicyJ2ME: "J2ME", NativePolicySKT: "SKT"}[policy], func(t *testing.T) {
			vm := policyRegressionVM(t, policy)
			image := invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "createImage", "(II)Ljavax/microedition/lcdui/Image;", 0, IntValue(8), IntValue(8))
			imageRef, err := image.Reference()
			check(t, err)
			imageGraphics := invokeTestNative(t, vm, "javax/microedition/lcdui/Image", "getGraphics", "()Ljavax/microedition/lcdui/Graphics;", imageRef)
			imageGraphicsRef, err := imageGraphics.Reference()
			check(t, err)
			game := vm.NewObject("javax/microedition/lcdui/game/GameCanvas", nil)
			invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "<init>", "(Z)V", game, IntValue(0))
			gameGraphics := invokeTestNative(t, vm, "javax/microedition/lcdui/game/GameCanvas", "getGraphics", "()Ljavax/microedition/lcdui/Graphics;", game)
			gameGraphicsRef, err := gameGraphics.Reference()
			check(t, err)
			refs := map[string]uint32{"screen": vm.ScreenGraphics(), "image": imageGraphicsRef, "game": gameGraphicsRef}
			verify := func() {
				t.Helper()
				for name, ref := range refs {
					object, ok := vm.Object(ref)
					want := graphics
					if policy == NativePolicyLGT {
						want = graphicsX
					}
					if !ok || object.Class != want {
						t.Errorf("%s graphics class = %v, want %s", name, object, want)
						continue
					}
					if !vm.IsInstance(ref, graphics) || !vm.IsInstance(ref, "java/lang/Object") || vm.IsInstance(ref, graphicsX) != (policy == NativePolicyLGT) {
						t.Errorf("%s graphics inheritance does not match policy", name)
					}
					if vm.IsInstance(ref, "java/lang/String") || vm.IsInstance(ref, "[I") {
						t.Errorf("%s graphics accepted unrelated cast", name)
					}
					if !vm.SupportsNativeReference(object.Class, "setColor", "(I)V") {
						t.Errorf("%s inherited native method does not resolve", name)
					}
					invokeTestNative(t, vm, graphics, "setColor", "(I)V", ref, IntValue(0x123456))
					value := invokeTestNative(t, vm, graphics, "getColor", "()I", ref)
					color, err := value.Int()
					check(t, err)
					if color != 0x123456 {
						t.Errorf("%s inherited graphics state = %x", name, color)
					}
				}
			}
			verify()
			if _, exists := vm.hostSupers[graphicsX]; exists != (policy == NativePolicyLGT) {
				t.Error("GraphicsX host registration leaked across policies or is missing")
			}
			if policy == NativePolicyLGT && vm.classAssignable(graphics, graphicsX) {
				t.Error("base Graphics must not be assignable to its subclass")
			}
			if vm.SupportsNativeReference(graphicsX, "setAlpha", "(I)V") != (policy == NativePolicyLGT) {
				t.Error("alpha extension availability does not match policy")
			}
			state, err := vm.MarshalBinary()
			check(t, err)
			check(t, vm.UnmarshalBinary(state))
			verify()
			replayed, err := vm.MarshalBinary()
			check(t, err)
			if !bytes.Equal(state, replayed) {
				t.Error("graphics identity/native state changed after restore and equivalent replay")
			}
		})
	}
}
