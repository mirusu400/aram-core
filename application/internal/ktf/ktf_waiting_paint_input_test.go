package ktf

import "testing"

// A paint callback may wait for its key handler to notify it. Treating that
// suspended callback as a busy renderer prevents the notification from arriving.
func TestKTFKeyEventReachesCardWhilePaintWaits(t *testing.T) {
	for _, test := range []struct {
		name    string
		timeout int64
	}{
		{name: "indefinite"},
		{name: "timed", timeout: 50},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := newTestRuntime(t)
			runtime.JvmContext = allocWords(t, runtime, 3+128)
			runtime.DeferThreads = true
			runtime.TickMS = 100
			class := inspectClass(t, runtime, ensureClass(t, runtime, "Clet$CletCard"))
			_, err := runtime.addHostJavaMethod(class, "keyNotify", "(II)Z")
			check(t, err)
			card, err := runtime.NewJavaInstanceForClass(class)
			check(t, err)
			const display = uint32(0x10004000)
			runtime.DefaultDisplay = display
			runtime.DisplayCards[display] = card
			paint := &Task{}
			runtime.Tasks = []*Task{paint}
			runtime.PaintTasks[card] = paint
			runtime.activeTask = paint
			check(t, runtime.waitJavaObject(card, test.timeout, 0))
			runtime.activeTask = nil

			if !runtime.CanQueueKeyEventFor(-5) {
				t.Fatal("waiting paint blocked the key that can wake it")
			}
			queued, err := runtime.QueueKeyEvent(true, -5)
			check(t, err)
			if !queued || len(runtime.Tasks) != 2 || runtime.Tasks[1].KeyCard != card {
				t.Fatal("key was not dispatched to the waiting card")
			}
			if runtime.PaintTasks[card] != paint || paint.Done {
				t.Fatal("dispatch replaced or canceled the paint continuation")
			}
			check(t, runtime.notifyJavaObject(card, false))
			if paint.monitorWait != 0 || paint.WakeAtMS != 0 {
				t.Fatal("notification did not wake the paint continuation")
			}
		})
	}
}

func TestKTFKeyEventStillWaitsForActivePaint(t *testing.T) {
	const card, display = uint32(2), uint32(1)
	for _, test := range []struct {
		name string
		task Task
	}{
		{name: "runnable"},
		{name: "sleeping", task: Task{WakeAtMS: 150}},
		{name: "expired_wait", task: Task{monitorWait: card, WakeAtMS: 100}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := &Runtime{
				TickMS: 100, DefaultDisplay: display,
				DisplayCards: map[uint32]uint32{display: card},
				PaintTasks:   map[uint32]*Task{card: &test.task},
			}
			if runtime.CanQueueKeyEventFor(-5) {
				t.Fatal("key bypassed an active paint callback")
			}
			queued, err := runtime.QueueKeyEvent(true, -5)
			check(t, err)
			if queued || len(runtime.Tasks) != 0 {
				t.Fatal("key task was created while paint owned the card")
			}
		})
	}
}
