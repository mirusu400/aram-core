package raptor

import (
	"context"
	"errors"
	"reflect"
	"testing"

	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	"github.com/mirusu400/aram-core/cpu"
)

func TestRaptorJavaMethodPlanRebuildsDerivedMetadata(t *testing.T) {
	method := (raptorJavaMethod{className: "java/lang/String", Name: "charAt", descriptor: "(I)C"}).compile()
	if method.compiled.argumentCount != 2 || method.compiled.returnsReference || method.compiled.handler == nil {
		t.Fatalf("bad instance plan: %+v", method.compiled)
	}
	if method.compile().compiled != method.compiled {
		t.Fatal("unchanged method discarded its plan")
	}
	state := javaMethodToState(method)
	restored := state.runtimeMethod()
	if restored.compiled == nil || restored.compiled == method.compiled || javaMethodToState(restored) != state {
		t.Fatal("save/restore did not rebuild a non-semantic method plan")
	}
	changed := method
	changed.descriptor, changed.isStatic = "(J[BLjava/lang/Object;)Ljava/lang/String;", true
	changed = changed.compile()
	if changed.compiled == method.compiled || changed.compiled.argumentCount != 4 || !changed.compiled.returnsReference {
		t.Fatalf("descriptor/static change reused stale metadata: %+v", changed.compiled)
	}
}

func TestRaptorJavaArgumentsUseNestedFrameAndKeepStackFaultLazy(t *testing.T) {
	public := newPublicRuntime(t)
	runtime := &Runtime{CPU: public.CPU, Public: public}
	for index := uint32(0); index < 4; index++ {
		check(t, runtime.CPU.WriteRegister(index, 10+index))
	}
	stack, err := runtime.CPU.ReadRegister(cpu.RegisterSP)
	check(t, err)
	check(t, public.WriteU32(stack, 100))
	check(t, public.WriteU32(stack+4, 101))
	var arguments [6]uint32
	check(t, runtime.readRaptorJavaArgumentsInto(arguments[:]))
	if want := [6]uint32{10, 11, 12, 13, 100, 101}; arguments != want {
		t.Fatalf("standalone arguments = %v, want %v", arguments, want)
	}
	outer, err := runtime.pushHostCallFrame()
	check(t, err)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, 20))
	inner, err := runtime.pushHostCallFrame()
	check(t, err)
	check(t, runtime.readRaptorJavaArgumentsInto(arguments[:]))
	if arguments[0] != 20 {
		t.Fatal("nested call did not select its own frame")
	}
	runtime.popHostCallFrame(inner)
	check(t, runtime.readRaptorJavaArgumentsInto(arguments[:]))
	if arguments[0] != 10 {
		t.Fatal("nested call overwrote outer frame arguments")
	}
	runtime.popHostCallFrame(outer)
	check(t, runtime.CPU.WriteRegister(cpu.RegisterSP, 0xdeadbeec))
	check(t, runtime.readRaptorJavaArgumentsInto(arguments[:4]))
	if err := runtime.readRaptorJavaArgumentsInto(arguments[:5]); !errors.Is(err, cpu.ErrInvalidAddress) {
		t.Fatalf("requested stack argument lost address fault: %v", err)
	}
}

func TestRaptorJavaCachedAndFreshHandlersPreserveCallsAndTrace(t *testing.T) {
	for _, mode := range []ktfrt.KTFTraceMode{ktfrt.KTFTraceOff, ktfrt.KTFTraceCounters, ktfrt.KTFTraceSampled, ktfrt.KTFTraceFull} {
		t.Run(mode.String(), func(t *testing.T) {
			type outcome struct {
				values []uint32
				trace  []string
				drops  int
			}
			run := func(cached bool) outcome {
				public := newPublicRuntime(t)
				runtime := &Runtime{CPU: public.CPU, Public: public,
					resolvedImports: make(map[raptorImportKey]uint64),
					importSlotByKey: make(map[raptorImportKey]uint32)}
				java, err := runtime.ensureJavaRuntime()
				check(t, err)
				check(t, java.Host.SetTraceMode(mode))
				text, err := runtime.NewRaptorJavaString("가나다A")
				check(t, err)
				method := raptorJavaMethod{className: "java/lang/String", Name: "charAt", descriptor: "(I)C"}
				if cached {
					method = method.compile()
				}
				result := outcome{}
				for index := 0; index < ktfrt.HostTraceSampleInterval+2; index++ {
					check(t, runtime.CPU.WriteRegister(cpu.RegisterR0, text))
					check(t, runtime.CPU.WriteRegister(cpu.RegisterR1, uint32(index%4)))
					value, err := runtime.callJavaHostMethod(context.Background(), method)
					check(t, err)
					result.values = append(result.values, value.Low)
				}
				result.trace = append([]string(nil), java.Host.HostTrace...)
				result.drops = java.Host.HostTraceDropped
				return result
			}
			if cached, fresh := run(true), run(false); !reflect.DeepEqual(cached, fresh) {
				t.Fatal("derived handler cache changed returned values or detailed trace")
			}
		})
	}
}

func BenchmarkRaptorJavaHostCallPlan(b *testing.B) {
	for _, cached := range []bool{false, true} {
		name := "fresh"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			public := newPublicRuntime(b)
			runtime := &Runtime{CPU: public.CPU, Public: public,
				resolvedImports: make(map[raptorImportKey]uint64),
				importSlotByKey: make(map[raptorImportKey]uint32)}
			java, err := runtime.ensureJavaRuntime()
			check(b, err)
			check(b, java.Host.SetTraceMode(ktfrt.KTFTraceCounters))
			text, err := runtime.NewRaptorJavaString("가나다A")
			check(b, err)
			check(b, runtime.CPU.WriteRegister(cpu.RegisterR0, text))
			check(b, runtime.CPU.WriteRegister(cpu.RegisterR1, 2))
			method := raptorJavaMethod{className: "java/lang/String", Name: "charAt", descriptor: "(I)C"}
			if cached {
				method = method.compile()
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				value, err := runtime.callJavaHostMethod(context.Background(), method)
				if err != nil || value.Low != '다' {
					b.Fatalf("charAt = %x, %v", value.Low, err)
				}
			}
		})
	}
}
