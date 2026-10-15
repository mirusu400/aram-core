package ktf

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"reflect"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// referenceCleanupHostJavaMethod is the pre-extraction boundary. Keep the
// ordering and named returns independent of HostJavaMethod: the unchanged
// dispatch body has its own handler tests, while this oracle pins its cleanup.
func referenceCleanupHostJavaMethod(className, name, descriptor string) ktfHostHandler {
	sampleDetailedTrace := isKTFHighFrequencyJavaMethod(className, name, descriptor)
	var detailedTraceCalls uint64
	declaredClassName := className
	declaresHostMethod := ktfHostSpecDeclaresJavaMethod(className, name, descriptor)
	argumentWords := uint32(4)
	if parameters, ok := ktfJavaParameterWords(descriptor); ok {
		argumentWords = uint32(min(int(cpu.RegisterR12+1), max(4, parameters+2)))
	}
	return func(ctx context.Context, runtime *Runtime) (value uint32, returnedErr error) {
		className := declaredClassName
		scope, ownedScope, err := runtime.acquireHostCallScope(argumentWords)
		if err != nil {
			return 0, err
		}
		if ownedScope {
			defer runtime.popHostCallScope(scope)
		}
		runtime.JavaReturnHigh = 0
		defer func() {
			if returnedErr != nil || !strings.HasSuffix(descriptor, ")J") || runtime.NativeParameterBase != 0 {
				return
			}
			var commit cpu.RegisterCommit
			_ = commit.Set(cpu.RegisterR1, runtime.JavaReturnHigh)
			if err := cpu.CommitHostCallRegisters(runtime.CPU, commit); err != nil {
				value = 0
				returnedErr = err
			}
		}()
		registers := scope.arguments[:cpu.RegisterR12+1]
		declaredClass := className
		runtime.inspectMemo.begin()
		defer runtime.inspectMemo.reset()
		exactHostReceiver := false
		if declaresHostMethod && !strings.HasPrefix(name, "<") && registers[1] != 0 {
			declaredAddress := runtime.JavaClasses[className]
			if runtime.hostJavaClass[declaredAddress] {
				receiverClass, receiverErr := runtime.readHostJavaReceiverClass(registers[1])
				exactHostReceiver = receiverErr == nil && receiverClass == declaredAddress
			}
		}
		if !exactHostReceiver {
			className = runtime.correctHostJavaReceiverClass(className, name, descriptor, registers)
		}
		if className != declaredClass {
			runtime.tracef("java_host_receiver_correct:%s.%s%s->%s", declaredClass, name, descriptor, className)
		}
		runtime.LastJavaCallLR = scope.frame.Registers[cpu.RegisterLR]
		if runtime.traceMode == KTFTraceFull {
			if sampleDetailedTrace {
				detailedTraceCalls++
				if detailedTraceCalls == 1 || detailedTraceCalls%HostTraceSampleInterval == 0 {
					runtime.traceJavaMethodCall(className, name, descriptor, runtime.LastJavaCallLR, registers)
				} else {
					runtime.omitTrace()
				}
			} else {
				runtime.traceJavaMethodCall(className, name, descriptor, runtime.LastJavaCallLR, registers)
			}
		}
		if !exactHostReceiver {
			if value, ok, err := runtime.redispatchGuestJavaMethod(ctx, className, name, descriptor, registers); ok || err != nil {
				return value, err
			}
		}
		runtime.inspectMemo.reset()
		return dispatchKTFHostJavaMethod(ctx, runtime, className, name, descriptor, registers)
	}
}

type cleanupKTFBackend struct {
	cpu.Backend
	runtime     *Runtime
	captureErr  error
	commitErr   error
	panicCommit bool
	panicReadAt uint32
	onCommit    func()
	events      []string
	commits     []cpu.RegisterCommit
}

func (b *cleanupKTFBackend) CaptureHostCallFrame(frame *cpu.HostCallFrame, request cpu.HostCallFrameRequest) error {
	if b.captureErr != nil {
		return b.captureErr
	}
	return cpu.CaptureHostCallFrame(b.Backend, frame, request)
}

func (b *cleanupKTFBackend) CommitHostCallRegisters(commit cpu.RegisterCommit) error {
	b.events = append(b.events, fmt.Sprintf("commit:depth=%d:memo=%t", b.runtime.hostCallDepth, b.runtime.inspectMemo.open))
	b.commits = append(b.commits, commit)
	if b.onCommit != nil {
		callback := b.onCommit
		b.onCommit = nil
		callback()
	}
	if b.panicCommit {
		panic("commit panic")
	}
	if b.commitErr != nil {
		return b.commitErr
	}
	return cpu.CommitHostCallRegisters(b.Backend, commit)
}

func (b *cleanupKTFBackend) ReadMemory(address uint32, destination []byte) error {
	if address != 0 && address == b.panicReadAt {
		b.events = append(b.events, fmt.Sprintf("read panic:depth=%d:memo=%t", b.runtime.hostCallDepth, b.runtime.inspectMemo.open))
		panic("resolution panic")
	}
	return b.Backend.ReadMemory(address, destination)
}

type cleanupKTFOutcome struct {
	Value     uint32
	Err       string
	Panic     string
	Depth     int
	Memo      bool
	High      uint32
	LastLR    uint32
	Registers [17]uint32
	Events    []string
	Commits   []cpu.RegisterCommit
	Trace     []string
}

func runCleanupKTFCase(t *testing.T, scenario string, factory func(string, string, string) ktfHostHandler) cleanupKTFOutcome {
	t.Helper()
	r := newTestRuntime(t)
	base := r.CPU
	spy := &cleanupKTFBackend{Backend: base, runtime: r}
	r.CPU = spy
	for register := uint32(0); register < 13; register++ {
		check(t, base.WriteRegister(register, 0x100+register))
	}
	check(t, base.WriteRegister(cpu.RegisterR1, 0x76543211))
	check(t, base.WriteRegister(cpu.RegisterR2, 0xfedcba98))
	check(t, base.WriteRegister(cpu.RegisterLR, 0x12345679))
	r.JavaReturnHigh = 0xdeadbeef
	r.traceMode = KTFTraceFull
	name, descriptor := "abs", "(J)J"
	var borrowed *ktfHostCallScope
	switch scenario {
	case "int":
		descriptor = "(I)I"
		check(t, base.WriteRegister(cpu.RegisterR1, ^uint32(6)))
	case "body error":
		name, descriptor = "missing", "()J"
	case "native":
		r.NativeParameterBase = allocWords(t, r, 3)
		check(t, r.writeWords(r.NativeParameterBase, []uint32{0x76543211, 0xfedcba98, 0}))
	case "borrowed scope":
		var err error
		borrowed, err = r.pushHostCallScope(4)
		check(t, err)
	case "commit error":
		spy.commitErr = errors.New("commit failed")
	case "commit panic":
		spy.panicCommit = true
	case "resolution panic":
		class := newInspectableJavaClass(t, r, "java/lang/Math")
		r.JavaClasses["java/lang/Math"] = class
		r.hostJavaClass[class] = true
		spy.panicReadAt = class
	case "capture error":
		spy.captureErr = errors.New("capture failed")
		r.inspectMemo.begin()
	case "reentrant commit":
		spy.onCommit = func() {
			check(t, base.WriteRegister(cpu.RegisterR1, ^uint32(6)))
			value, err := r.invokeHostHandler(context.Background(), ktfHostCall{handler: factory("java/lang/Math", "abs", "(I)I")})
			spy.events = append(spy.events, fmt.Sprintf("nested:value=%d:err=%v:depth=%d:memo=%t", value, err, r.hostCallDepth, r.inspectMemo.open))
			if err != nil || value != 7 {
				t.Fatalf("nested abs = %d, %v", value, err)
			}
		}
	}
	var outcome cleanupKTFOutcome
	func() {
		defer func() {
			if p := recover(); p != nil {
				outcome.Panic = fmt.Sprint(p)
			}
		}()
		var err error
		outcome.Value, err = factory("java/lang/Math", name, descriptor)(context.Background(), r)
		if err != nil {
			outcome.Err = err.Error()
		}
	}()
	outcome.Depth, outcome.Memo = r.hostCallDepth, r.inspectMemo.open
	outcome.High, outcome.LastLR = r.JavaReturnHigh, r.LastJavaCallLR
	outcome.Events, outcome.Commits, outcome.Trace = spy.events, spy.commits, r.HostTrace
	var frame cpu.HostCallFrame
	check(t, cpu.CaptureHostCallFrame(base, &frame, cpu.HostCallFrameRequest{}))
	outcome.Registers = frame.Registers
	if borrowed != nil {
		r.popHostCallScope(borrowed)
	}
	return outcome
}

func TestKTFJavaCleanupMatchesReference(t *testing.T) {
	for _, scenario := range []string{"long", "int", "body error", "native", "borrowed scope", "commit error", "commit panic", "resolution panic", "capture error", "reentrant commit"} {
		t.Run(scenario, func(t *testing.T) {
			want := runCleanupKTFCase(t, scenario, referenceCleanupHostJavaMethod)
			got := runCleanupKTFCase(t, scenario, HostJavaMethod)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("cleanup mismatch\ngot  %+v\nwant %+v", got, want)
			}
			depth := 0
			if scenario == "borrowed scope" {
				depth = 1
			}
			if got.Depth != depth || got.Memo != (scenario == "capture error") {
				t.Fatalf("cleanup depth=%d memo=%t", got.Depth, got.Memo)
			}
			for _, event := range got.Events {
				if strings.HasPrefix(event, "commit:") && event != "commit:depth=1:memo=false" {
					t.Fatalf("R1 writeback did not follow memo reset and precede scope pop: %s", event)
				}
			}
			switch scenario {
			case "long", "borrowed scope", "reentrant commit":
				if got.Value != 0x89abcdef || got.Registers[cpu.RegisterR1] != 0x01234567 || got.Err != "" || got.Panic != "" {
					t.Fatalf("long return = %+v", got)
				}
			case "int":
				if got.Value != 7 || len(got.Commits) != 0 {
					t.Fatalf("int return = %+v", got)
				}
			case "native":
				if got.Value != 0x89abcdef || got.High != 0x01234567 || len(got.Commits) != 0 || got.Registers[cpu.RegisterR1] != 0x76543211 {
					t.Fatalf("native long return = %+v", got)
				}
			case "commit error":
				if got.Value != 0 || got.Err != "commit failed" {
					t.Fatalf("writeback error did not override named result: %+v", got)
				}
			case "body error":
				if got.Err == "" || len(got.Commits) != 0 {
					t.Fatalf("body error performed R1 writeback: %+v", got)
				}
			case "resolution panic":
				if got.Panic != "resolution panic" || len(got.Commits) != 1 || got.Registers[cpu.RegisterR1] != 0 {
					t.Fatalf("panic cleanup changed original zero-high writeback: %+v", got)
				}
			case "commit panic":
				if got.Panic != "commit panic" {
					t.Fatalf("panic = %q", got.Panic)
				}
			case "capture error":
				if got.Err != "capture failed" || got.High != 0xdeadbeef || len(got.Commits) != 0 {
					t.Fatalf("capture error changed pre-boundary state: %+v", got)
				}
			}
		})
	}
}

func TestKTFJavaCleanupDetailedTraceCounterLifetime(t *testing.T) {
	type result struct {
		Trace   []string
		Dropped int
		Pixels  []byte
		LastLR  uint32
	}
	run := func(factory func(string, string, string) ktfHostHandler) result {
		r := newTestRuntime(t)
		r.JvmContext = allocWords(t, r, 3+128)
		const className = "org/kwis/msp/lcdui/Graphics"
		object := newHostObject(t, r, className)
		pixels := image.NewRGBA(image.Rect(0, 0, 2, 2))
		r.Graphics[object] = &ktfGraphics{Target: pixels, clip: pixels.Bounds(), color: color.RGBA{R: 37, G: 51, B: 82, A: 255}}
		check(t, r.CPU.WriteRegister(cpu.RegisterR1, object))
		check(t, r.CPU.WriteRegister(cpu.RegisterR2, 1))
		check(t, r.CPU.WriteRegister(cpu.RegisterR3, 1))
		check(t, r.CPU.WriteRegister(cpu.RegisterLR, 0x12345679))
		r.HostTrace = nil
		r.HostTraceDropped = 0
		call := factory(className, "setPixel", "(II)V")
		for index := 0; index < 2*HostTraceSampleInterval+3; index++ {
			r.traceMode = KTFTraceFull
			_, err := call(context.Background(), r)
			check(t, err)
			r.traceMode = KTFTraceOff
			_, err = call(context.Background(), r)
			check(t, err)
		}
		r.traceMode = KTFTraceFull
		_, err := factory(className, "setPixel", "(II)V")(context.Background(), r)
		check(t, err)
		if r.hostCallDepth != 0 || r.inspectMemo.open {
			t.Fatal("graphics call leaked its boundary")
		}
		return result{r.HostTrace, r.HostTraceDropped, pixels.Pix, r.LastJavaCallLR}
	}
	want, got := run(referenceCleanupHostJavaMethod), run(HostJavaMethod)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("trace counter mismatch\ngot  %+v\nwant %+v", got, want)
	}
	if len(got.Trace) != 4 || got.Dropped != 2*HostTraceSampleInterval || got.LastLR != 0x12345679 {
		t.Fatalf("sampling/counter lifetime changed: %+v", got)
	}
}
