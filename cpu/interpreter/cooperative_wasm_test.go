//go:build js && wasm

package interpreter

import (
	"context"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// A closed fetch channel makes the cancelling goroutine runnable without any
// JS timer. It must get CPU before a bounded, otherwise non-terminating loop
// spends its entire budget. Keep this separate from browser event-loop claims.
func TestWasmRunSchedulesRunnableCancellation(t *testing.T) {
	for backendIndex, create := range []func() *Backend{New, newLoopAccelerationBackend} {
		for _, program := range countedLoopTestPrograms() {
			bus := &signalingSystemBus{
				testSystemBus: testSystemBus{memory: make(map[uint32]byte)},
				started:       make(chan struct{}),
			}
			bus.writeRaw(countedLoopTestAddress, program.code)
			backend := create()
			t.Cleanup(func() { _ = backend.Close() })
			check(t, backend.AttachSystemBus(bus))
			check(t, backend.WriteRegister(cpu.RegisterR0, ^uint32(0)))
			status := uint32(processorModeSystem) | statusIRQDisable | statusFIQDisable
			if program.mode == cpu.ModeThumb {
				status |= cpu.StatusThumb
			}
			check(t, backend.WriteRegister(cpu.RegisterCPSR, status))
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan cpu.Result, 1)
			go func() { done <- backend.Run(ctx, countedLoopTestAddress, program.mode, 1<<20) }()
			<-bus.started
			cancel()
			result := <-done
			if result.Reason != cpu.StopRequested || !errors.Is(result.Err, context.Canceled) ||
				result.Instructions == 0 || result.Instructions >= 1<<20 || result.Instructions%runBatchInstructions != 0 {
				t.Fatalf("backend %d / %s cancellation = %+v", backendIndex, program.name, result)
			}
		}
	}
}
