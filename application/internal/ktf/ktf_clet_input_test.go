package ktf

import (
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
)

// Clet$CletCard is the Java facade every KTF Clet build compiles into its
// client. Its keyNotify(type, key) is ordinary KWIS Card glue: it turns
// EventQueue.KEY_PRESSED (1) into the native press event 2, KEY_RELEASED (2)
// into the native release event 3, and anything else - KEY_REPEATED - into 4.
// Handing it the two edges the other way round made a Clet see its key go up
// when the player pressed it and go down when the player let go, so a tank in
// SBS포트리스4 (issue #570) or a ship in 크로노스윙 (issue #547) sat still while
// the direction was held and then drove off on its own once it was released.
func TestKTFCletCardReceivesKWISKeyEdges(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	class := inspectClass(t, runtime, ensureClass(t, runtime, "Clet$CletCard"))
	_, err := runtime.addHostJavaMethod(class, "keyNotify", "(II)Z")
	check(t, err)
	card, err := runtime.NewJavaInstanceForClass(class)
	check(t, err)
	const display = uint32(0x10004000)
	runtime.DefaultDisplay = display
	runtime.DisplayCards[display] = card

	// deliver drains the shared input service and returns the keyNotify type
	// the card was called with, then retires that call.
	deliver := func(step string) uint32 {
		t.Helper()
		check(t, runtime.DrainServiceEvents(runtime.Services.Clock.Monotonic()))
		var task *Task
		for _, candidate := range runtime.Tasks {
			if !candidate.Done && candidate.KeyCard == card {
				task = candidate
			}
		}
		if task == nil {
			t.Fatalf("%s did not reach the Clet card", step)
		}
		check(t, runtime.CPU.RestoreContext(task.Context))
		edge, err := runtime.CPU.ReadRegister(cpu.RegisterR2)
		check(t, err)
		key, err := runtime.CPU.ReadRegister(cpu.RegisterR3)
		check(t, err)
		if int32(key) != -4 {
			t.Fatalf("%s key = %d, want -4", step, int32(key))
		}
		task.Done = true
		return edge
	}

	check(t, runtime.Services.QueueInput(runtime.ServiceOwner, "right", true, 0))
	if got := deliver("press"); got != KeyPressed {
		t.Fatalf("Clet press keyNotify type = %d, want KEY_PRESSED %d", got, KeyPressed)
	}
	// Hold the key past the auto-repeat delay.
	check(t, runtime.Services.Advance(runtime.ServiceOwner, time.Second))
	if got := deliver("auto-repeat"); got != KeyRepeated {
		t.Fatalf("Clet repeat keyNotify type = %d, want KEY_REPEATED %d", got, KeyRepeated)
	}
	check(t, runtime.Services.QueueInput(
		runtime.ServiceOwner, "right", false, runtime.Services.Clock.Monotonic(),
	))
	if got := deliver("release"); got != KeyReleased {
		t.Fatalf("Clet release keyNotify type = %d, want KEY_RELEASED %d", got, KeyReleased)
	}
}

// An ordinary KWIS Card keeps the values it always had, including a held key's
// auto-repeat arriving as another KEY_PRESSED.
func TestKTFOrdinaryCardKeyEdgesAreUnchanged(t *testing.T) {
	runtime := newTestRuntime(t)
	ordinary := newHostObject(t, runtime, "org/kwis/msp/lcdui/Card")
	for _, test := range []struct {
		edge, want uint32
	}{
		{KeyPressed, KeyPressed},
		{KeyReleased, KeyReleased},
		{KeyRepeated, KeyPressed},
	} {
		got, err := runtime.keyEventType(ordinary, test.edge)
		check(t, err)
		if got != test.want {
			t.Fatalf("ordinary Card edge %d keyNotify type = %d, want %d", test.edge, got, test.want)
		}
	}
}

// TestKTFInputKeepsTheReservedTaskSlot proves background Java work cannot use
// the one stack the handset needs to dispatch a key to its displayed card.
func TestKTFInputKeepsTheReservedTaskSlot(t *testing.T) {
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
	runtime.Tasks = make([]*Task, ktfBackgroundTaskLimit)
	for index := range runtime.Tasks {
		runtime.Tasks[index] = &Task{}
	}

	if !runtime.CanQueueKeyEvent() {
		t.Fatal("a displayed card lost key delivery to background tasks")
	}
	queued, err := runtime.QueueKeyEvent(true, -5)
	check(t, err)
	if !queued || len(runtime.Tasks) != MaxTasks {
		t.Fatalf("key queue = %t, tasks=%d", queued, len(runtime.Tasks))
	}
	if task := runtime.Tasks[MaxTasks-1]; task.KeyCard != card {
		t.Fatalf("reserved task card = 0x%08x, want 0x%08x", task.KeyCard, card)
	}
}
