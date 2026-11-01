package ktf

import (
	"context"
	"testing"

	"github.com/mirusu400/aram-core/cpu/interpreter"
	"github.com/mirusu400/aram-core/loader/ktf"
)

// Issue #569: a native Clet reads MC_knlGetAccessLevel through kernel slot
// 0x34 and refuses to start unless the program holds the access bits it
// needs. 슈퍼액션히어로2 masks the answer with 0xbc; the slot used to fall
// through to the no-op handler and answer 0, so the title stopped on its
// "인증 과정에 에러가 발생했습니다" (error 1001) screen, and confirming that
// screen ran its error path into a fault.
func TestKTFKernelAccessLevelGrantsTheJavaFrameworkLevel(t *testing.T) {
	// The title's own call sequence: load the slot, call it, keep the bits
	// it gates on.
	client := []byte{
		0x00, 0xb5, // push {lr}
		0x43, 0x6b, // ldr r3, [r0, #0x34]
		0x98, 0x47, // blx r3
		0xbc, 0x21, // movs r1, #0xbc
		0x08, 0x40, // ands r0, r1
		0x00, 0xbd, // pop {pc}
	}
	runtime, err := NewRuntime(interpreter.New(), ktf.Package{
		ClientName: "client.bin0",
		Client:     client,
	})
	check(t, err)
	defer runtime.CPU.Close()
	check(t, runtime.MapImageAndHost())
	knl, err := runtime.lookupInterface("WIPIC_knlInterface")
	check(t, err)

	_, granted, err := runtime.call(
		context.Background(),
		ImageBase|1,
		[]uint32{knl},
		1_000,
	)
	check(t, err)
	if granted != 0xbc {
		t.Fatalf("MC_knlGetAccessLevel & 0xbc = 0x%02x, want 0xbc", granted)
	}

	level, err := ktfKernelGetAccessLevel(context.Background(), runtime)
	check(t, err)
	javaLevel, err := runtime.handleMSFKernelMethod("getAccessLevel", "()I")
	check(t, err)
	if level != javaLevel {
		t.Fatalf(
			"MC_knlGetAccessLevel = 0x%x, MsfKernel.getAccessLevel() = 0x%x",
			level,
			javaLevel,
		)
	}
}
