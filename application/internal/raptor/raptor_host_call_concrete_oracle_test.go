package raptor

import (
	"context"
	"errors"
	"fmt"
	"image"
	"reflect"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	shared "github.com/mirusu400/aram-core/runtime"
	"github.com/mirusu400/aram-core/wipi"
)

const raptorConcreteBuffer = uint32(0x04000000)

var raptorConcreteFailure = errors.New("Raptor concrete-frame oracle failure")

type raptorConcreteContextKey struct{}

// Mask only the concrete type, not its batch semantics or counters. The
// reference dispatcher therefore enters the unchanged cpu wrapper at every
// capture/return, while the positive fixture is the actual New/NewJIT pointer.
// No rejected API-selector cache or runtime backend cache is installed here.
type raptorConcreteWrapper struct {
	*interpreter.Backend
	captures, returns int
	readR0            int
	failCapture       int
	failReturn        bool
	panicReturn       bool
	failReadR0        bool
	panicReadR0       bool
}

func (b *raptorConcreteWrapper) CaptureHostCallFrame(frame *cpu.HostCallFrame, request cpu.HostCallFrameRequest) error {
	b.captures++
	if b.captures == b.failCapture {
		return raptorConcreteFailure
	}
	return cpu.CaptureHostCallFrame(b.Backend, frame, request)
}

func (b *raptorConcreteWrapper) ReturnHostCallFrame(frame *cpu.HostCallFrame, low, high uint32) error {
	b.returns++
	if b.panicReturn {
		panic("Raptor concrete-frame return panic")
	}
	if b.failReturn {
		return raptorConcreteFailure
	}
	return cpu.ReturnHostCallFrame(b.Backend, frame, low, high)
}

func (b *raptorConcreteWrapper) ReadRegister(register uint32) (uint32, error) {
	if register == cpu.RegisterR0 {
		b.readR0++
		if b.panicReadR0 {
			panic("Raptor concrete-frame audio read panic")
		}
		if b.failReadR0 {
			return 0, raptorConcreteFailure
		}
	}
	return b.Backend.ReadRegister(register)
}

func raptorConcreteNewCPU(t *testing.T, create func() *interpreter.Backend, seed uint32) *interpreter.Backend {
	t.Helper()
	b := create()
	t.Cleanup(func() { _ = b.Close() })
	check(t, b.Map(raptorConcreteBuffer, 4096, cpu.PermissionRead|cpu.PermissionWrite))
	check(t, b.Map(guest.DefaultStackBase, guest.DefaultStackSize, cpu.PermissionRead|cpu.PermissionWrite))
	for register := uint32(0); register < cpu.RegisterSP; register++ {
		check(t, b.WriteRegister(register, seed+register))
	}
	check(t, b.WriteRegister(cpu.RegisterSP, guest.DefaultStackBase+guest.DefaultStackSize-0x100))
	check(t, b.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel+seed*2|1))
	check(t, b.WriteRegister(cpu.RegisterCPSR, 0xa0000010|cpu.StatusThumb))
	check(t, guest.WriteU32(b, guest.DefaultStackBase+guest.DefaultStackSize-0x100, 0x5566))
	return b
}

func raptorConcreteArgs(t *testing.T, b cpu.Backend, args [4]uint32) {
	t.Helper()
	for register, value := range args {
		check(t, b.WriteRegister(uint32(register), value))
	}
}

type raptorConcreteCPUState struct {
	Context []byte
	Memory  []byte
	Stats   cpu.ExecutionStatistics
	R0      uint32
}

func raptorConcreteState(t *testing.T, b *interpreter.Backend) raptorConcreteCPUState {
	t.Helper()
	result := raptorConcreteCPUState{Memory: make([]byte, 4096), Stats: b.ExecutionStatistics()}
	var err error
	result.Context, err = b.SaveContext()
	check(t, err)
	result.R0, err = b.ReadRegister(cpu.RegisterR0)
	check(t, err)
	check(t, b.ReadMemory(raptorConcreteBuffer, result.Memory))
	return result
}

type raptorConcreteOutcome struct {
	Err, Panic    string
	Wraps         bool
	CPU           [3]raptorConcreteCPUState
	Depth         int
	Frames        [16]cpu.HostCallFrame
	Imports       []raptorImportCall
	Audio         []guest.DebugRaptorAudioCall
	AudioCalls    uint64
	Stats         wipirt.WIPIFrameStats
	Observed      map[string]uint64
	Unimplemented map[string]uint64
	Calls         []string
	Heap          []byte
	Free          []guest.Block
	Services      shared.ServicesState
}

func runRaptorConcreteOracle(t *testing.T, create func() *interpreter.Backend, scenario string, control bool) raptorConcreteOutcome {
	t.Helper()
	a, b, c := raptorConcreteNewCPU(t, create, 10), raptorConcreteNewCPU(t, create, 20), raptorConcreteNewCPU(t, create, 30)
	check(t, wipirt.MapRuntimeMemory(a))
	separate := scenario == "separate CPU" || scenario == "separate audio CPU"
	publicBase := a
	if separate {
		check(t, wipirt.MapRuntimeMemory(b))
		publicBase = b
	}
	public, err := wipirt.NewRuntime(publicBase, image.NewRGBA(image.Rect(0, 0, 8, 6)))
	check(t, err)
	r := &Runtime{CPU: a, Public: public}
	wa, wb, wc := &raptorConcreteWrapper{Backend: a}, &raptorConcreteWrapper{Backend: b}, &raptorConcreteWrapper{Backend: c}
	custom := scenario == "capture error" || scenario == "scope capture error" || scenario == "return error" || scenario == "return panic" || scenario == "audio read error" || scenario == "audio read panic"
	if control || custom {
		r.CPU, public.CPU = wa, wa
		if separate {
			public.CPU = wb
		}
	}
	if !control && !custom {
		if _, direct := r.CPU.(*interpreter.Backend); !direct {
			t.Fatal("Raptor positive is not the exact interpreter pointer")
		}
		if _, direct := public.CPU.(*interpreter.Backend); !direct {
			t.Fatal("public positive is not the exact interpreter pointer")
		}
	}
	dispatch := (*Runtime).dispatchImport
	if control {
		// This literal pre-selector/pre-concrete dispatcher is kept unchanged.
		// Every backend in its control is masked above, so even the runtime scope
		// methods it calls use the original cpu wrapper rather than the new path.
		dispatch = referenceCleanupDispatchImport
	}
	check(t, public.Services.Advance(public.ServiceOwner, 137*time.Millisecond))
	key := raptorImportKey{Module: 1, Ordinal: 900}
	args := [4]uint32{0x11223344}
	if separate {
		raptorConcreteArgs(t, b, [4]uint32{0x55667788})
	}
	switch scenario {
	case "stack":
		key.Ordinal, args = 101, [4]uint32{raptorConcreteBuffer + 0x300, raptorConcreteBuffer + 0x200, 0x1234, 17}
		check(t, a.WriteMemory(raptorConcreteBuffer+0x200, []byte("v:%08x %u %x\x00")))
	case "public fault":
		key.Ordinal, args = 904, [4]uint32{0xf0000000}
	case "private audio":
		key.Module, key.Ordinal, args = 507, 1210, [4]uint32{^uint32(0)}
	case "audio", "caps", "capture error", "scope capture error", "return error", "return panic", "audio read error", "audio read panic", "separate audio CPU":
		key.Ordinal = 1208
		if !separate {
			args = [4]uint32{^uint32(0)}
		}
	}
	if scenario == "caps" {
		for index := 0; index < wipirt.MaxSavedEntries; index++ {
			r.ImportTrace = append(r.ImportTrace, raptorImportCall{Module: 900, Ordinal: uint32(index)})
		}
		for index := 0; index < maxAudioImportTrace; index++ {
			r.AudioImportTrace = append(r.AudioImportTrace, guest.DebugRaptorAudioCall{DebugRaptorImportCall: guest.DebugRaptorImportCall{Ordinal: uint32(index)}, Name: "seed"})
		}
		r.AudioImportCalls = 1000
	}
	var calls []string
	var reentered bool
	const probe = uint32(0x12340005)
	public.InvokeSync = func(ctx context.Context, call wipirt.GuestCallback) (uint32, error) {
		value, _ := ctx.Value(raptorConcreteContextKey{}).(string)
		if call.Procedure == probe {
			calls = append(calls, fmt.Sprintf("probe:%s:depth=%d", value, r.hostCallDepth))
			return 1, nil
		}
		calls = append(calls, fmt.Sprintf("callback:%s:depth=%d", value, r.hostCallDepth))
		if scenario == "callback error" {
			return 0, raptorConcreteFailure
		}
		if scenario == "callback panic" {
			panic("Raptor concrete-frame callback panic")
		}
		if scenario == "callback reentry" && !reentered {
			reentered = true
			saved, saveErr := a.SaveContext()
			check(t, saveErr)
			raptorConcreteArgs(t, a, [4]uint32{7, raptorConcreteBuffer + 0x380, 4})
			check(t, dispatch(r, context.WithValue(context.Background(), raptorConcreteContextKey{}, "inner"), key))
			check(t, a.RestoreContext(saved))
			_, probeErr := public.CallGuestFunction(probe)
			check(t, probeErr)
		}
		if scenario == "callback replacement" && !reentered {
			reentered = true
			r.CPU = wb
			public.CPU = c
			if control {
				public.CPU = wc
			}
			check(t, dispatch(r, context.WithValue(context.Background(), raptorConcreteContextKey{}, "inner"), raptorImportKey{Module: 1, Ordinal: 900}))
			_, probeErr := public.CallGuestFunction(probe)
			check(t, probeErr)
		}
		return 1, nil
	}
	if scenario == "callback" || scenario == "callback error" || scenario == "callback panic" || scenario == "callback reentry" || scenario == "callback replacement" {
		api, found := wipi.Lookup("MC_dbSortRecords")
		if !found {
			t.Fatal("fixture database API missing")
		}
		key.Ordinal, args = libwipiARAMImportBase+uint32(api.Ordinal), [4]uint32{7, raptorConcreteBuffer + 0x300, 4, 0}
		public.Databases["oracle"] = &wipirt.Database{Name: "oracle", RecordSize: 4, Records: map[int32][]byte{7: {1, 2, 3, 4}}}
		public.DatabaseHandles[7] = "oracle"
		check(t, guest.WriteU32(a, guest.DefaultStackBase+guest.DefaultStackSize-0x100, 0x12340001))
	}
	raptorConcreteArgs(t, a, args)
	switch scenario {
	case "capture error":
		wa.failCapture = 1
	case "scope capture error":
		wa.failCapture = 2
	case "return error":
		wa.failReturn = true
	case "return panic":
		wa.panicReturn = true
	case "audio read error":
		wa.failReadR0 = true
	case "audio read panic":
		wa.panicReadR0 = true
	}
	var result raptorConcreteOutcome
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				result.Panic = fmt.Sprint(recovered)
			}
		}()
		if dispatchErr := dispatch(r, context.WithValue(context.Background(), raptorConcreteContextKey{}, "outer"), key); dispatchErr != nil {
			result.Err, result.Wraps = dispatchErr.Error(), errors.Is(dispatchErr, raptorConcreteFailure)
		}
	}()
	_, err = public.CallGuestFunction(probe)
	check(t, err)
	if custom && wa.captures == 0 {
		t.Fatal("custom capture override was bypassed")
	}
	if (scenario == "return error" || scenario == "return panic") && wa.returns != 1 {
		t.Fatal("custom return override was bypassed")
	}
	if (scenario == "audio read error" || scenario == "audio read panic") && wa.readR0 != 1 {
		t.Fatal("audio observer override was bypassed")
	}
	result.CPU = [3]raptorConcreteCPUState{raptorConcreteState(t, a), raptorConcreteState(t, b), raptorConcreteState(t, c)}
	result.Depth, result.Frames = r.hostCallDepth, r.hostCallFrames
	result.Imports, result.Audio, result.AudioCalls = r.ImportTrace, r.AudioImportTrace, r.AudioImportCalls
	result.Stats, result.Observed, result.Unimplemented = public.Stats, public.Observed, public.Unimplemented
	result.Calls, result.Services = calls, public.Services.Snapshot()
	result.Heap, result.Free = make([]byte, 64<<10), append([]guest.Block(nil), public.Heap.Root().Free...)
	check(t, publicBase.ReadMemory(guest.HeapBase, result.Heap))
	if len(public.Heap.Root().Allocations) != 0 {
		t.Fatal("callback temporary heap allocation leaked")
	}
	return result
}

func TestRaptorConcreteHostCallMatchesOriginalWrappers(t *testing.T) {
	for _, factory := range []struct {
		name   string
		create func() *interpreter.Backend
	}{{"precise", interpreter.New}, {"portable", interpreter.NewJIT}} {
		for _, scenario := range []string{"public", "stack", "public fault", "private audio", "audio", "caps", "capture error", "scope capture error", "return error", "return panic", "audio read error", "audio read panic", "callback", "callback error", "callback panic", "callback reentry", "callback replacement", "separate CPU", "separate audio CPU"} {
			t.Run(factory.name+"/"+scenario, func(t *testing.T) {
				want := runRaptorConcreteOracle(t, factory.create, scenario, true)
				got := runRaptorConcreteOracle(t, factory.create, scenario, false)
				if !reflect.DeepEqual(got, want) {
					t.Fatal("Raptor runtime differs from unchanged literal dispatcher and original CPU wrappers")
				}
				if got.Depth != 0 || len(got.Calls) == 0 || got.Calls[len(got.Calls)-1] != "probe::depth=0" {
					t.Fatal("frame/scope/context leaked after dispatch")
				}
				switch scenario {
				case "capture error", "scope capture error", "return error", "callback error":
					if got.Err == "" || !got.Wraps {
						t.Fatal("injected error/wrapping lost")
					}
				case "public fault":
					if got.Err == "" {
						t.Fatal("public pointer fixture did not fault")
					}
				case "return panic":
					if got.Panic != "Raptor concrete-frame return panic" || len(got.Audio) != 1 || got.Audio[0].Fault {
						t.Fatal("return panic changed named-error/audio cleanup")
					}
				case "audio read panic":
					if got.Panic != "Raptor concrete-frame audio read panic" || got.AudioCalls != 0 {
						t.Fatal("audio panic committed a trace or was lost")
					}
				case "audio read error":
					if got.Err != "" || len(got.Audio) != 1 || !got.Audio[0].Fault {
						t.Fatal("audio read error changed dispatch result/fault trace")
					}
				case "callback panic":
					if got.Panic != "Raptor concrete-frame callback panic" {
						t.Fatal("callback panic lost")
					}
				case "caps":
					if len(got.Imports) != wipirt.MaxSavedEntries/2+1 || got.Imports[0].Ordinal != uint32(wipirt.MaxSavedEntries/2) || len(got.Audio) != 64 || got.Audio[0].Ordinal != 1 || got.AudioCalls != 1001 {
						t.Fatal("trace cap retention/count changed")
					}
				case "callback reentry":
					if !reflect.DeepEqual(got.Calls, []string{"callback:outer:depth=1", "callback:inner:depth=2", "probe:outer:depth=1", "probe::depth=0"}) {
						t.Fatal("actual nested callback/context was not exercised")
					}
				case "callback replacement":
					if len(got.Imports) != 2 || got.Imports[0].Args[0] != 7 || got.Imports[1].Args[0] != 20 || got.CPU[1].Stats.HostFrameCaptures != 1 || got.CPU[2].Stats.HostCallReturnBatches != 2 || !reflect.DeepEqual(got.CPU[2].Memory[0x300:0x304], []byte{7, 0, 0, 0}) {
						t.Fatal("nested replacement did not capture B and dispatch/output/return through C")
					}
				case "separate CPU":
					if got.Imports[0].Args[0] != 0x11223344 || got.CPU[0].R0 != 0x11223344 || got.CPU[1].R0 != 0x88776655 || got.CPU[0].Stats.HostFrameCaptures != 1 || got.CPU[1].Stats.HostCallReturnBatches != 1 {
						t.Fatal("Raptor and public CPU were incorrectly unified")
					}
				case "separate audio CPU":
					if len(got.Audio) != 1 || got.Audio[0].Args[0] != 0x11223344 || got.Audio[0].Result != 0x11223344 || got.CPU[1].R0 != ^uint32(0) {
						t.Fatal("audio result no longer observes the original Raptor CPU")
					}
				}
				if got.Err == "" && got.Panic == "" && got.Stats.APICalls == 0 {
					t.Fatal("successful fixture did not dispatch an API")
				}
			})
		}
	}
}
