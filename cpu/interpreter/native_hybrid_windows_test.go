//go:build windows && amd64

package interpreter

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestHybridSystemSelectsNativeARMAndGoThumb(t *testing.T) {
	backend := NewHybridJIT()
	if backend.nativeBlocks == nil || backend.jitBlocks == nil {
		backend.Close()
		t.Skip("native executable arena unavailable")
	}
	bus := &nativeSystemBus{ram: make([]byte, 0x10000), mmio: 0x9000}
	if err := backend.AttachSystemBus(bus); err != nil {
		backend.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close() })

	putThumb(bus.ram, 0x1000, 0x2001, 0xbe00) // movs r0,#1; bkpt
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 2)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("Thumb run = %+v", result)
	}
	if backend.jitBlocks[0x1000] == nil {
		t.Fatal("whole-system Thumb did not use the Go micro-op block tier")
	}
	if _, translatedNative := backend.nativeBlocks[0x1000]; translatedNative {
		t.Fatal("whole-system Thumb unexpectedly emitted a native block")
	}

	putARM(bus.ram, 0x2000, 0xe3a00002, 0xe1200070) // mov r0,#2; bkpt
	result = backend.Run(context.Background(), 0x2000, cpu.ModeARM, 2)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("ARM run = %+v", result)
	}
	if backend.nativeARMBlocks[0x2000] == nil {
		t.Fatal("whole-system ARM did not use the native block tier")
	}
}
