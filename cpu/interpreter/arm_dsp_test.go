package interpreter

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// The ARMv5TE DSP instructions share the opcode space of a compare with S
// clear. Every execution tier must compute them and must leave N, Z, C and V
// alone.
func TestARMEnhancedDSPInstructions(t *testing.T) {
	program := []uint32{
		0xe16a0880, // smulbb r10, r0, r8
		0xe16b08c0, // smulbt r11, r0, r8
		0xe16c08e0, // smultt r12, r0, r8
		0xe1031180, // smlabb r3, r0, r1, r1
		0xe12402a2, // smulwb r4, r2, r2
		0xe12651c2, // smlawt r6, r2, r1, r5
		0xe1497980, // smlalbb r7, r9, r0, r9
		0xe1025051, // qadd r5, r1, r2
		0xe1200070, // bkpt
	}
	for _, tier := range armExecutionTierConstructors() {
		t.Run(tier.name, func(t *testing.T) {
			backend := tier.new()
			defer backend.Close()
			check(t, backend.Map(
				0x1000,
				0x1000,
				cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
			))
			code := make([]byte, len(program)*4)
			for index, instruction := range program {
				binary.LittleEndian.PutUint32(code[index*4:], instruction)
			}
			check(t, backend.WriteMemory(0x1000, code))
			check(t, backend.WriteRegister(cpu.RegisterR0, 0x0003fffe)) // halves 3, -2
			check(t, backend.WriteRegister(cpu.RegisterR1, 0x7fffffff))
			check(t, backend.WriteRegister(cpu.RegisterR2, 0x00050004))
			check(t, backend.WriteRegister(cpu.RegisterR5, 0x00000010))
			check(t, backend.WriteRegister(cpu.RegisterR7, 0xffffffff))
			check(t, backend.WriteRegister(cpu.RegisterR8, 0x00070002)) // halves 7, 2
			check(t, backend.WriteRegister(cpu.RegisterR9, 0x00000000))
			const flags = uint32(0x60000000) // Z and C set, N and V clear
			cpsr := register(t, backend, cpu.RegisterCPSR)
			check(t, backend.WriteRegister(cpu.RegisterCPSR, cpsr&^0xf8000000|flags))

			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 16)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint {
				t.Fatalf("result = %+v", result)
			}
			for id, want := range map[uint32]uint32{
				cpu.RegisterR10: 0xfffffffc, // -2 * 2
				cpu.RegisterR11: 0xfffffff2, // -2 * 7
				cpu.RegisterR12: 0x00000015, // 3 * 7
				cpu.RegisterR3:  0x80000001, // -2 * -1 + 0x7fffffff overflows
				cpu.RegisterR4:  0x00000014, // (0x00050004 * 4) >> 16
				cpu.RegisterR6:  0x0002800c, // (0x00050004 * 0x7fff) >> 16, plus 0x10
				cpu.RegisterR7:  0xffffffff, // low half of -1 + (-2 * 0)
				cpu.RegisterR9:  0x00000000,
				cpu.RegisterR5:  0x7fffffff, // saturated
			} {
				if got := register(t, backend, id); got != want {
					t.Errorf("register %d = 0x%08x, want 0x%08x", id, got, want)
				}
			}
			got := register(t, backend, cpu.RegisterCPSR) & 0xf8000000
			if want := flags | flagQ; got != want {
				t.Errorf("CPSR flags = 0x%08x, want 0x%08x", got, want)
			}
		})
	}
}

func TestARMSaturatingDoubleAndSubtract(t *testing.T) {
	program := []uint32{
		0xe1223051, // qsub r3, r1, r2
		0xe1424051, // qdadd r4, r1, r2
		0xe1625051, // qdsub r5, r1, r2
		0xe1200070, // bkpt
	}
	for _, tier := range armExecutionTierConstructors() {
		t.Run(tier.name, func(t *testing.T) {
			backend := tier.new()
			defer backend.Close()
			check(t, backend.Map(
				0x1000,
				0x1000,
				cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute,
			))
			code := make([]byte, len(program)*4)
			for index, instruction := range program {
				binary.LittleEndian.PutUint32(code[index*4:], instruction)
			}
			check(t, backend.WriteMemory(0x1000, code))
			check(t, backend.WriteRegister(cpu.RegisterR1, 0x00000005))
			check(t, backend.WriteRegister(cpu.RegisterR2, 0x00000003))
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint {
				t.Fatalf("result = %+v", result)
			}
			for id, want := range map[uint32]uint32{
				cpu.RegisterR3: 0x00000002, // 5 - 3
				cpu.RegisterR4: 0x0000000b, // 5 + 2*3
				cpu.RegisterR5: 0xffffffff, // 5 - 2*3
			} {
				if got := register(t, backend, id); got != want {
					t.Errorf("register %d = 0x%08x, want 0x%08x", id, got, want)
				}
			}
			if got := register(t, backend, cpu.RegisterCPSR) & flagQ; got != 0 {
				t.Errorf("Q set without saturation")
			}
		})
	}
}
