package raptor

import "testing"

// TestNextRunnableJavaTaskRotates pins the thread scheduler. It used to take
// the first task that was not done, so a title whose main loop never returns
// starved every other thread it started: 현영맞고2006 starts Hcvs.run() and
// then TimeChecker.run(), and the clock thread never got an instruction
// (issue #79).
func TestNextRunnableJavaTaskRotates(t *testing.T) {
	first := &JavaTask{Procedure: 0x1001}
	second := &JavaTask{Procedure: 0x2001}
	third := &JavaTask{Procedure: 0x3001}
	runtime := &Runtime{Java: &JavaRuntime{Tasks: []*JavaTask{first, second, third}}}

	for round := 0; round < 2; round++ {
		for index, want := range []*JavaTask{first, second, third} {
			if got := runtime.NextRunnableJavaTask(); got != want {
				t.Fatalf(
					"round %d slice %d ran procedure 0x%04x, want 0x%04x",
					round, index, got.Procedure, want.Procedure,
				)
			}
		}
	}

	// A finished thread is skipped without disturbing the rotation.
	second.Done = true
	if got := runtime.NextRunnableJavaTask(); got != first {
		t.Fatalf("after the second finished, ran 0x%04x, want 0x%04x", got.Procedure, first.Procedure)
	}
	if got := runtime.NextRunnableJavaTask(); got != third {
		t.Fatalf("finished thread was not skipped: ran 0x%04x", got.Procedure)
	}

	first.Done = true
	third.Done = true
	if got := runtime.NextRunnableJavaTask(); got != nil {
		t.Fatalf("all threads finished but 0x%04x was scheduled", got.Procedure)
	}
}

// TestNextRunnableJavaTaskWithoutThreads keeps the empty cases quiet.
func TestNextRunnableJavaTaskWithoutThreads(t *testing.T) {
	if (&Runtime{}).NextRunnableJavaTask() != nil {
		t.Fatal("a runtime with no Java host scheduled a task")
	}
	if (&Runtime{Java: &JavaRuntime{}}).NextRunnableJavaTask() != nil {
		t.Fatal("a Java runtime with no threads scheduled a task")
	}
}
