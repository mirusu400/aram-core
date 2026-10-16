package ktf

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	shared "github.com/mirusu400/aram-core/runtime"
)

func TestKTFTimerWorkerSurvivesSaveRestore(t *testing.T) {
	r := newTestRuntime(t)
	first, err := r.NewTask(ImageBase|1, nil, 0)
	check(t, err)
	next, err := r.NewTask(ImageBase|1, nil, 1)
	check(t, err)
	r.Tasks = []*Task{first, next}
	r.TickMS = 100
	check(t, r.Services.Clock.Advance(100*time.Millisecond))
	first.timerTask, first.timerOwner, first.timerDeadlineMS = 11, 1, 80
	first.timerStarted, first.WakeAtMS = true, 150
	next.timerTask, next.timerOwner, next.timerDeadlineMS = 12, 1, 100
	next.timerPeriodMS, next.timerFixedRate = 50, true
	r.javaTimerTasks = map[uint32]*Task{11: first, 12: next}
	r.javaTimerTaskStates = map[uint32]uint8{11: ktfJavaTimerScheduled, 12: ktfJavaTimerScheduled, 13: ktfJavaTimerCancelled}
	var output bytes.Buffer
	check(t, WriteState(r, r.CPU, true, guest.NewStateWriter(&output)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(output.Bytes())}
	saved, err := ParseState(r, &decoder)
	check(t, err)
	started := false
	check(t, RestoreState(r, r.CPU, saved, &started))
	if r.HasRunnableTask() {
		t.Fatal("restore started another callback on the sleeping Timer's worker")
	}
	if r.javaTimerTasks[11] != r.Tasks[0] || r.javaTimerTasks[12] != r.Tasks[1] || r.javaTimerTaskStates[13] != ktfJavaTimerCancelled {
		t.Fatal("restore lost TimerTask identity or cancellation state")
	}
	if task := r.Tasks[1]; task.timerPeriodMS != 50 || !task.timerFixedRate || task.timerDeadlineMS != 100 {
		t.Fatal("restore lost the periodic callback's schedule")
	}
	r.TickMS = 150
	if r.nextRunnableTask() != r.Tasks[0] {
		t.Fatal("restore did not resume the existing callback first")
	}
	r.Tasks[0].Done = true
	if r.nextRunnableTask() != r.Tasks[1] {
		t.Fatal("restore did not release the next callback")
	}

	// Version 15 ends before the timer extension and must still be readable.
	var extension bytes.Buffer
	// Use the original bytes' snapshot, before mutating task completion.
	r.Tasks[0].Done = false
	check(t, r.writeJavaTimerState(guest.NewStateWriter(&extension)))
	legacy := bytes.Clone(output.Bytes()[:output.Len()-extension.Len()])
	binary.LittleEndian.PutUint32(legacy[4:8], ktfStateSchemaV15)
	decoder = guest.StateDecoder{Reader: bytes.NewReader(legacy)}
	_, err = ParseState(r, &decoder)
	check(t, err)
}

func TestKTFTimerStateRejectsInvalidOwnership(t *testing.T) {
	for _, state := range []ktfJavaTimersSnapshot{
		{Tasks: []ktfJavaTimerTaskSnapshot{{Owner: 1}}},
		{Tasks: []ktfJavaTimerTaskSnapshot{{Instance: 11}}, States: map[uint32]uint8{11: ktfJavaTimerScheduled}},
		{Tasks: []ktfJavaTimerTaskSnapshot{{Instance: 11, Owner: 1}}, States: map[uint32]uint8{11: ktfJavaTimerCancelled}},
		{Tasks: []ktfJavaTimerTaskSnapshot{{}}, States: map[uint32]uint8{11: 4}},
	} {
		encoded, err := shared.MarshalStateComponent(state)
		check(t, err)
		var output bytes.Buffer
		writer := guest.NewStateWriter(&output)
		writer.U32(uint32(len(encoded)))
		writer.Write(encoded)
		decoder := guest.StateDecoder{Reader: bytes.NewReader(output.Bytes())}
		if _, err := readJavaTimerState(&decoder, make([]ktfTaskSnapshot, 1)); err == nil {
			t.Fatalf("accepted invalid timer state %+v", state)
		}
	}
}

func TestKTFTimerStatusDoesNotRetainFinishedCallbacks(t *testing.T) {
	r := newTestRuntime(t)
	// The runtime's heap base is itself a conservative root.
	_, err := r.AllocateWords(2)
	check(t, err)
	expired, err := r.AllocateWords(2)
	check(t, err)
	live, err := r.AllocateWords(2)
	check(t, err)
	r.javaTimerTaskStates[expired] = ktfJavaTimerExecuted
	r.javaTimerTaskStates[live] = ktfJavaTimerScheduled
	r.Tasks = []*Task{{timerTask: live, timerOwner: 1}}
	r.collectJavaHeap()
	if _, retained := r.javaTimerTaskStates[expired]; retained {
		t.Fatal("completed timer status kept an unreachable callback alive")
	}
	if r.javaTimerTaskStates[live] != ktfJavaTimerScheduled {
		t.Fatal("collection lost the running callback's timer status")
	}
}
