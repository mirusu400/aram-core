package interpreter

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestThumbImmediateBranchExchangeWithLinkEntersARM(t *testing.T) {
	backend := New()
	mapCodeAndStack(t, backend)
	if err := backend.WriteMemory(0x1002, []byte{
		0x00, 0xf0, // BLX prefix: high offset 0
		0x7e, 0xe8, // BLX suffix: branch to aligned 0x1100
		0x00, 0xbe, // Thumb return address: BKPT
	}); err != nil {
		t.Fatal(err)
	}
	code := make([]byte, 8)
	binary.LittleEndian.PutUint32(code[0:4], 0xe3a0002a) // MOV r0, #42
	binary.LittleEndian.PutUint32(code[4:8], 0xe12fff1e) // BX lr
	if err := backend.WriteMemory(0x1100, code); err != nil {
		t.Fatal(err)
	}

	result := backend.Run(context.Background(), 0x1002, cpu.ModeThumb, 4)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("Thumb BLX result = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterLR); got != 0x1007 {
		t.Fatalf("LR after Thumb BLX = 0x%x, want 0x1007", got)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 42 {
		t.Fatalf("r0 after ARM target = %d, want 42", got)
	}
}
