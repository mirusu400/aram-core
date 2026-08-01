package application

import "testing"

func TestKTFDrainServiceEventsConsumesUnknownInput(t *testing.T) {
	runtime := newScratchKTFRuntime(t)
	if err := runtime.services.QueueInput(
		runtime.serviceOwner,
		"unknown-control",
		true,
		0,
	); err != nil {
		t.Fatal(err)
	}
	if err := runtime.drainServiceEvents(0); err != nil {
		t.Fatal(err)
	}
	if event, ok := runtime.services.Events.Peek(); ok {
		t.Fatalf("unknown input remained queued: %+v", event)
	}
	if len(runtime.tasks) != 0 {
		t.Fatalf("unknown input created %d KTF tasks", len(runtime.tasks))
	}
	if len(runtime.hostTrace) != 1 {
		t.Fatalf("unknown input trace = %v", runtime.hostTrace)
	}
	if got := runtime.hostTrace[0]; got !=
		`java_input_drop:control="unknown-control":kind=input.press` {
		t.Fatalf("unknown input trace = %q", got)
	}
}
