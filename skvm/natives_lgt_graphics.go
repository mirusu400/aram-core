package skvm

const lgtGraphicsClass = "mmpp/microedition/lcdui/GraphicsX"

// The public LGT GraphicsX Javadoc specifies that all Graphics objects are
// GraphicsX instances. Only the object identity and documented inheritance are
// installed here. Extension methods remain explicitly unsupported.
func (vm *VM) installLGTGraphicsTypes() {
	vm.RegisterHostClass(lgtGraphicsClass, "javax/microedition/lcdui/Graphics")
}

func (vm *VM) newGraphicsObject(state *graphicsState) uint32 {
	class := "javax/microedition/lcdui/Graphics"
	if vm.nativePolicy == NativePolicyLGT {
		class = lgtGraphicsClass
	}
	return vm.NewObject(class, state)
}
