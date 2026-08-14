package ktf

import "testing"

func TestKTFDrainServiceEventsConsumesUnknownInput(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	if err := runtime.Services.QueueInput(
		runtime.ServiceOwner,
		"unknown-control",
		true,
		0,
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.DrainServiceEvents(0); err != nil {
		t.Fatal(err)
	}
	if event, ok := runtime.Services.Events.Peek(); ok {
		t.Fatalf("unknown input remained queued: %+v", event)
	}
	if len(runtime.Tasks) != 0 {
		t.Fatalf("unknown input created %d KTF tasks", len(runtime.Tasks))
	}
	if len(runtime.HostTrace) != 1 {
		t.Fatalf("unknown input trace = %v", runtime.HostTrace)
	}
	if got := runtime.HostTrace[0]; got !=
		`java_input_drop:control="unknown-control":kind=input.press` {
		t.Fatalf("unknown input trace = %q", got)
	}
}
