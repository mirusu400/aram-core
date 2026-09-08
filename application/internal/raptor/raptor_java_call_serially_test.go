package raptor

import (
	"context"
	"testing"

	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
)

// Display.callSerially hands a Runnable to the event loop; it does not run it
// inside the caller. Running it inline is fine for a title that arms it once,
// but the loop idiom - a run() that re-arms itself with callSerially before it
// returns - turns into unbounded recursion: 스파이더맨3's launch class re-arms
// on every pass and reached the host-call nesting limit before its first frame.
func TestRaptorCallSeriallyQueuesARunnableThatReArmsItself(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{
		CPU:             public.CPU,
		Public:          public,
		resolvedImports: make(map[raptorImportKey]uint64),
		importSlotByKey: make(map[raptorImportKey]uint32),
	}
	java, err := runtime.ensureJavaRuntime()
	check(t, err)

	const runBody = uint32(0x00002468)
	holder, err := public.Heap.Allocate(12, true)
	if err != nil || holder == 0 {
		t.Fatalf("allocate holder = 0x%08x, %v", holder, err)
	}
	class := &raptorJavaClass{
		Name:   "app/Loop",
		Holder: holder,
		methods: []raptorJavaDeclaredMethod{
			{Name: "run", descriptor: "()V", Body: runBody},
		},
	}
	java.classes[holder] = class
	java.ClassByName[class.Name] = class

	runnable, err := public.Heap.Allocate(12, true)
	if err != nil || runnable == 0 {
		t.Fatalf("allocate runnable = 0x%08x, %v", runnable, err)
	}
	check(t, public.WriteU32(runnable+4, holder))

	callSerially := raptorJavaMethod{
		className:  "org/kwis/msp/lcdui/Display",
		Name:       "callSerially",
		descriptor: "(Ljava/lang/Runnable;)V",
	}
	arm := func() error {
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, 0x10000000))
		check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, runnable))
		_, err := runtime.callJavaHostMethod(context.Background(), callSerially)
		return err
	}

	// The runnable re-arms itself, exactly as the guest's run() does.
	invocations := 0
	public.InvokeSync = func(
		_ context.Context,
		callback wipirt.GuestCallback,
	) (uint32, error) {
		invocations++
		if callback.Procedure != runBody || callback.Args[0] != runnable {
			t.Fatalf("ran %#v, want run() 0x%08x on 0x%08x",
				callback, runBody, runnable)
		}
		return 0, arm()
	}
	check(t, arm())

	if invocations != 1 {
		t.Fatalf("callSerially ran the runnable %d times, want 1", invocations)
	}
	if len(runtime.CallbackTasks) != 1 {
		t.Fatalf("queued %d callbacks, want the re-armed one",
			len(runtime.CallbackTasks))
	}
	queued := runtime.CallbackTasks[0].Callback
	if queued.Procedure != runBody || queued.Args[0] != runnable {
		t.Fatalf("queued %#v, want run() 0x%08x on 0x%08x",
			queued, runBody, runnable)
	}
}
