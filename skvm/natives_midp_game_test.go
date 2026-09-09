package skvm

import (
	"testing"

	shared "github.com/mirusu400/aram-core/runtime"
)

func newOpaqueMIDPImage(t *testing.T, vm *VM, width, height int) uint32 {
	t.Helper()
	state, err := vm.newImageState(width, height)
	check(t, err)
	for y := range height {
		for x := range width {
			check(t, vm.services.Graphics.SetPixel(vm.serviceOwner, state.surface, int32(x), int32(y), shared.Color{R: 255, G: 255, B: 255, A: 255}))
		}
	}
	return vm.NewObject("javax/microedition/lcdui/Image", state)
}

func TestMIDPSpriteFramesTransformAndCollision(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	image := newOpaqueMIDPImage(t, vm, 4, 2)
	first := vm.NewObject("javax/microedition/lcdui/game/Sprite", nil)
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "<init>", "(Ljavax/microedition/lcdui/Image;II)V", first, ReferenceValue(image), IntValue(2), IntValue(2))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "getRawFrameCount", "()I", first)); got != 2 {
		t.Fatalf("raw frame count = %d", got)
	}
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "nextFrame", "()V", first)
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "getFrame", "()I", first)); got != 1 {
		t.Fatalf("frame = %d", got)
	}
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "defineReferencePixel", "(II)V", first, IntValue(1), IntValue(1))
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "setRefPixelPosition", "(II)V", first, IntValue(10), IntValue(20))
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "setTransform", "(I)V", first, IntValue(transRot90))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "getRefPixelX", "()I", first)); got != 10 {
		t.Fatalf("reference X after transform = %d", got)
	}
	second := vm.NewObject("javax/microedition/lcdui/game/Sprite", nil)
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "<init>", "(Ljavax/microedition/lcdui/Image;)V", second, ReferenceValue(image))
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/Layer", "setPosition", "(II)V", second, IntValue(9), IntValue(19))
	collision := invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "collidesWith", "(Ljavax/microedition/lcdui/game/Sprite;Z)Z", first, ReferenceValue(second), IntValue(1))
	if mustInt(t, collision) != 1 {
		t.Fatal("expected pixel-level sprite collision")
	}
}

func TestMIDPTiledLayerCellsAndAnimatedTiles(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	image := newOpaqueMIDPImage(t, vm, 4, 2)
	layer := vm.NewObject("javax/microedition/lcdui/game/TiledLayer", nil)
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/TiledLayer", "<init>", "(IILjavax/microedition/lcdui/Image;II)V", layer, IntValue(3), IntValue(2), ReferenceValue(image), IntValue(2), IntValue(2))
	animated := invokeTestNative(t, vm, "javax/microedition/lcdui/game/TiledLayer", "createAnimatedTile", "(I)I", layer, IntValue(2))
	animatedIndex := mustInt(t, animated)
	if animatedIndex != -1 {
		t.Fatalf("animated tile index = %d", animatedIndex)
	}
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/TiledLayer", "setCell", "(III)V", layer, IntValue(1), IntValue(1), IntValue(animatedIndex))
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/game/TiledLayer", "getCell", "(II)I", layer, IntValue(1), IntValue(1))); got != -1 {
		t.Fatalf("cell = %d", got)
	}
	if got := mustInt(t, invokeTestNative(t, vm, "javax/microedition/lcdui/game/TiledLayer", "getAnimatedTile", "(I)I", layer, IntValue(-1))); got != 2 {
		t.Fatalf("animated tile target = %d", got)
	}
}

func TestMIDPLayerManagerMovesExistingLayer(t *testing.T) {
	vm, err := New(map[string][]byte{})
	check(t, err)
	image := newOpaqueMIDPImage(t, vm, 1, 1)
	first := vm.NewObject("javax/microedition/lcdui/game/Sprite", nil)
	second := vm.NewObject("javax/microedition/lcdui/game/Sprite", nil)
	for _, sprite := range []uint32{first, second} {
		invokeTestNative(t, vm, "javax/microedition/lcdui/game/Sprite", "<init>", "(Ljavax/microedition/lcdui/Image;)V", sprite, ReferenceValue(image))
	}
	manager := vm.NewObject("javax/microedition/lcdui/game/LayerManager", nil)
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/LayerManager", "<init>", "()V", manager)
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/LayerManager", "append", "(Ljavax/microedition/lcdui/game/Layer;)V", manager, ReferenceValue(first))
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/LayerManager", "append", "(Ljavax/microedition/lcdui/game/Layer;)V", manager, ReferenceValue(second))
	invokeTestNative(t, vm, "javax/microedition/lcdui/game/LayerManager", "insert", "(Ljavax/microedition/lcdui/game/Layer;I)V", manager, ReferenceValue(second), IntValue(0))
	value := invokeTestNative(t, vm, "javax/microedition/lcdui/game/LayerManager", "getLayerAt", "(I)Ljavax/microedition/lcdui/game/Layer;", manager, IntValue(0))
	reference, err := value.Reference()
	check(t, err)
	if reference != second {
		t.Fatalf("front layer = %d, want %d", reference, second)
	}
}
