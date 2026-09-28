package skvm

import (
	"context"
	"testing"
)

func TestPaintCurrentDefersWhileWorkerPaintIsParked(t *testing.T) {
	vm := policyRegressionVM(t, NativePolicyJ2ME)
	canvas := vm.NewObject("javax/microedition/lcdui/Canvas", nil)
	vm.currentDisplay = canvas
	vm.setRepaintPending(true)
	worker := &threadState{
		active: true,
		continuation: []*frame{{
			method: Method{Name: "paint", Descriptor: "(Ljavax/microedition/lcdui/Graphics;)V"},
			locals: []Value{ReferenceValue(canvas)},
		}},
	}
	vm.NewObject("java/lang/Thread", worker)

	if err := vm.PaintCurrent(context.Background()); err != nil {
		t.Fatalf("deferred paint: %v", err)
	}
	if !vm.RepaintPending() {
		t.Fatal("deferred paint consumed the pending repaint request")
	}
}
