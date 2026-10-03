package skvm

import (
	"context"

	shared "github.com/mirusu400/aram-core/runtime"
)

// The XCE input indicator is handset UI, not an archive resource. Model each
// requested mode with a compact, readable image so a focused field can show its
// current mode even when the original handset artwork is unavailable.
func (vm *VM) installXCEToolkitImages() {
	for _, spec := range []struct {
		method string
		label  string
	}{
		{"ueimImg", "A"},
		{"leimImg", "a"},
		{"simImg", "*"},
		{"kimImg", "한"},
		{"nimImg", "1"},
		{"imHintImg", "?"},
	} {
		spec := spec
		const class = "com/xce/lcdui/Toolkit"
		const descriptor = "Ljavax/microedition/lcdui/Image;"
		field := "__aramImage_" + spec.method
		key := fieldStorageKey(class, field, descriptor)
		vm.RegisterStaticField(class, field, descriptor, ReferenceValue(0))
		vm.RegisterNative(class, spec.method, "()"+descriptor,
			func(_ context.Context, vm *VM, _ uint32, _ []Value) (Value, bool, error) {
				if reference, _ := vm.hostStatic[key].Reference(); reference != 0 {
					return ReferenceValue(reference), true, nil
				}
				reference, err := vm.newXCEInputIcon(spec.label)
				if err != nil {
					return Value{}, false, err
				}
				vm.hostStatic[key] = ReferenceValue(reference)
				return ReferenceValue(reference), true, nil
			},
		)
	}
}

func (vm *VM) newXCEInputIcon(label string) (uint32, error) {
	const size = 15
	image, err := vm.newImageState(size, size)
	if err != nil {
		return 0, err
	}
	keepSurface := false
	defer func() {
		if !keepSurface {
			_ = vm.services.Graphics.DestroySurface(vm.serviceOwner, image.surface)
		}
	}()
	graphics := &graphicsState{width: size, height: size, surface: image.surface}
	if err := fillRectangle(vm, graphics, 0, 0, size, size, 0xffffffff); err != nil {
		return 0, err
	}
	for _, edge := range [][4]int{
		{0, 0, size, 1}, {0, size - 1, size, 1},
		{0, 0, 1, size}, {size - 1, 0, 1, size},
	} {
		if err := fillRectangle(vm, graphics, edge[0], edge[1], edge[2], edge[3], 0xff333333); err != nil {
			return 0, err
		}
	}
	width, err := vm.services.Text.Measure(vm.serviceOwner, vm.defaultFont, label)
	if err != nil {
		return 0, err
	}
	metrics, err := vm.services.Text.Metrics(vm.serviceOwner, vm.defaultFont)
	if err != nil {
		return 0, err
	}
	if err := vm.services.Text.Draw(
		vm.serviceOwner, vm.defaultFont, image.surface, label,
		int32((size-int(width))/2), int32((size-int(metrics.Height))/2),
		shared.AnchorLeft|shared.AnchorTop, shared.RGB(0, 0, 0),
	); err != nil {
		return 0, err
	}
	reference := vm.newImmutableImageObject(image)
	keepSurface = true
	return reference, nil
}
