package interpreter

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/mirusu400/aram-core/cpu"
)

const countedLoopTestAddress = uint32(0x1000)

type countedLoopTestProgram struct {
	name             string
	mode             cpu.Mode
	code             []byte
	instructionWidth uint32
}

func countedLoopTestPrograms() []countedLoopTestProgram {
	return []countedLoopTestProgram{
		{
			name:             "thumb",
			mode:             cpu.ModeThumb,
			instructionWidth: 2,
			code: thumbTestCode(
				0x3801, // SUBS r0, #1
				0xd1fd, // BNE 0x1000
				0x212a, // MOVS r1, #42
				0xbe00, // BKPT
			),
		},
		{
			name:             "arm",
			mode:             cpu.ModeARM,
			instructionWidth: 4,
			code: armTestCode(
				0xe2500001, // SUBS r0, r0, #1
				0x1afffffd, // BNE 0x1000
				0xe3a0102a, // MOV r1, #42
				0xe1200070, // BKPT
			),
		},
	}
}

func thumbTestCode(instructions ...uint16) []byte {
	code := make([]byte, len(instructions)*2)
	for index, instruction := range instructions {
		binary.LittleEndian.PutUint16(code[index*2:], instruction)
	}
	return code
}

func armTestCode(instructions ...uint32) []byte {
	code := make([]byte, len(instructions)*4)
	for index, instruction := range instructions {
		binary.LittleEndian.PutUint32(code[index*4:], instruction)
	}
	return code
}

func newLoopAccelerationBackend() *Backend {
	return NewJITWithOptions(JITOptions{LoopAcceleration: true})
}

func configureApplicationCountedLoop(
	t *testing.T,
	newBackend func() *Backend,
	program countedLoopTestProgram,
	counter uint32,
) *Backend {
	t.Helper()
	backend := newBackend()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(
		countedLoopTestAddress,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteMemory(countedLoopTestAddress, program.code); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, counter); err != nil {
		t.Fatal(err)
	}
	status := flagC | flagV | uint32(processorModeSystem)
	if program.mode == cpu.ModeThumb {
		status |= cpu.StatusThumb
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, status); err != nil {
		t.Fatal(err)
	}
	return backend
}

func configureSystemCountedLoop(
	t *testing.T,
	newBackend func() *Backend,
	program countedLoopTestProgram,
	counter, status uint32,
) (*Backend, *testSystemBus) {
	t.Helper()
	bus := &testSystemBus{memory: make(map[uint32]byte)}
	bus.writeRaw(countedLoopTestAddress, program.code)
	backend := newBackend()
	t.Cleanup(func() { _ = backend.Close() })
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, counter); err != nil {
		t.Fatal(err)
	}
	if program.mode == cpu.ModeThumb {
		status |= cpu.StatusThumb
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, status); err != nil {
		t.Fatal(err)
	}
	return backend, bus
}

func assertLoopBackendParity(
	t *testing.T,
	exact, accelerated *Backend,
	exactResult, acceleratedResult cpu.Result,
) {
	t.Helper()
	if exactResult.Reason != acceleratedResult.Reason ||
		exactResult.Instructions != acceleratedResult.Instructions ||
		exactResult.PC != acceleratedResult.PC ||
		fmt.Sprint(exactResult.Err) != fmt.Sprint(acceleratedResult.Err) {
		t.Fatalf("result mismatch:\n exact: %+v\n fast:  %+v", exactResult, acceleratedResult)
	}
	for registerID := uint32(0); registerID <= cpu.RegisterCPSR; registerID++ {
		exactValue := register(t, exact, registerID)
		acceleratedValue := register(t, accelerated, registerID)
		if exactValue != acceleratedValue {
			t.Fatalf(
				"register %d mismatch: exact=%#08x accelerated=%#08x",
				registerID, exactValue, acceleratedValue,
			)
		}
	}
}

func TestJITLoopAccelerationIsExplicitAndObservable(t *testing.T) {
	exact := NewJIT()
	t.Cleanup(func() { _ = exact.Close() })
	configuredOff := NewJITWithOptions(JITOptions{})
	t.Cleanup(func() { _ = configuredOff.Close() })
	accelerated := newLoopAccelerationBackend()
	t.Cleanup(func() { _ = accelerated.Close() })

	if got := exact.Identity().Name; got != BackendName+"-jit" {
		t.Fatalf("NewJIT identity = %q", got)
	}
	if got := configuredOff.Identity().Name; got != BackendName+"-jit" {
		t.Fatalf("zero-option JIT identity = %q", got)
	}
	if got := accelerated.Identity().Name; got != BackendName+"-jit-loops" {
		t.Fatalf("accelerated JIT identity = %q", got)
	}
}

func TestJITLoopAccelerationDifferentialBudgets(t *testing.T) {
	counters := []uint32{0, 1, 3, ^uint32(0)}
	budgets := []uint64{1, 2, 3, 4, 5, 6, 7, 8, 255, 256, 257}
	for _, program := range countedLoopTestPrograms() {
		for _, counter := range counters {
			for _, budget := range budgets {
				t.Run(fmt.Sprintf("%s/counter_%08x/budget_%d", program.name, counter, budget), func(t *testing.T) {
					exact := configureApplicationCountedLoop(t, NewJIT, program, counter)
					accelerated := configureApplicationCountedLoop(t, newLoopAccelerationBackend, program, counter)
					exactResult := exact.Run(context.Background(), countedLoopTestAddress, program.mode, budget)
					acceleratedResult := accelerated.Run(context.Background(), countedLoopTestAddress, program.mode, budget)
					assertLoopBackendParity(t, exact, accelerated, exactResult, acceleratedResult)
				})
			}
		}
	}
}

func TestJITLoopAccelerationClassifiesOnlyQualifiedShapes(t *testing.T) {
	tests := []struct {
		name string
		mode cpu.Mode
		code []byte
		want bool
	}{
		{"thumb_sub_immediate", cpu.ModeThumb, thumbTestCode(0x3801, 0xd1fd), true},
		{"thumb_sub_three_operand", cpu.ModeThumb, thumbTestCode(0x1e40, 0xd1fd), true},
		{"thumb_sub_two", cpu.ModeThumb, thumbTestCode(0x3802, 0xd1fd), false},
		{"thumb_source_differs", cpu.ModeThumb, thumbTestCode(0x1e48, 0xd1fd), false},
		{"thumb_wrong_condition", cpu.ModeThumb, thumbTestCode(0x3801, 0xd0fd), false},
		{"thumb_wrong_target", cpu.ModeThumb, thumbTestCode(0x3801, 0xd1fe), false},
		{"thumb_extra_body", cpu.ModeThumb, thumbTestCode(0x3200, 0x3801, 0xd1fc), false},
		{"arm_sub_immediate", cpu.ModeARM, armTestCode(0xe2500001, 0x1afffffd), true},
		{"arm_sub_two", cpu.ModeARM, armTestCode(0xe2500002, 0x1afffffd), false},
		{"arm_no_flags", cpu.ModeARM, armTestCode(0xe2400001, 0x1afffffd), false},
		{"arm_conditional_sub", cpu.ModeARM, armTestCode(0x12500001, 0x1afffffd), false},
		{"arm_source_differs", cpu.ModeARM, armTestCode(0xe2510001, 0x1afffffd), false},
		{"arm_link_register", cpu.ModeARM, armTestCode(0xe25ee001, 0x1afffffd), false},
		{"arm_linking_branch", cpu.ModeARM, armTestCode(0xe2500001, 0x1bfffffd), false},
		{"arm_wrong_target", cpu.ModeARM, armTestCode(0xe2500001, 0x1afffffe), false},
		{"arm_extra_body", cpu.ModeARM, armTestCode(0xe1a02002, 0xe2500001, 0x1afffffc), false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newLoopAccelerationBackend()
			t.Cleanup(func() { _ = backend.Close() })
			if err := backend.Map(
				countedLoopTestAddress,
				0x1000,
				cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
			); err != nil {
				t.Fatal(err)
			}
			if err := backend.WriteMemory(countedLoopTestAddress, test.code); err != nil {
				t.Fatal(err)
			}
			var block *jitBlock
			if test.mode == cpu.ModeThumb {
				block = backend.translateThumbBlock(countedLoopTestAddress)
			} else {
				block = backend.translateARMBlock(countedLoopTestAddress)
			}
			if block == nil {
				t.Fatal("loop did not translate")
			}
			if got := block.countedLoop != nil; got != test.want {
				t.Fatalf("counted-loop classification = %v, want %v", got, test.want)
			}
		})
	}
}

func TestJITLoopAccelerationStatisticsProveSkippedWork(t *testing.T) {
	for _, program := range countedLoopTestPrograms() {
		t.Run(program.name, func(t *testing.T) {
			exact := configureApplicationCountedLoop(t, NewJIT, program, 1000)
			accelerated := configureApplicationCountedLoop(t, newLoopAccelerationBackend, program, 1000)
			exactResult := exact.Run(context.Background(), countedLoopTestAddress, program.mode, runBatchInstructions)
			acceleratedResult := accelerated.Run(context.Background(), countedLoopTestAddress, program.mode, runBatchInstructions)
			assertLoopBackendParity(t, exact, accelerated, exactResult, acceleratedResult)
			if statistics := exact.ExecutionStatistics(); statistics.AcceleratedLoopIterations != 0 ||
				statistics.AcceleratedLoopInstructions != 0 {
				t.Fatalf("default JIT accelerated a loop: %+v", statistics)
			}
			statistics := accelerated.ExecutionStatistics()
			if statistics.AcceleratedLoopIterations != 127 ||
				statistics.AcceleratedLoopInstructions != 254 {
				t.Fatalf("acceleration statistics = %+v", statistics)
			}
			if got := register(t, accelerated, cpu.RegisterR0); got != 872 {
				t.Fatalf("counter after batch = %d, want 872", got)
			}
		})
	}
}

func TestJITLoopAccelerationDisabledByTracing(t *testing.T) {
	for _, program := range countedLoopTestPrograms() {
		t.Run(program.name, func(t *testing.T) {
			backend := configureApplicationCountedLoop(t, newLoopAccelerationBackend, program, 1000)
			if err := backend.SetPCHistoryLimit(runBatchInstructions); err != nil {
				t.Fatal(err)
			}
			result := backend.Run(context.Background(), countedLoopTestAddress, program.mode, runBatchInstructions)
			if result.Err != nil || result.Reason != cpu.StopBudget {
				t.Fatalf("traced run result = %+v", result)
			}
			statistics := backend.ExecutionStatistics()
			if statistics.AcceleratedLoopIterations != 0 || statistics.AcceleratedLoopInstructions != 0 {
				t.Fatalf("traced loop accelerated: %+v", statistics)
			}
			if history := backend.PCHistory(); len(history) != runBatchInstructions {
				t.Fatalf("PC history length = %d, want %d", len(history), runBatchInstructions)
			}
		})
	}
}

func TestJITLoopAccelerationDisabledByExecutionTraps(t *testing.T) {
	for _, program := range countedLoopTestPrograms() {
		t.Run(program.name, func(t *testing.T) {
			status := uint32(processorModeSystem) | statusIRQDisable | statusFIQDisable
			backend, _ := configureSystemCountedLoop(t, newLoopAccelerationBackend, program, 1000, status)
			branchAddress := countedLoopTestAddress + program.instructionWidth
			if err := backend.SetExecutionTraps([]cpu.ExecutionTrap{{
				Address: branchAddress,
				Mode:    program.mode,
			}}); err != nil {
				t.Fatal(err)
			}
			result := backend.Run(context.Background(), countedLoopTestAddress, program.mode, 64)
			if result.Err != nil || result.Reason != cpu.StopExecutionTrap ||
				result.Instructions != 1 || result.PC != branchAddress {
				t.Fatalf("execution-trap result = %+v", result)
			}
			if got := register(t, backend, cpu.RegisterR0); got != 999 {
				t.Fatalf("counter at trap = %d, want 999", got)
			}
			if statistics := backend.ExecutionStatistics(); statistics.AcceleratedLoopIterations != 0 {
				t.Fatalf("trapped loop accelerated: %+v", statistics)
			}
		})
	}
}

func TestJITLoopAccelerationRequiresBothInterruptMasks(t *testing.T) {
	for _, program := range countedLoopTestPrograms() {
		t.Run(program.name+"/both_masked", func(t *testing.T) {
			status := uint32(processorModeSystem) | statusIRQDisable | statusFIQDisable
			exact, _ := configureSystemCountedLoop(t, NewJIT, program, 1000, status)
			accelerated, _ := configureSystemCountedLoop(t, newLoopAccelerationBackend, program, 1000, status)
			if err := exact.SetInterruptLine(cpu.InterruptIRQ, true); err != nil {
				t.Fatal(err)
			}
			if err := accelerated.SetInterruptLine(cpu.InterruptIRQ, true); err != nil {
				t.Fatal(err)
			}
			exactResult := exact.Run(context.Background(), countedLoopTestAddress, program.mode, runBatchInstructions)
			acceleratedResult := accelerated.Run(context.Background(), countedLoopTestAddress, program.mode, runBatchInstructions)
			assertLoopBackendParity(t, exact, accelerated, exactResult, acceleratedResult)
			if statistics := accelerated.ExecutionStatistics(); statistics.AcceleratedLoopIterations == 0 {
				t.Fatalf("masked system loop was not accelerated: %+v", statistics)
			}
		})

		t.Run(program.name+"/fiq_unmasked", func(t *testing.T) {
			status := uint32(processorModeSystem) | statusIRQDisable
			backend, _ := configureSystemCountedLoop(t, newLoopAccelerationBackend, program, 1000, status)
			if err := backend.SetInterruptLine(cpu.InterruptIRQ, true); err != nil {
				t.Fatal(err)
			}
			result := backend.Run(context.Background(), countedLoopTestAddress, program.mode, runBatchInstructions)
			if result.Err != nil || result.Reason != cpu.StopBudget {
				t.Fatalf("run result = %+v", result)
			}
			if statistics := backend.ExecutionStatistics(); statistics.AcceleratedLoopIterations != 0 {
				t.Fatalf("loop accelerated with FIQ unmasked: %+v", statistics)
			}
		})
	}
}

type interruptOnFetchBus struct {
	testSystemBus
	backend *Backend
	trigger uint32
	once    sync.Once
	err     error
}

func (b *interruptOnFetchBus) Read(
	address uint32,
	destination []byte,
	permission cpu.Permissions,
) error {
	err := b.testSystemBus.Read(address, destination, permission)
	if err == nil && permission == cpu.PermissionExecute &&
		uint64(address) <= uint64(b.trigger) &&
		uint64(address)+uint64(len(destination)) > uint64(b.trigger) {
		b.once.Do(func() {
			b.err = b.backend.SetInterruptLine(cpu.InterruptIRQ, true)
		})
	}
	return err
}

func configureInterruptBoundaryLoop(
	t *testing.T,
	newBackend func() *Backend,
	program countedLoopTestProgram,
) (*Backend, *interruptOnFetchBus) {
	t.Helper()
	bus := &interruptOnFetchBus{
		testSystemBus: testSystemBus{memory: make(map[uint32]byte)},
		trigger:       countedLoopTestAddress + program.instructionWidth,
	}
	bus.writeRaw(countedLoopTestAddress, program.code)
	bus.writeU32(vectorIRQ, 0xe1200070) // BKPT in the IRQ vector
	backend := newBackend()
	t.Cleanup(func() { _ = backend.Close() })
	bus.backend = backend
	if err := backend.AttachSystemBus(bus); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 1000); err != nil {
		t.Fatal(err)
	}
	status := uint32(processorModeSystem)
	if program.mode == cpu.ModeThumb {
		status |= cpu.StatusThumb
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, status); err != nil {
		t.Fatal(err)
	}
	return backend, bus
}

func TestJITLoopAccelerationPreservesUnmaskedIRQBoundary(t *testing.T) {
	for _, program := range countedLoopTestPrograms() {
		t.Run(program.name, func(t *testing.T) {
			exact, exactBus := configureInterruptBoundaryLoop(t, NewJIT, program)
			accelerated, acceleratedBus := configureInterruptBoundaryLoop(t, newLoopAccelerationBackend, program)
			exactResult := exact.Run(context.Background(), countedLoopTestAddress, program.mode, 16)
			acceleratedResult := accelerated.Run(context.Background(), countedLoopTestAddress, program.mode, 16)
			if exactBus.err != nil || acceleratedBus.err != nil {
				t.Fatalf("assert IRQ: exact=%v accelerated=%v", exactBus.err, acceleratedBus.err)
			}
			assertLoopBackendParity(t, exact, accelerated, exactResult, acceleratedResult)
			if acceleratedResult.Reason != cpu.StopBreakpoint || acceleratedResult.Instructions != 2 ||
				register(t, accelerated, cpu.RegisterR0) != 999 {
				t.Fatalf("interrupt boundary result = %+v r0=%d", acceleratedResult, register(t, accelerated, cpu.RegisterR0))
			}
			if statistics := accelerated.ExecutionStatistics(); statistics.AcceleratedLoopIterations != 0 {
				t.Fatalf("unmasked-interrupt loop accelerated: %+v", statistics)
			}
		})
	}
}

func TestJITLoopAccelerationHonorsCanceledContext(t *testing.T) {
	for _, program := range countedLoopTestPrograms() {
		t.Run(program.name, func(t *testing.T) {
			backend := configureApplicationCountedLoop(t, newLoopAccelerationBackend, program, 1000)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			result := backend.Run(ctx, countedLoopTestAddress, program.mode, 1024)
			if result.Reason != cpu.StopRequested || result.Instructions != 0 ||
				result.PC != countedLoopTestAddress || !errors.Is(result.Err, context.Canceled) {
				t.Fatalf("canceled run result = %+v", result)
			}
			if got := register(t, backend, cpu.RegisterR0); got != 1000 {
				t.Fatalf("counter after canceled run = %d", got)
			}
			if statistics := backend.ExecutionStatistics(); statistics.AcceleratedLoopIterations != 0 {
				t.Fatalf("canceled run accelerated: %+v", statistics)
			}
		})
	}
}

type signalingSystemBus struct {
	testSystemBus
	started chan struct{}
	once    sync.Once
}

func (b *signalingSystemBus) Read(
	address uint32,
	destination []byte,
	permission cpu.Permissions,
) error {
	if permission == cpu.PermissionExecute {
		b.once.Do(func() { close(b.started) })
	}
	return b.testSystemBus.Read(address, destination, permission)
}

func TestJITLoopAccelerationPreservesStopResponsiveness(t *testing.T) {
	for _, program := range countedLoopTestPrograms() {
		t.Run(program.name, func(t *testing.T) {
			bus := &signalingSystemBus{
				testSystemBus: testSystemBus{memory: make(map[uint32]byte)},
				started:       make(chan struct{}),
			}
			bus.writeRaw(countedLoopTestAddress, program.code)
			backend := newLoopAccelerationBackend()
			t.Cleanup(func() { _ = backend.Close() })
			if err := backend.AttachSystemBus(bus); err != nil {
				t.Fatal(err)
			}
			if err := backend.WriteRegister(cpu.RegisterR0, ^uint32(0)); err != nil {
				t.Fatal(err)
			}
			status := uint32(processorModeSystem) | statusIRQDisable | statusFIQDisable
			if program.mode == cpu.ModeThumb {
				status |= cpu.StatusThumb
			}
			if err := backend.WriteRegister(cpu.RegisterCPSR, status); err != nil {
				t.Fatal(err)
			}

			done := make(chan cpu.Result, 1)
			go func() {
				done <- backend.Run(context.Background(), countedLoopTestAddress, program.mode, 0)
			}()
			select {
			case <-bus.started:
			case <-time.After(2 * time.Second):
				t.Fatal("Run did not begin fetching loop code")
			}
			if err := backend.Stop(); err != nil {
				t.Fatal(err)
			}
			select {
			case result := <-done:
				if result.Reason != cpu.StopRequested || !errors.Is(result.Err, cpu.ErrStopped) ||
					result.Instructions == 0 || result.Instructions%runBatchInstructions != 0 {
					t.Fatalf("stopped run result = %+v", result)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("accelerated Run did not observe Stop")
			}
		})
	}
}

func TestJITLoopAccelerationContextRoundTripParity(t *testing.T) {
	for _, program := range countedLoopTestPrograms() {
		t.Run(program.name, func(t *testing.T) {
			exact := configureApplicationCountedLoop(t, NewJIT, program, 1000)
			accelerated := configureApplicationCountedLoop(t, newLoopAccelerationBackend, program, 1000)
			exactResult := exact.Run(context.Background(), countedLoopTestAddress, program.mode, 257)
			acceleratedResult := accelerated.Run(context.Background(), countedLoopTestAddress, program.mode, 257)
			assertLoopBackendParity(t, exact, accelerated, exactResult, acceleratedResult)

			exactContext, err := exact.SaveContext()
			if err != nil {
				t.Fatal(err)
			}
			acceleratedContext, err := accelerated.SaveContext()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(exactContext, acceleratedContext) {
				t.Fatal("precise and accelerated portable contexts differ")
			}
			if err := exact.RestoreContext(acceleratedContext); err != nil {
				t.Fatal(err)
			}
			if err := accelerated.RestoreContext(exactContext); err != nil {
				t.Fatal(err)
			}

			exactResult = exact.Run(context.Background(), exactResult.PC, program.mode, 100)
			acceleratedResult = accelerated.Run(context.Background(), acceleratedResult.PC, program.mode, 100)
			assertLoopBackendParity(t, exact, accelerated, exactResult, acceleratedResult)
		})
	}
}

func benchmarkJITCountedLoop(
	b *testing.B,
	program countedLoopTestProgram,
	newBackend func() *Backend,
) {
	backend := configureApplicationCountedLoopBenchmark(b, newBackend, program)
	const budget = uint64(100_000)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		if err := backend.WriteRegister(cpu.RegisterR0, uint32(budget/2)); err != nil {
			b.Fatal(err)
		}
		result := backend.Run(ctx, countedLoopTestAddress, program.mode, budget)
		if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != budget {
			b.Fatalf("run result = %+v", result)
		}
	}
	b.ReportMetric(float64(b.N)*float64(budget)/b.Elapsed().Seconds(), "guest-insn/s")
}

func configureApplicationCountedLoopBenchmark(
	b *testing.B,
	newBackend func() *Backend,
	program countedLoopTestProgram,
) *Backend {
	b.Helper()
	backend := newBackend()
	b.Cleanup(func() { _ = backend.Close() })
	if err := backend.Map(
		countedLoopTestAddress,
		0x1000,
		cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
	); err != nil {
		b.Fatal(err)
	}
	if err := backend.WriteMemory(countedLoopTestAddress, program.code); err != nil {
		b.Fatal(err)
	}
	return backend
}

func BenchmarkThumbCountedLoopJIT(b *testing.B) {
	benchmarkJITCountedLoop(b, countedLoopTestPrograms()[0], NewJIT)
}

func BenchmarkThumbCountedLoopAcceleratedJIT(b *testing.B) {
	benchmarkJITCountedLoop(b, countedLoopTestPrograms()[0], newLoopAccelerationBackend)
}

func BenchmarkARMCountedLoopJIT(b *testing.B) {
	benchmarkJITCountedLoop(b, countedLoopTestPrograms()[1], NewJIT)
}

func BenchmarkARMCountedLoopAcceleratedJIT(b *testing.B) {
	benchmarkJITCountedLoop(b, countedLoopTestPrograms()[1], newLoopAccelerationBackend)
}
