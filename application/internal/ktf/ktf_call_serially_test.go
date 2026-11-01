package ktf

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
)

// 천공의 기사단 calls Display.callSerially from its paint and then sleeps
// inside that paint. A handset runs the Runnable on its event thread after
// the paint returns. Started during the sleep, the Runnable's repaint() was
// coalesced into the paint still in flight, nothing painted again, and the
// loading screen stayed on its first frame.
func TestKTFCallSeriallyWaitsForTheRunningPaint(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	runtime.DeferThreads = true
	display := newHostObject(t, runtime, "org/kwis/msp/lcdui/Display")
	card := newHostObject(t, runtime, "org/kwis/msp/lcdui/Card")
	runtime.DefaultDisplay = display
	runtime.DisplayCards[display] = card
	painting, err := runtime.NewTask(ImageBase|1, nil, 0)
	check(t, err)
	painting.paintCard = card
	painting.presentOnReturn = true
	painting.WakeAtMS = 5000
	runtime.Tasks = []*Task{painting}
	runtime.PaintTasks[card] = painting
	runtime.TickMS = 4000
	check(t, runtime.Services.Clock.Advance(4000*time.Millisecond))

	runnable := newHostObject(t, runtime, "java/lang/Thread")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, display))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, runnable))
	if _, err := HostJavaMethod(
		"org/kwis/msp/lcdui/Display",
		"callSerially",
		"(Ljava/lang/Runnable;)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 2 {
		t.Fatalf("callSerially queued %d tasks, want the Runnable", len(runtime.Tasks)-1)
	}
	if runtime.HasRunnableTask() {
		t.Fatal("callSerially Runnable can start while its paint is asleep")
	}

	// The order survives a save taken while the paint sleeps.
	var state bytes.Buffer
	check(t, WriteState(runtime, runtime.CPU, true, guest.NewStateWriter(&state)))
	decoder := guest.StateDecoder{Reader: bytes.NewReader(state.Bytes())}
	saved, err := ParseState(runtime, &decoder)
	check(t, err)
	started := false
	check(t, RestoreState(runtime, runtime.CPU, saved, &started))
	painting, serial := runtime.Tasks[0], runtime.Tasks[1]
	if serial.serialAfter != painting {
		t.Fatal("restore lost the Runnable's wait for its paint")
	}
	if runtime.HasRunnableTask() {
		t.Fatal("restored Runnable can start while its paint is asleep")
	}

	painting.Done = true
	if got := runtime.nextRunnableTask(); got != serial {
		t.Fatalf("after the paint returned the scheduler picked %p, want the Runnable %p", got, serial)
	}

	// A save from before the serial-order section still loads.
	legacy := bytes.Clone(state.Bytes()[:state.Len()-4*len(runtime.Tasks)])
	binary.LittleEndian.PutUint32(legacy[4:8], ktfStateSchemaV16)
	decoder = guest.StateDecoder{Reader: bytes.NewReader(legacy)}
	_, err = ParseState(runtime, &decoder)
	check(t, err)
}

// A Runnable posted while no paint is running starts at once, as before.
func TestKTFCallSeriallyWithoutAPaintStartsAtOnce(t *testing.T) {
	runtime := newTestRuntime(t)
	runtime.JvmContext = allocWords(t, runtime, 3+128)
	runtime.DeferThreads = true
	display := newHostObject(t, runtime, "org/kwis/msp/lcdui/Display")
	runtime.DefaultDisplay = display
	runtime.DisplayCards[display] = newHostObject(t, runtime, "org/kwis/msp/lcdui/Card")
	runnable := newHostObject(t, runtime, "java/lang/Thread")
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, display))
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR2, runnable))
	if _, err := HostJavaMethod(
		"org/kwis/msp/lcdui/Display",
		"callSerially",
		"(Ljava/lang/Runnable;)V",
	)(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tasks) != 1 || runtime.Tasks[0].serialAfter != nil ||
		!runtime.HasRunnableTask() {
		t.Fatalf("callSerially without a paint queued %#v", runtime.Tasks)
	}
}
