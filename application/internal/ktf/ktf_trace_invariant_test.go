package ktf

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	ktfloader "github.com/mirusu400/aram-core/loader/ktf"
	shared "github.com/mirusu400/aram-core/runtime"
)

type ktfTraceInvariantOutcome struct {
	Registers [17]uint32
	Memory    [4]byte
	Frame     shared.FrameSnapshot
	Events    shared.EventBusState
}

func runKTFTraceInvariantScenario(
	t *testing.T,
	mode KTFTraceMode,
) ktfTraceInvariantOutcome {
	t.Helper()
	backend := interpreter.New()
	t.Cleanup(func() { _ = backend.Close() })
	runtime, err := NewRuntime(backend, ktfloader.Package{
		ClientName: "client.bin0",
		Client:     []byte{0x70, 0x47}, // bx lr
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetTraceMode(mode); err != nil {
		t.Fatal(err)
	}

	surface, err := runtime.Services.Graphics.CreateSurface(
		runtime.ServiceOwner,
		shared.SurfaceDescriptor{
			Width: 2, Height: 1, Format: shared.PixelRGBA8888,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Services.Graphics.SetScreen(runtime.ServiceOwner, surface); err != nil {
		t.Fatal(err)
	}
	const scratch = guest.DefaultStackBase + 0x400
	if err := runtime.writeWords(scratch, []uint32{2}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 1); err != nil {
		t.Fatal(err)
	}

	callIndex := 0
	host := ktfHostCall{
		name: "semantic.invariant",
		handler: func(_ context.Context, current *Runtime) (uint32, error) {
			argument, err := current.parameter(0)
			if err != nil {
				return 0, err
			}
			value, err := current.ReadU32(scratch)
			if err != nil {
				return 0, err
			}
			value += argument
			if err := current.writeWords(scratch, []uint32{value}); err != nil {
				return 0, err
			}
			if err := current.CPU.WriteRegister(cpu.RegisterR4, value^0x55aa55aa); err != nil {
				return 0, err
			}
			if err := current.Services.Graphics.SetPixel(
				current.ServiceOwner,
				surface,
				int32(callIndex%2),
				0,
				shared.RGB(uint8(value), uint8(value>>8), uint8(value>>16)),
			); err != nil {
				return 0, err
			}
			if _, err := current.Services.Graphics.Present(
				current.ServiceOwner,
				surface,
				shared.Rectangle{},
			); err != nil {
				return 0, err
			}
			_, err = current.Services.Events.Enqueue(shared.Event{
				At:    time.Duration(callIndex) * time.Millisecond,
				Kind:  shared.EventApplication,
				Owner: current.ServiceOwner,
				Name:  "trace-invariant",
				Value: int64(value),
			})
			callIndex++
			return value, err
		},
	}
	for range 3 {
		runtime.TraceHostCall(host.name)
		value, err := runtime.invokeHostHandler(context.Background(), host)
		if err != nil {
			t.Fatal(err)
		}
		var commit cpu.RegisterCommit
		if err := commit.Set(cpu.RegisterR0, value); err != nil {
			t.Fatal(err)
		}
		if err := cpu.CommitHostCallRegisters(runtime.CPU, commit); err != nil {
			t.Fatal(err)
		}
	}

	var frame cpu.HostCallFrame
	if err := cpu.CaptureHostCallFrame(
		runtime.CPU,
		&frame,
		cpu.HostCallFrameRequest{},
	); err != nil {
		t.Fatal(err)
	}
	outcome := ktfTraceInvariantOutcome{
		Registers: frame.Registers,
		Frame:     runtime.Services.Graphics.LastFrame(),
		Events:    runtime.Services.Events.Snapshot(),
	}
	if err := runtime.CPU.ReadMemory(scratch, outcome.Memory[:]); err != nil {
		t.Fatal(err)
	}
	return outcome
}

func TestKTFTraceModesDoNotChangeGuestSemantics(t *testing.T) {
	want := runKTFTraceInvariantScenario(t, KTFTraceOff)
	for _, mode := range []KTFTraceMode{
		KTFTraceCounters,
		KTFTraceSampled,
		KTFTraceFull,
	} {
		got := runKTFTraceInvariantScenario(t, mode)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trace mode %s changed registers, memory, frame, or events", mode)
		}
	}
}
