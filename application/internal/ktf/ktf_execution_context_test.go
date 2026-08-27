package ktf

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
	ktfloader "github.com/mirusu400/aram-core/loader/ktf"
)

type serializedContextBackend struct {
	cpu.Backend
}

func newBudgetTaskRuntime(
	t testing.TB,
	backend cpu.Backend,
	taskCount int,
) *Runtime {
	t.Helper()
	runtime, err := NewRuntime(backend, ktfloader.Package{
		ClientName: "client.bin0",
		Client: []byte{
			0x01, 0x30, // adds r0, #1
			0xfd, 0xe7, // b 0x0000
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	for index := range taskCount {
		task, err := runtime.NewTask(
			ImageBase|1,
			[]uint32{uint32(index * 1000)},
			index,
		)
		if err != nil {
			t.Fatal(err)
		}
		runtime.Tasks = append(runtime.Tasks, task)
	}
	return runtime
}

func TestKTFTaskSlicesKeepWarmJITBlocks(t *testing.T) {
	backend := interpreter.NewJIT()
	defer backend.Close()
	runtime := newBudgetTaskRuntime(t, backend, 1)

	before := backend.ExecutionStatistics()
	first := runtime.RunTaskSlice(t.Context(), 64)
	if first.Err != nil || first.Reason != cpu.StopBudget {
		t.Fatalf("first task slice = %+v", first)
	}
	warm := backend.ExecutionStatistics()
	if warm.TranslatedBlocks <= before.TranslatedBlocks {
		t.Fatalf("first slice did not warm a JIT block: before %+v after %+v", before, warm)
	}
	second := runtime.RunTaskSlice(t.Context(), 64)
	if second.Err != nil || second.Reason != cpu.StopBudget {
		t.Fatalf("second task slice = %+v", second)
	}
	after := backend.ExecutionStatistics()
	if after.TranslationInvalidations != before.TranslationInvalidations {
		t.Fatalf("task slices invalidated translations: before %+v after %+v", before, after)
	}
	if after.TranslatedBlocks != warm.TranslatedBlocks {
		t.Fatalf("warm second slice retranslated blocks: warm %+v after %+v", warm, after)
	}
	task := runtime.Tasks[0]
	if task.Instructions() != 128 || task.Slices() != 2 || task.Yields() != 2 ||
		task.LastYieldReason() != "budget" {
		t.Fatalf(
			"task telemetry instructions=%d slices=%d yields=%d reason=%q",
			task.Instructions(),
			task.Slices(),
			task.Yields(),
			task.LastYieldReason(),
		)
	}
}

func TestKTFExecutionContextsKeepTaskRegistersIndependent(t *testing.T) {
	backend := interpreter.NewJIT()
	defer backend.Close()
	runtime := newBudgetTaskRuntime(t, backend, 2)
	before := backend.ExecutionStatistics()
	for range 4 {
		result := runtime.RunTaskSlice(t.Context(), 4)
		if result.Err != nil || result.Reason != cpu.StopBudget {
			t.Fatalf("task slice = %+v", result)
		}
	}
	for index, task := range runtime.Tasks {
		if err := runtime.restoreTaskContext(task); err != nil {
			t.Fatal(err)
		}
		value, err := runtime.CPU.ReadRegister(cpu.RegisterR0)
		if err != nil {
			t.Fatal(err)
		}
		want := uint32(index*1000 + 4)
		if value != want {
			t.Fatalf("task %d r0 = %d, want %d", index, value, want)
		}
	}
	after := backend.ExecutionStatistics()
	if after.TranslationInvalidations != before.TranslationInvalidations {
		t.Fatalf("multi-task switches invalidated translations: before %+v after %+v", before, after)
	}
}

func TestKTFTaskSchedulerFallsBackToPortableContexts(t *testing.T) {
	backend := interpreter.NewJIT()
	defer backend.Close()
	runtime := newBudgetTaskRuntime(
		t,
		serializedContextBackend{Backend: backend},
		1,
	)
	before := backend.ExecutionStatistics()
	for range 2 {
		result := runtime.RunTaskSlice(t.Context(), 8)
		if result.Err != nil || result.Reason != cpu.StopBudget {
			t.Fatalf("serialized task slice = %+v", result)
		}
	}
	after := backend.ExecutionStatistics()
	if after.SerializedContextRestores-before.SerializedContextRestores != 2 {
		t.Fatalf("serialized fallback restore counters: before %+v after %+v", before, after)
	}
	if after.TranslationInvalidations <= before.TranslationInvalidations {
		t.Fatalf("serialized fallback did not exercise cache invalidation: before %+v after %+v", before, after)
	}
}

func TestKTFHostYieldKeepsWarmJITBlocks(t *testing.T) {
	runtime := newYieldTaskRuntime(t, 1)
	backend := runtime.CPU.(*interpreter.Backend)
	for range 2 {
		if result := runtime.RunTaskSlice(t.Context(), 64); result.Err != nil {
			t.Fatal(result.Err)
		}
	}
	warm := backend.ExecutionStatistics()
	for range 128 {
		result := runtime.RunTaskSlice(t.Context(), 64)
		if result.Err != nil || result.Reason != cpu.StopBudget {
			t.Fatalf("host-yield task slice = %+v", result)
		}
	}
	after := backend.ExecutionStatistics()
	if after.TranslationInvalidations != warm.TranslationInvalidations ||
		after.TranslatedBlocks != warm.TranslatedBlocks {
		t.Fatalf("host yield cooled translations: warm %+v after %+v", warm, after)
	}
}

func TestKTFHostCallScopesAreReentrant(t *testing.T) {
	backend := interpreter.New()
	defer backend.Close()
	runtime := newBudgetTaskRuntime(t, backend, 0)
	outerStack := guest.DefaultStackBase + 0x100
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 10); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, outerStack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(outerStack, []uint32{14, 15}); err != nil {
		t.Fatal(err)
	}
	outer, err := runtime.pushHostCallScope(6)
	if err != nil {
		t.Fatal(err)
	}
	if outer.arguments[0] != 10 || outer.arguments[4] != 14 || outer.arguments[5] != 15 {
		t.Fatalf("outer host-call arguments = %v", outer.arguments[:6])
	}

	innerStack := guest.DefaultStackBase + 0x200
	if err := runtime.CPU.WriteRegister(cpu.RegisterR0, 20); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CPU.WriteRegister(cpu.RegisterSP, innerStack); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeWords(innerStack, []uint32{24, 25}); err != nil {
		t.Fatal(err)
	}
	inner, err := runtime.pushHostCallScope(6)
	if err != nil {
		t.Fatal(err)
	}
	if inner.arguments[0] != 20 || inner.arguments[4] != 24 || inner.arguments[5] != 25 {
		t.Fatalf("inner host-call arguments = %v", inner.arguments[:6])
	}
	runtime.popHostCallScope(inner)
	if got, err := runtime.parameter(4); err != nil || got != 14 {
		t.Fatalf("outer argument after nested return = %d, %v; want 14", got, err)
	}
	if outer.arguments[0] != 10 || outer.arguments[5] != 15 {
		t.Fatalf("nested call corrupted outer arguments = %v", outer.arguments[:6])
	}
	runtime.popHostCallScope(outer)
}

func newYieldTaskRuntime(t testing.TB, taskCount int) *Runtime {
	t.Helper()
	backend := interpreter.NewJIT()
	t.Cleanup(func() { _ = backend.Close() })
	runtime, err := NewRuntime(backend, ktfloader.Package{
		ClientName: "client.bin0",
		Client: []byte{
			0x01, 0x4b, // ldr r3, [pc, #4] -> literal at +8
			0x98, 0x47, // blx r3
			0xfc, 0xe7, // b 0x0000
			0x00, 0xbf, // alignment nop
			0x00, 0x00, 0x00, 0x00,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.MapImageAndHost(); err != nil {
		t.Fatal(err)
	}
	yield := runtime.RegisterHostCall(
		"benchmark.yield",
		func(_ context.Context, current *Runtime) (uint32, error) {
			current.yieldRequested = true
			return 0, nil
		},
	)
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], yield)
	if err := runtime.CPU.WriteMemory(ImageBase+8, encoded[:]); err != nil {
		t.Fatal(err)
	}
	for index := range taskCount {
		task, err := runtime.NewTask(ImageBase|1, nil, index)
		if err != nil {
			t.Fatal(err)
		}
		runtime.Tasks = append(runtime.Tasks, task)
	}
	return runtime
}

func BenchmarkKTFYieldingTaskSwitch(b *testing.B) {
	for _, taskCount := range []int{1, 2, 4} {
		b.Run(string(rune('0'+taskCount))+"_tasks", func(b *testing.B) {
			runtime := newYieldTaskRuntime(b, taskCount)
			benchmarkContext := context.Background()
			for range taskCount {
				if result := runtime.RunTaskSlice(benchmarkContext, 64); result.Err != nil {
					b.Fatal(result.Err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				result := runtime.RunTaskSlice(benchmarkContext, 64)
				if result.Err != nil || result.Reason != cpu.StopBudget {
					b.Fatalf("task slice = %+v", result)
				}
			}
		})
	}
}

func BenchmarkKTFWarmBudgetSlice(b *testing.B) {
	backend := interpreter.NewJIT()
	b.Cleanup(func() { _ = backend.Close() })
	runtime := newBudgetTaskRuntime(b, backend, 1)
	if result := runtime.RunTaskSlice(context.Background(), 512); result.Err != nil {
		b.Fatal(result.Err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result := runtime.RunTaskSlice(context.Background(), 512)
		if result.Err != nil || result.Reason != cpu.StopBudget {
			b.Fatalf("task slice = %+v", result)
		}
	}
}
