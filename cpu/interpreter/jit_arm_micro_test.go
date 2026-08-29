package interpreter

import (
	"encoding/binary"
	"testing"
)

func TestARMJITMemoryFamiliesUseDirectMicroOps(t *testing.T) {
	backend := NewJIT()
	mapCodeAndStack(t, backend)
	code := make([]byte, 16)
	for index, instruction := range []uint32{
		0xe5910000, // ldr r0,[r1]
		0xe1d120b0, // ldrh r2,[r1]
		0xe8b10018, // ldmia r1!,{r3,r4}
		0xe1200070, // bkpt
	} {
		binary.LittleEndian.PutUint32(code[index*4:], instruction)
	}
	if err := backend.WriteMemory(0x1000, code); err != nil {
		t.Fatal(err)
	}
	block := backend.armJITBlockAt(0x1000)
	if block == nil || len(block.arm) != 4 {
		t.Fatalf("ARM block = %#v, want four instructions", block)
	}
	want := []armMicroOp{
		armMicroSingleTransfer,
		armMicroHalfwordTransfer,
		armMicroBlockTransfer,
	}
	for index, op := range want {
		if got := block.arm[index].op; got != op || block.arm[index].exec != nil {
			t.Fatalf("instruction %d op=%d exec=%p, want direct op=%d",
				index, got, block.arm[index].exec, op)
		}
	}
	if block.arm[3].op != armMicroClosure || block.arm[3].exec == nil {
		t.Fatal("non-memory terminator did not retain its decoded fallback")
	}
}
