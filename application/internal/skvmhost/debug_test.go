package skvmhost

import (
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	skengine "github.com/mirusu400/aram-core/skvm"
)

func TestSKVMDebugSnapshotReportsInterpreterProgress(t *testing.T) {
	machine := &Machine{
		state:     machinecore.StatePaused,
		mainClass: "example/Game",
		started:   true,
		midlet:    12,
		input:     make([]machinecore.InputEvent, 3),
		vm:        &skengine.VM{Instructions: 987},
	}
	snapshot := machine.DebugSnapshot(10)
	if snapshot.Runtime != "skvm" ||
		snapshot.State != "paused" ||
		snapshot.SKVM == nil ||
		snapshot.SKVM.MainClass != "example/Game" ||
		snapshot.SKVM.Instructions != 987 ||
		snapshot.SKVM.QueuedInput != 3 {
		t.Fatalf("SKVM snapshot = %+v", snapshot)
	}
}
