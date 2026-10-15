package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Literal Thumb opcodes independent of the production matcher's word table.
// Both LDR literals refer to 0x1100. The final BNE returns to 0x1000.
var colorPrivateOracleWords = []uint16{
	0x8832, 0x4b3f, 0x4013, 0x0a1b, 0x9303, 0x9b0c, 0x4013, 0x08db,
	0x9302, 0x231f, 0x4013, 0x9903, 0x00db, 0x469c, 0x1c03, 0x434b,
	0x9a09, 0x9c02, 0x18d3, 0x121f, 0x1c03, 0x4363, 0x4662, 0x4342,
	0x9908, 0x18cb, 0x121b, 0x4c32, 0x9300, 0x0239, 0x4442, 0x4021,
	0x9c00, 0x1213, 0x9301, 0x00e3, 0x9c0c, 0x4023, 0x4319, 0x12d2,
	0x231f, 0x401a, 0x4311, 0x3d01, 0x8031, 0x3602, 0x2d00, 0xd1cf,
}

func newColorPrivateOracleBackend(t *testing.T, backend *Backend, stackExecutable, pixelExecutable bool) *Backend {
	t.Helper()
	t.Cleanup(func() { _ = backend.Close() })
	rw := cpu.PermissionRead | cpu.PermissionWrite
	check(t, backend.Map(0x1000, 0x104, rw|cpu.PermissionExecute))
	stackPerms, pixelPerms := rw, rw
	if stackExecutable {
		stackPerms |= cpu.PermissionExecute
	}
	if pixelExecutable {
		pixelPerms |= cpu.PermissionExecute
	}
	check(t, backend.Map(0x2000, 16, pixelPerms))
	check(t, backend.Map(0x3000, 64, stackPerms))
	code := make([]byte, 0x104)
	for index, word := range colorPrivateOracleWords {
		binary.LittleEndian.PutUint16(code[index*2:], word)
	}
	binary.LittleEndian.PutUint16(code[96:], 0xbe00)
	binary.LittleEndian.PutUint32(code[0x100:], 0xfffff800)
	check(t, backend.WriteMemory(0x1000, code))
	pixels, stack := make([]byte, 16), make([]byte, 64)
	for i := range pixels {
		pixels[i] = byte(i*29 + 7)
	}
	for i := range stack {
		stack[i] = byte(i*43 + 11)
	}
	binary.LittleEndian.PutUint32(stack[32:], 0x7fffffff)
	binary.LittleEndian.PutUint32(stack[36:], 0xffffff35)
	binary.LittleEndian.PutUint32(stack[48:], 0x000007e0)
	check(t, backend.WriteMemory(0x2000, pixels))
	check(t, backend.WriteMemory(0x3000, stack))
	for r := uint32(0); r < cpu.RegisterPC; r++ {
		check(t, backend.WriteRegister(r, 0x9e370000+r*0x103))
	}
	check(t, backend.WriteRegister(0, 137))
	check(t, backend.WriteRegister(5, 8))
	check(t, backend.WriteRegister(6, 0x2000))
	check(t, backend.WriteRegister(8, 12345))
	check(t, backend.WriteRegister(cpu.RegisterSP, 0x3000))
	check(t, backend.WriteRegister(cpu.RegisterCPSR, 0xb000003f))
	return backend
}

// A translated whole-iteration shortcut must not bypass the scalar word
// alignment/rotation rules for guest stack spills and parameters.
func TestThumbColorPrivateUnalignedStackOracle(t *testing.T) {
	for lowBits := uint32(1); lowBits < 4; lowBits++ {
		for _, budget := range []uint64{47, 48, 49, 95, 96, 97, 255, 256, 257} {
			t.Run(fmt.Sprintf("sp-low=%d/budget=%d", lowBits, budget), func(t *testing.T) {
				precise := newColorPrivateOracleBackend(t, New(), false, false)
				translated := newColorPrivateOracleBackend(t, NewJIT(), false, false)
				for _, backend := range []*Backend{precise, translated} {
					check(t, backend.WriteRegister(cpu.RegisterSP, 0x3000+lowBits))
					check(t, backend.WriteRegister(5, 8))
				}
				assertThumbColorParity(t, precise, translated, budget)
				if translated.ExecutionStatistics().AcceleratedLoopInstructions != 0 {
					t.Fatal("unaligned stack bypassed scalar accesses")
				}
			})
		}
	}
}
