package ktf

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Clet$CletCard is the Java-facing veneer of KTF's native Clet input path.
// It deliberately uses the native press/release values rather than the normal
// KWIS Card values its method signature otherwise resembles.
func TestKTFCletCardUsesNativeInputEdgeValues(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)

	class := inspectClass(t, runtime, ensureClass(t, runtime, "Clet$CletCard"))
	_, err := runtime.addHostJavaMethod(class, "keyNotify", "(II)Z")
	check(t, err)
	card, err := runtime.NewJavaInstanceForClass(class)
	check(t, err)

	const display = uint32(0x10004000)
	runtime.DefaultDisplay = display
	runtime.DisplayCards[display] = card
	queued, err := runtime.QueueKeyEvent(true, -5)
	check(t, err)
	if !queued || len(runtime.Tasks) != 1 {
		t.Fatalf("Clet press queue = %t, tasks=%d", queued, len(runtime.Tasks))
	}
	check(t, runtime.CPU.RestoreContext(runtime.Tasks[0].Context))
	pressed, err := runtime.CPU.ReadRegister(cpu.RegisterR2)
	check(t, err)
	if pressed != KeyReleased {
		t.Fatalf("Clet press event = %d, want native press value %d", pressed, KeyReleased)
	}
	runtime.Tasks[0].Done = true
	queued, err = runtime.QueueKeyEvent(false, -5)
	check(t, err)
	if !queued {
		t.Fatal("Clet release did not queue")
	}
	check(t, runtime.CPU.RestoreContext(runtime.Tasks[0].Context))
	released, err := runtime.CPU.ReadRegister(cpu.RegisterR2)
	check(t, err)
	if released != KeyPressed {
		t.Fatalf("Clet release event = %d, want native release value %d", released, KeyPressed)
	}

	ordinary := newHostObject(t, runtime, "org/kwis/msp/lcdui/Card")
	pressed, err = runtime.keyEventType(ordinary, true)
	check(t, err)
	if pressed != KeyPressed {
		t.Fatalf("ordinary Card press event = %d, want %d", pressed, KeyPressed)
	}
	released, err = runtime.keyEventType(ordinary, false)
	check(t, err)
	if released != KeyReleased {
		t.Fatalf("ordinary Card release event = %d, want %d", released, KeyReleased)
	}
}
