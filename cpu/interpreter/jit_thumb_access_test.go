package interpreter

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Warm private bytes deliberately differ from physical bus bytes. Each narrow
// load must still use the bus in system mode and populate the native TLB when
// ordinary private memory is read with a native fallback cache present.
func TestThumbCachedNarrowReadsUsePhysicalBusAndNativeTLB(t *testing.T) {
	for _, test := range []struct {
		raw                    uint16
		busValue, privateValue uint32
	}{
		{0x5608, 0xfffffffe, 1},      // LDRSB r0,[r1,r0]
		{0x5a08, 0x80fe, 0x0201},     // LDRH r0,[r1,r0]
		{0x5c08, 0xfe, 1},            // LDRB r0,[r1,r0]
		{0x5e08, 0xffff80fe, 0x0201}, // LDRSH r0,[r1,r0]
		{0x7808, 0xfe, 1},            // LDRB r0,[r1,#0]
		{0x8808, 0x80fe, 0x0201},     // LDRH r0,[r1,#0]
	} {
		b := NewJIT()
		t.Cleanup(func() { _ = b.Close() })
		check(t, b.Map(0x2000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite))
		check(t, b.WriteMemory(0x2000, []byte{1, 2}))
		_, err := b.read16(0x2000, cpu.PermissionRead)
		check(t, err)
		b.regs[cpu.RegisterR0], b.regs[cpu.RegisterR1] = 0, 0x2000
		block := &jitBlock{thumb: []thumbMicroInstr{{pc: 0x1000, raw: test.raw, op: thumbInstructionClasses[test.raw]}}}
		bus := &testSystemBus{memory: map[uint32]byte{0x2000: 0xfe, 0x2001: 0x80}}
		b.systemBus, b.physicalAccess = bus, true
		retired, _, _, err := b.executeThumbMicroBlock(block, 1, true, false, false)
		check(t, err)
		if retired != 1 || b.regs[cpu.RegisterR0] != test.busValue || bus.dataReads != 1 {
			t.Fatalf("opcode %#x bypassed physical bus: retired=%d value=%#x reads=%d", test.raw, retired, b.regs[cpu.RegisterR0], bus.dataReads)
		}
		b.systemBus, b.physicalAccess = nil, false
		b.tlb = make([]tlbEntry, nativeTLBEntries*2)
		b.tlbClear()
		b.regs[cpu.RegisterR0] = 0
		retired, _, _, err = b.executeThumbMicroBlock(block, 1, false, false, false)
		check(t, err)
		if retired != 1 || b.regs[cpu.RegisterR0] != test.privateValue || !b.tlbHit(0x2000, cpu.PermissionRead) {
			t.Fatalf("opcode %#x skipped private read or native TLB: retired=%d value=%#x", test.raw, retired, b.regs[cpu.RegisterR0])
		}
	}
}

func TestThumbCachedNarrowReadsPreserveFaults(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		mapCodeAndStack(t, b)
		check(t, b.Map(0x4000, 16, cpu.PermissionRead|cpu.PermissionWrite))
		check(t, b.Map(0x5000, 16, cpu.PermissionWrite))
		check(t, b.WriteMemory(0x4000, []byte{0x5a, 0xa5}))
		t.Cleanup(func() { _ = b.Close() })
	}
	for _, instruction := range []uint16{0x7808, 0x8808} { // LDRB / LDRH r0,[r1]
		for _, address := range []uint32{0x3fff, 0x400f, 0x5000, 0xffffffff} {
			var results [2]cpu.Result
			for index, b := range []*Backend{precise, jit} {
				check(t, b.WriteMemory(0x1000, thumbTestCode(instruction)))
				check(t, b.WriteRegister(cpu.RegisterR1, 0x4000))
				warm := b.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
				if warm.Err != nil || warm.Instructions != 1 {
					t.Fatalf("cache warmup: %+v", warm)
				}
				check(t, b.WriteRegister(cpu.RegisterR0, 0xfeedface))
				check(t, b.WriteRegister(cpu.RegisterR1, address))
				results[index] = b.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
			}
			wantFault := address != 0x400f || instruction == 0x8808
			for _, result := range results {
				if (result.Err != nil) != wantFault {
					t.Fatalf("instruction %#x address %#x: expected fault=%v, result=%+v", instruction, address, wantFault, result)
				}
				if address == 0x5000 && !errors.Is(result.Err, cpu.ErrPermissionDenied) {
					t.Fatalf("read bypassed write-only permissions: %+v", result)
				}
			}
			if results[0].Reason != results[1].Reason || results[0].Instructions != results[1].Instructions || results[0].PC != results[1].PC {
				t.Fatalf("instruction %#x address %#x: precise=%+v jit=%+v", instruction, address, results[0], results[1])
			}
			for r := uint32(0); r <= cpu.RegisterCPSR; r++ {
				if got, want := immediateMemoryRegisterValue(t, jit, r), immediateMemoryRegisterValue(t, precise, r); got != want {
					t.Fatalf("instruction %#x address %#x register %d: got %#x want %#x", instruction, address, r, got, want)
				}
			}
		}
	}
}

// Exercise every register-offset transfer (including signed loads) and every
// immediate halfword transfer against the independent precise implementation.
// All base/destination/offset aliases and all byte alignments are represented.
func TestThumbRegisterAndHalfwordTransfersMatchPrecise(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		mapCodeAndStack(t, b)
		check(t, b.Map(0x4000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite))
		t.Cleanup(func() { _ = b.Close() })
	}
	seed := make([]byte, 1024)
	for i := range seed {
		seed[i] = byte(i*29 + 137)
	}
	ctx := context.Background()
	for _, first := range []uint32{0x5000, 0x8000} {
		for raw := first; raw < first+0x1000; raw++ {
			for _, b := range []*Backend{precise, jit} {
				check(t, b.WriteMemory(0x1000, thumbTestCode(uint16(raw))))
				check(t, b.WriteMemory(0x2400, seed))
				check(t, b.WriteMemory(0x4800, seed))
				for r := uint32(0); r < 8; r++ {
					check(t, b.WriteRegister(r, 0x2400+r*8+(raw&3)))
				}
				check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
				result := b.Run(ctx, 0x1000, cpu.ModeThumb, 1)
				if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 1 {
					t.Fatalf("opcode %#04x: %+v", raw, result)
				}
			}
			for r := uint32(0); r <= cpu.RegisterCPSR; r++ {
				want := immediateMemoryRegisterValue(t, precise, r)
				if got := immediateMemoryRegisterValue(t, jit, r); got != want {
					t.Fatalf("opcode %#04x register %d: got %#x want %#x", raw, r, got, want)
				}
			}
			for _, address := range []uint32{0x2400, 0x4800} {
				var want, got [1024]byte
				check(t, precise.ReadMemory(address, want[:]))
				check(t, jit.ReadMemory(address, got[:]))
				if !bytes.Equal(want[:], got[:]) {
					t.Fatalf("opcode %#04x memory at %#x changed", raw, address)
				}
			}
		}
	}
}
