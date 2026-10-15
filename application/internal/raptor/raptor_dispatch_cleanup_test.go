package raptor

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/application/internal/guest"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/wipi"
)

// This is the original, unsplit import dispatcher. In particular the audio
// closure sees the named error before the enclosing frame is popped.
func referenceCleanupDispatchImport(r *Runtime, ctx context.Context, key raptorImportKey) (dispatchErr error) {
	frame, err := r.pushHostCallFrame()
	if err != nil {
		return err
	}
	defer r.popHostCallFrame(frame)
	call := raptorImportCall{Module: key.Module, Ordinal: key.Ordinal}
	for register := cpu.RegisterR0; register <= cpu.RegisterR3; register++ {
		call.Args[register] = frame.Registers[register]
	}
	call.LR = frame.Registers[cpu.RegisterLR]
	if (key.Module == 1 || key.Module == 507) && key.Ordinal >= 1200 && key.Ordinal <= 1234 {
		guestNS := int64(r.Public.Services.Clock.Monotonic())
		defer func() { r.recordAudioImport(call, guestNS, dispatchErr) }()
	}
	if len(r.ImportTrace) < wipirt.MaxSavedEntries {
		r.ImportTrace = append(r.ImportTrace, call)
	} else {
		keep := len(r.ImportTrace) / 2
		copy(r.ImportTrace, r.ImportTrace[len(r.ImportTrace)-keep:])
		r.ImportTrace = append(r.ImportTrace[:keep], call)
	}
	if key.Module == 100 {
		if key.Ordinal == 85 {
			r.observeJavaSafepoint(call.LR)
			const name = "RAPTOR.Java.safepoint"
			r.Public.Stats.APICalls++
			r.Public.Stats.ImplementedCalls++
			r.Public.Stats.LastAPI = name
			r.Public.Observed[name]++
			return r.Public.ReturnFromTrap(guest.WIPIReturn{})
		}
		if class, raises := raptorJavaThrowClasses[key.Ordinal]; raises {
			r.recordRaptorJavaThrow(class, call.LR)
			return r.Public.ReturnFromTrap(guest.WIPIReturn{})
		}
	}
	if result, name, handled, javaErr := r.dispatchJavaImport(ctx, key); handled {
		if javaErr != nil {
			return fmt.Errorf("%s: %w", name, javaErr)
		}
		r.Public.Stats.APICalls++
		r.Public.Stats.ImplementedCalls++
		r.Public.Stats.LastAPI = name
		r.Public.Observed[name]++
		return r.Public.ReturnFromTrap(result)
	}
	if key.Module == 507 {
		if result, name, handled, privateErr := r.DispatchPrivateImport(key.Ordinal); handled {
			if privateErr != nil {
				return fmt.Errorf("%s: %w", name, privateErr)
			}
			r.Public.Stats.APICalls++
			r.Public.Stats.ImplementedCalls++
			r.Public.Stats.LastAPI = name
			r.Public.Observed[name]++
			return r.Public.ReturnFromTrap(result)
		}
	}
	if publicName, ok := raptorWIPIImportName(key.Ordinal); ok && (key.Module == 1 || key.Module == 507) {
		api, found := wipi.Lookup(publicName)
		if !found {
			return fmt.Errorf("Raptor import %d names unknown public WIPI API %q", key.Ordinal, publicName)
		}
		return r.Public.DispatchAPI(ctx, api)
	}
	name := r.unimplementedImportName(key)
	r.Public.Stats.APICalls++
	r.Public.Stats.UnimplementedCalls++
	r.Public.Stats.LastAPI = name
	r.Public.Stats.LastUnimplemented = name
	r.Public.Observed[name]++
	r.Public.Unimplemented[name]++
	return r.Public.ReturnFromTrap(guest.WIPIReturn{})
}

type cleanupRaptorBackend struct {
	cpu.Backend
	runtime     *Runtime
	captureErr  error
	returnErr   error
	panicReturn bool
	panicRead   int
	errorRead   int
	r0Reads     int
	onReturn    func()
	events      []string
}

func (b *cleanupRaptorBackend) CaptureHostCallFrame(frame *cpu.HostCallFrame, request cpu.HostCallFrameRequest) error {
	if b.captureErr != nil {
		return b.captureErr
	}
	return cpu.CaptureHostCallFrame(b.Backend, frame, request)
}

func (b *cleanupRaptorBackend) CommitHostCallRegisters(commit cpu.RegisterCommit) error {
	return cpu.CommitHostCallRegisters(b.Backend, commit)
}

func (b *cleanupRaptorBackend) ReturnHostCallFrame(frame *cpu.HostCallFrame, low, high uint32) error {
	b.events = append(b.events, fmt.Sprintf("return:depth=%d:audio=%d:low=%08x:high=%08x", b.runtime.hostCallDepth, b.runtime.AudioImportCalls, low, high))
	if b.onReturn != nil {
		callback := b.onReturn
		b.onReturn = nil
		callback()
	}
	if b.panicReturn {
		panic("return panic")
	}
	if b.returnErr != nil {
		return b.returnErr
	}
	return cpu.ReturnHostCallFrame(b.Backend, frame, low, high)
}

func (b *cleanupRaptorBackend) ReadRegister(register uint32) (uint32, error) {
	if register == cpu.RegisterR0 {
		b.r0Reads++
		b.events = append(b.events, fmt.Sprintf("read R0:depth=%d:audio=%d", b.runtime.hostCallDepth, b.runtime.AudioImportCalls))
		if b.r0Reads == b.panicRead {
			panic("audio recording panic")
		}
		if b.r0Reads == b.errorRead {
			return 0, errors.New("audio result read failed")
		}
	}
	return b.Backend.ReadRegister(register)
}

type cleanupRaptorOutcome struct {
	Err           string
	Panic         string
	Depth         int
	Registers     [17]uint32
	Events        []string
	Imports       []raptorImportCall
	Audio         []guest.DebugRaptorAudioCall
	AudioCalls    uint64
	Stats         wipirt.WIPIFrameStats
	Observed      map[string]uint64
	Unimplemented map[string]uint64
}

func runCleanupRaptorCase(t *testing.T, scenario string, dispatch func(*Runtime, context.Context, raptorImportKey) error) cleanupRaptorOutcome {
	t.Helper()
	public := newPublicRuntime(t)
	base := public.CPU
	r := &Runtime{CPU: base, Public: public}
	spy := &cleanupRaptorBackend{Backend: base, runtime: r}
	r.CPU, public.CPU = spy, spy
	for register := uint32(0); register < 13; register++ {
		check(t, base.WriteRegister(register, 0x100+register))
	}
	check(t, base.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel|1))
	check(t, public.Services.Advance(public.ServiceOwner, 137*time.Millisecond))
	key := raptorImportKey{Module: 507, Ordinal: 1210}
	switch scenario {
	case "body error":
		key.Ordinal = 1200
		check(t, base.WriteRegister(cpu.RegisterR0, 0xf0000000))
	case "capture error":
		spy.captureErr = errors.New("capture failed")
	case "return error":
		spy.returnErr = errors.New("return failed")
	case "return panic":
		spy.panicReturn = true
	case "audio recording panic":
		spy.panicRead = 2
	case "audio result read error":
		spy.errorRead = 2
	case "non audio":
		key = raptorImportKey{Module: 999, Ordinal: 999}
	case "audio unimplemented":
		key.Ordinal = 1234
	case "public audio":
		key.Module = 1
	case "reentrant return":
		spy.onReturn = func() {
			check(t, base.WriteRegister(cpu.RegisterR0, 0xabcdef01))
			check(t, base.WriteRegister(cpu.RegisterLR, guest.ReturnSentinel+0x20|1))
			err := dispatch(r, context.Background(), raptorImportKey{Module: 999, Ordinal: 998})
			spy.events = append(spy.events, fmt.Sprintf("nested:err=%v:depth=%d:audio=%d", err, r.hostCallDepth, r.AudioImportCalls))
			check(t, err)
		}
	}
	var outcome cleanupRaptorOutcome
	func() {
		defer func() {
			if p := recover(); p != nil {
				outcome.Panic = fmt.Sprint(p)
			}
		}()
		if err := dispatch(r, context.Background(), key); err != nil {
			outcome.Err = err.Error()
		}
	}()
	outcome.Depth, outcome.Events = r.hostCallDepth, spy.events
	outcome.Imports, outcome.Audio, outcome.AudioCalls = r.ImportTrace, r.AudioImportTrace, r.AudioImportCalls
	outcome.Stats, outcome.Observed, outcome.Unimplemented = public.Stats, public.Observed, public.Unimplemented
	var frame cpu.HostCallFrame
	check(t, cpu.CaptureHostCallFrame(base, &frame, cpu.HostCallFrameRequest{}))
	outcome.Registers = frame.Registers
	return outcome
}

func TestRaptorDispatchCleanupMatchesReference(t *testing.T) {
	for _, scenario := range []string{"audio", "body error", "capture error", "return error", "return panic", "audio recording panic", "audio result read error", "non audio", "audio unimplemented", "public audio", "reentrant return"} {
		t.Run(scenario, func(t *testing.T) {
			want := runCleanupRaptorCase(t, scenario, referenceCleanupDispatchImport)
			got := runCleanupRaptorCase(t, scenario, (*Runtime).dispatchImport)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("dispatch cleanup mismatch\ngot  %+v\nwant %+v", got, want)
			}
			if got.Depth != 0 {
				t.Fatalf("frame leak after return/error/panic: depth %d", got.Depth)
			}
			if scenario == "capture error" || scenario == "non audio" || scenario == "audio recording panic" {
				if got.AudioCalls != 0 || len(got.Audio) != 0 {
					t.Fatalf("unexpected audio recording: %+v", got)
				}
			} else {
				if got.AudioCalls != 1 || len(got.Audio) != 1 || got.Audio[0].GuestNS != int64(137*time.Millisecond) {
					t.Fatalf("audio recording count/time = %+v", got)
				}
				wantFault := scenario == "body error" || scenario == "return error" || scenario == "audio result read error"
				if got.Audio[0].Fault != wantFault {
					t.Fatalf("audio fault=%t, want %t", got.Audio[0].Fault, wantFault)
				}
				if got.Events[len(got.Events)-1] != "read R0:depth=1:audio=0" {
					t.Fatalf("audio recording did not precede frame pop: %v", got.Events)
				}
			}
			if scenario == "reentrant return" && (len(got.Imports) != 2 || got.Audio[0].Args[0] != 0x100 || got.Audio[0].LR != guest.ReturnSentinel|1) {
				t.Fatalf("nested return clobbered original audio arguments: %+v", got)
			}
			switch scenario {
			case "capture error":
				if got.Err != "capture failed" || len(got.Imports) != 0 {
					t.Fatalf("capture error = %+v", got)
				}
			case "return error":
				if got.Err != "return failed" {
					t.Fatalf("named dispatch error = %q", got.Err)
				}
			case "return panic", "audio recording panic":
				if got.Panic != scenario {
					t.Fatalf("panic = %q, want %q", got.Panic, scenario)
				}
			}
		})
	}
}
