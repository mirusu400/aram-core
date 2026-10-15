package wipi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	catalog "github.com/mirusu400/aram-core/wipi"
)

const wipiConcreteBuffer = uint32(0x04000000)

var wipiConcreteFailure = errors.New("WIPI concrete-frame oracle failure")

type wipiConcreteContextKey struct{}

// This type deliberately is not *interpreter.Backend. Its optional methods
// enter the original cpu wrappers, so the control never enters the concrete
// guest-helper branch, including lazy captures made inside existing handlers.
type wipiConcreteWrapper struct {
	*interpreter.Backend
	captures, returns int
	failCapture       int
	failReturn        bool
	panicReturn       bool
}

func (b *wipiConcreteWrapper) CaptureHostCallFrame(frame *cpu.HostCallFrame, request cpu.HostCallFrameRequest) error {
	b.captures++
	if b.captures == b.failCapture {
		return wipiConcreteFailure
	}
	return cpu.CaptureHostCallFrame(b.Backend, frame, request)
}

func (b *wipiConcreteWrapper) ReturnHostCallFrame(frame *cpu.HostCallFrame, low, high uint32) error {
	b.returns++
	if b.panicReturn {
		panic("WIPI concrete-frame return panic")
	}
	if b.failReturn {
		return wipiConcreteFailure
	}
	return cpu.ReturnHostCallFrame(b.Backend, frame, low, high)
}

type wipiConcreteNonComparable struct {
	*wipiConcreteWrapper
	marker []byte
}

// These three routines are the literal pre-concrete-helper runtime operations.
// Baseline wipi_runtime.go SHA256:
// 169d127fc46589b9b47af3bf86dbf2f301b8aae5583d65dcd7bf21e08b096a4a.
func wipiConcreteOldPush(r *Runtime) (*wipiHostCallScope, error) {
	if r.hostCallDepth >= len(r.hostCallScopes) {
		return nil, fmt.Errorf("WIPI host-call nesting limit reached")
	}
	scope := &r.hostCallScopes[r.hostCallDepth]
	if err := cpu.CaptureHostCallFrame(r.CPU, &scope.frame, cpu.HostCallFrameRequest{}); err != nil {
		return nil, err
	}
	copy(scope.arguments[:4], scope.frame.Registers[:4])
	scope.argumentWords = 4
	r.hostCallDepth++
	return scope, nil
}

func wipiConcreteOldEnsure(r *Runtime, scope *wipiHostCallScope, count int) error {
	if count <= scope.argumentWords {
		return nil
	}
	if count > len(scope.arguments) {
		return fmt.Errorf("WIPI argument count %d exceeds host-call capacity", count)
	}
	request := cpu.HostCallFrameRequest{}
	if count > 4 {
		request.StackWords = uint32(count - 4)
	}
	if err := cpu.CaptureHostCallFrame(r.CPU, &scope.frame, request); err != nil {
		return err
	}
	copy(scope.arguments[:4], scope.frame.Registers[:4])
	copy(scope.arguments[4:count], scope.frame.Stack[:request.StackWords])
	scope.argumentWords = count
	return nil
}

func wipiConcreteOldReturn(r *Runtime, result guest.WIPIReturn) error {
	frame := &r.standaloneFrame
	if r.hostCallDepth != 0 {
		frame = &r.hostCallScopes[r.hostCallDepth-1].frame
	}
	return cpu.ReturnHostCallFrame(r.CPU, frame, result.Low, result.High)
}

func wipiConcreteOldDispatch(r *Runtime, ctx context.Context, api catalog.API) error {
	if ctx == nil {
		ctx = context.Background()
	}
	previousContext := r.activeContext
	r.activeContext = ctx
	defer func() { r.activeContext = previousContext }()
	scope, err := wipiConcreteOldPush(r)
	if err != nil {
		return err
	}
	defer r.popHostCallScope(scope)
	r.Stats.APICalls++
	r.Stats.LastAPI = api.Name
	r.Observed[api.Name]++
	result, handled, err := r.dispatch(api)
	if err != nil {
		return fmt.Errorf("%s.%s: %w", api.Family, api.Name, err)
	}
	if handled {
		r.Stats.ImplementedCalls++
	} else {
		r.Stats.UnimplementedCalls++
		r.Stats.LastUnimplemented = api.Name
		r.Unimplemented[api.Name]++
		result.Low = defaultWIPIReturn(api.Family)
	}
	return wipiConcreteOldReturn(r, result)
}

func wipiConcreteNewCPU(t *testing.T, create func() *interpreter.Backend, seed uint32) *interpreter.Backend {
	t.Helper()
	b := create()
	t.Cleanup(func() { _ = b.Close() })
	check(t, b.Map(wipiConcreteBuffer, 4096, cpu.PermissionRead|cpu.PermissionWrite))
	check(t, b.Map(guest.DefaultStackBase, guest.DefaultStackSize, cpu.PermissionRead|cpu.PermissionWrite))
	for register := uint32(0); register < cpu.RegisterSP; register++ {
		check(t, b.WriteRegister(register, seed+register))
	}
	check(t, b.WriteRegister(cpu.RegisterSP, guest.DefaultStackBase+guest.DefaultStackSize-0x100))
	check(t, b.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel+seed*2|1))
	check(t, b.WriteRegister(cpu.RegisterCPSR, 0xa0000010|cpu.StatusThumb))
	for index := uint32(0); index < 4; index++ {
		check(t, guest.WriteU32(b, guest.DefaultStackBase+guest.DefaultStackSize-0x100+index*4, seed+100+index))
	}
	return b
}

func wipiConcreteNewRuntime(t *testing.T, create func() *interpreter.Backend) (*Runtime, *interpreter.Backend) {
	t.Helper()
	b := wipiConcreteNewCPU(t, create, 10)
	check(t, MapRuntimeMemory(b))
	r, err := NewRuntime(b, image.NewRGBA(image.Rect(0, 0, 8, 6)))
	check(t, err)
	return r, b
}

func wipiConcreteArgs(t *testing.T, b cpu.Backend, values [4]uint32) {
	t.Helper()
	for register, value := range values {
		check(t, b.WriteRegister(uint32(register), value))
	}
}

type wipiConcreteCPUState struct {
	Context []byte
	Memory  []byte
	Stats   cpu.ExecutionStatistics
}

func wipiConcreteState(t *testing.T, b *interpreter.Backend) wipiConcreteCPUState {
	t.Helper()
	result := wipiConcreteCPUState{Memory: make([]byte, 4096), Stats: b.ExecutionStatistics()}
	var err error
	result.Context, err = b.SaveContext()
	check(t, err)
	check(t, b.ReadMemory(wipiConcreteBuffer, result.Memory))
	return result
}

type wipiConcreteDispatchOutcome struct {
	Err, Panic string
	Wraps      bool
	Depth      int
	Scopes     [16]wipiHostCallScope
	Stats      WIPIFrameStats
	Observed   map[string]uint64
	CPU        wipiConcreteCPUState
	Heap       []byte
	Free       []guest.Block
	Calls      []string
}

func runWIPIConcreteDispatch(t *testing.T, create func() *interpreter.Backend, scenario string, control bool) wipiConcreteDispatchOutcome {
	t.Helper()
	r, base := wipiConcreteNewRuntime(t, create)
	wrapper := &wipiConcreteWrapper{Backend: base}
	custom := scenario == "capture error" || scenario == "extra capture error" || scenario == "return error" || scenario == "return panic"
	if control || custom {
		r.CPU = wrapper
	}
	if scenario == "noncomparable" {
		r.CPU = wipiConcreteNonComparable{wipiConcreteWrapper: wrapper, marker: []byte{1}}
	}
	if !control && !custom && scenario != "noncomparable" {
		if _, direct := r.CPU.(*interpreter.Backend); !direct {
			t.Fatal("positive runtime fixture does not have the exact concrete backend")
		}
	}
	dispatch := (*Runtime).DispatchAPI
	if control {
		dispatch = wipiConcreteOldDispatch
	}
	name := "MC_utilHtonl"
	args := [4]uint32{0x11223344}
	if scenario == "stack" || scenario == "lazy stack fault" || scenario == "extra capture error" {
		name, args = "MC_knlSprintk", [4]uint32{wipiConcreteBuffer + 0x300, wipiConcreteBuffer + 0x200, 0x1234, 17}
		check(t, base.WriteMemory(wipiConcreteBuffer+0x200, []byte("v:%08x %u %x\x00")))
		sp, err := base.ReadRegister(cpu.RegisterSP)
		check(t, err)
		check(t, guest.WriteU32(base, sp, 0x5566))
		if scenario == "lazy stack fault" {
			check(t, base.WriteRegister(cpu.RegisterSP, 0xf0000000))
		}
	}
	var calls []string
	var reentered bool
	r.InvokeSync = func(ctx context.Context, call GuestCallback) (uint32, error) {
		value, _ := ctx.Value(wipiConcreteContextKey{}).(string)
		calls = append(calls, fmt.Sprintf("callback:%s:depth=%d", value, r.hostCallDepth))
		if scenario == "callback error" {
			return 0, wipiConcreteFailure
		}
		if scenario == "callback panic" {
			panic("WIPI concrete-frame callback panic")
		}
		if scenario == "callback reentry" && !reentered {
			reentered = true
			saved, err := base.SaveContext()
			check(t, err)
			wipiConcreteArgs(t, base, [4]uint32{7, wipiConcreteBuffer + 0x380, 4, 0})
			api, _ := catalog.Lookup("MC_dbSortRecords")
			check(t, dispatch(r, context.WithValue(context.Background(), wipiConcreteContextKey{}, "inner"), api))
			check(t, base.RestoreContext(saved))
			value, _ = r.activeContext.Value(wipiConcreteContextKey{}).(string)
			calls = append(calls, "restored:"+value)
		}
		return 1, nil
	}
	if scenario == "callback" || scenario == "callback error" || scenario == "callback panic" || scenario == "callback reentry" {
		name, args = "MC_dbSortRecords", [4]uint32{7, wipiConcreteBuffer + 0x300, 4, 0}
		r.Databases["oracle"] = &Database{Name: "oracle", RecordSize: 4, Records: map[int32][]byte{7: {1, 2, 3, 4}}}
		r.DatabaseHandles[7] = "oracle"
		sp, err := base.ReadRegister(cpu.RegisterSP)
		check(t, err)
		check(t, guest.WriteU32(base, sp, 0x12340001))
	}
	wipiConcreteArgs(t, base, args)
	switch scenario {
	case "capture error":
		wrapper.failCapture = 1
	case "extra capture error":
		wrapper.failCapture = 2
	case "return error":
		wrapper.failReturn = true
	case "return panic":
		wrapper.panicReturn = true
	}
	api, found := catalog.Lookup(name)
	if !found {
		t.Fatal("fixture API is missing")
	}
	var result wipiConcreteDispatchOutcome
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				result.Panic = fmt.Sprint(recovered)
			}
		}()
		if err := dispatch(r, context.WithValue(context.Background(), wipiConcreteContextKey{}, "outer"), api); err != nil {
			result.Err, result.Wraps = err.Error(), errors.Is(err, wipiConcreteFailure)
		}
	}()
	if r.activeContext != nil {
		t.Fatal("dispatch left an active context after return/error/panic")
	}
	if custom && wrapper.captures == 0 {
		t.Fatal("custom backend's capture override was bypassed")
	}
	if (scenario == "return error" || scenario == "return panic") && wrapper.returns != 1 {
		t.Fatal("custom backend's return override was bypassed")
	}
	result.Depth, result.Scopes, result.Stats = r.hostCallDepth, r.hostCallScopes, r.Stats
	result.Observed, result.CPU, result.Calls = r.Observed, wipiConcreteState(t, base), calls
	result.Heap = make([]byte, 64<<10)
	check(t, base.ReadMemory(guest.HeapBase, result.Heap))
	result.Free = append([]guest.Block(nil), r.Heap.Root().Free...)
	if len(r.Heap.Root().Allocations) != 0 {
		t.Fatal("callback temporary guest allocations leaked")
	}
	return result
}

func TestWIPIConcreteHostCallDispatchMatchesOriginalWrappers(t *testing.T) {
	for _, factory := range []struct {
		name   string
		create func() *interpreter.Backend
	}{{"precise", interpreter.New}, {"portable", interpreter.NewJIT}} {
		for _, scenario := range []string{"utility", "noncomparable", "stack", "lazy stack fault", "callback", "callback error", "callback panic", "callback reentry", "capture error", "extra capture error", "return error", "return panic"} {
			t.Run(factory.name+"/"+scenario, func(t *testing.T) {
				want := runWIPIConcreteDispatch(t, factory.create, scenario, true)
				got := runWIPIConcreteDispatch(t, factory.create, scenario, false)
				if !reflect.DeepEqual(got, want) {
					t.Fatal("runtime differs from literal old scope/return wrappers")
				}
				if got.Depth != 0 {
					t.Fatal("scope leaked")
				}
				switch scenario {
				case "capture error", "extra capture error", "return error", "callback error":
					if got.Err == "" || !got.Wraps {
						t.Fatal("injected error/wrapping was lost")
					}
				case "lazy stack fault":
					if got.Err == "" || got.Scopes[0].argumentWords != 4 {
						t.Fatal("lazy stack fault committed extra argument metadata")
					}
				case "return panic":
					if got.Panic != "WIPI concrete-frame return panic" {
						t.Fatal("return panic was lost")
					}
				case "callback panic":
					if got.Panic != "WIPI concrete-frame callback panic" {
						t.Fatal("callback panic was lost")
					}
				case "stack":
					if !bytes.Equal(got.CPU.Memory[0x300:0x313], []byte("v:00001234 17 5566\x00")) {
						t.Fatal("stack argument fixture did not execute")
					}
				case "callback reentry":
					if !reflect.DeepEqual(got.Calls, []string{"callback:outer:depth=1", "callback:inner:depth=2", "restored:outer"}) {
						t.Fatal("nested callback/context was not exercised")
					}
				}
				if got.Err == "" && got.Panic == "" && (got.CPU.Stats.HostFrameCaptures == 0 || got.CPU.Stats.HostCallReturnBatches == 0) {
					t.Fatal("successful direct/control fixture did not capture and return")
				}
			})
		}
	}
}

type wipiConcreteReplacementOutcome struct {
	CPU       [3]wipiConcreteCPUState
	Scopes    [16]wipiHostCallScope
	Stages    []wipiHostCallScope
	Errors    []string
	Depth     int
	Extra     uint32
	BCaptures int
	BReturns  int
	Context   string
}

func runWIPIConcreteReplacement(t *testing.T, create func() *interpreter.Backend, control, callback bool) wipiConcreteReplacementOutcome {
	t.Helper()
	r, a := wipiConcreteNewRuntime(t, create)
	b, c := wipiConcreteNewCPU(t, create, 20), wipiConcreteNewCPU(t, create, 30)
	wa, wb, wc := &wipiConcreteWrapper{Backend: a}, &wipiConcreteWrapper{Backend: b}, &wipiConcreteWrapper{Backend: c}
	var currentA, currentC cpu.Backend = a, c
	if control {
		currentA, currentC = wa, wc
	}
	r.CPU = currentA
	push, ensure, returnFrame := (*Runtime).pushHostCallScope, (*Runtime).ensureHostCallArguments, (*Runtime).ReturnFromTrap
	if control {
		push, ensure, returnFrame = wipiConcreteOldPush, wipiConcreteOldEnsure, wipiConcreteOldReturn
	}
	var result wipiConcreteReplacementOutcome
	if !callback {
		scope, err := push(r)
		check(t, err)
		result.Stages = append(result.Stages, *scope)
		r.CPU = wb
		check(t, b.WriteRegister(cpu.RegisterSP, 0xf0000000))
		check(t, ensure(r, scope, 4))
		result.Stages = append(result.Stages, *scope)
		err = ensure(r, scope, 5)
		if !errors.Is(err, cpu.ErrInvalidAddress) {
			t.Fatal("extra stack capture did not fault")
		}
		result.Errors = append(result.Errors, err.Error())
		result.Stages = append(result.Stages, *scope)
		check(t, b.WriteRegister(cpu.RegisterSP, guest.DefaultStackBase+guest.DefaultStackSize-0x100))
		check(t, ensure(r, scope, 5))
		result.Stages = append(result.Stages, *scope)
		r.CPU = currentC
		check(t, returnFrame(r, guest.WIPIReturn{Low: 0x77, High: 0x88}))
		r.popHostCallScope(scope)
	} else {
		dispatch := (*Runtime).DispatchAPI
		if control {
			dispatch = wipiConcreteOldDispatch
		}
		r.Databases["oracle"] = &Database{Name: "oracle", RecordSize: 4, Records: map[int32][]byte{7: {1, 2, 3, 4}}}
		r.DatabaseHandles[7] = "oracle"
		wipiConcreteArgs(t, a, [4]uint32{7, wipiConcreteBuffer + 0x300, 4})
		check(t, guest.WriteU32(a, guest.DefaultStackBase+guest.DefaultStackSize-0x100, 0x12340001))
		r.InvokeSync = func(ctx context.Context, call GuestCallback) (uint32, error) {
			result.Context, _ = ctx.Value(wipiConcreteContextKey{}).(string)
			r.CPU = wb
			api, _ := catalog.Lookup("MC_utilHtonl")
			check(t, dispatch(r, context.WithValue(context.Background(), wipiConcreteContextKey{}, "inner"), api))
			value, err := r.arg(5)
			check(t, err)
			result.Extra = value
			result.Stages = append(result.Stages, r.hostCallScopes[0])
			if restored, _ := r.activeContext.Value(wipiConcreteContextKey{}).(string); restored != "outer" {
				t.Fatal("reentry lost enclosing context")
			}
			r.CPU = currentC
			return 1, nil
		}
		api, _ := catalog.Lookup("MC_dbSortRecords")
		check(t, dispatch(r, context.WithValue(context.Background(), wipiConcreteContextKey{}, "outer"), api))
		if r.activeContext != nil {
			t.Fatal("replacement callback leaked context")
		}
	}
	result.CPU = [3]wipiConcreteCPUState{wipiConcreteState(t, a), wipiConcreteState(t, b), wipiConcreteState(t, c)}
	result.Scopes, result.Depth = r.hostCallScopes, r.hostCallDepth
	result.BCaptures, result.BReturns = wb.captures, wb.returns
	return result
}

func TestWIPIConcreteHostCallUsesCurrentCPUPerOperation(t *testing.T) {
	for _, factory := range []struct {
		name   string
		create func() *interpreter.Backend
	}{{"precise", interpreter.New}, {"portable", interpreter.NewJIT}} {
		for _, callback := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/callback=%t", factory.name, callback), func(t *testing.T) {
				want := runWIPIConcreteReplacement(t, factory.create, true, callback)
				got := runWIPIConcreteReplacement(t, factory.create, false, callback)
				if !reflect.DeepEqual(got, want) {
					t.Fatal("capture/ensure/return retained an earlier CPU")
				}
				if got.Depth != 0 || got.BCaptures != 2 {
					t.Fatal("replacement/cleanup fixture did not exercise wrapper B twice")
				}
				if !callback {
					if got.Stages[0].arguments[0] != 10 || got.Stages[1].argumentWords != 4 || got.Stages[2].argumentWords != 4 || got.Stages[3].arguments[0] != 20 || got.Stages[3].arguments[4] != 120 {
						t.Fatal("lazy failure/retry metadata changed")
					}
				} else if got.Extra != 121 || got.Context != "outer" || got.BReturns != 1 || !bytes.Equal(got.CPU[2].Memory[0x300:0x304], []byte{7, 0, 0, 0}) {
					t.Fatal("real callback replacement did not use B arguments and C output/return")
				}
				if got.CPU[2].Stats.HostCallReturnBatches == 0 {
					t.Fatal("direct C return was not exercised")
				}
			})
		}
	}
}
