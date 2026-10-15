package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func assertThumbApplicationTransferParity(t *testing.T, precise, jit *Backend, raw uint16, budget uint64) {
	t.Helper()
	want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
	got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
	if got.Reason != want.Reason || got.Instructions != want.Instructions || got.PC != want.PC || !sameError(got.Err, want.Err) {
		t.Fatalf("opcode %04x budget %d: got=%+v want=%+v", raw, budget, got, want)
	}
	for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
		actual := immediateMemoryRegisterValue(t, jit, register)
		expected := immediateMemoryRegisterValue(t, precise, register)
		if actual != expected {
			t.Fatalf("opcode %04x budget %d register %d: got=%08x want=%08x", raw, budget, register, actual, expected)
		}
	}
	for index := range precise.regions {
		if !bytes.Equal(precise.regions[index].data, jit.regions[index].data) {
			t.Fatalf("opcode %04x budget %d memory at %08x differs", raw, budget, precise.regions[index].address)
		}
	}
}

// Budget=1 alone reaches the guarded partial executor for these nonterminal
// encodings. The BKPT creates a complete two-instruction application block;
// budget=2 independently checks the specialized executor, including all
// destination/base/offset aliases and every byte alignment.
func TestThumbApplicationTransfersAllEncodingsMatchPrecise(t *testing.T) {
	precise, jit := New(), NewJIT()
	for _, b := range []*Backend{precise, jit} {
		mapCodeAndStack(t, b)
		check(t, b.Map(0x4000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite))
		t.Cleanup(func() { _ = b.Close() })
	}
	seed := make([]byte, 1024)
	for index := range seed {
		seed[index] = byte(index*29 + 137)
	}
	for _, bounds := range [][2]uint32{{0x5000, 0x6000}, {0x6000, 0x8000}, {0x8000, 0xa000}} {
		for raw := bounds[0]; raw < bounds[1]; raw++ {
			for budget := uint64(1); budget <= 2; budget++ {
				for _, b := range []*Backend{precise, jit} {
					check(t, b.WriteMemory(0x1000, thumbTestCode(uint16(raw), 0xbe00)))
					check(t, b.WriteMemory(0x2400, seed))
					check(t, b.WriteMemory(0x4800, seed))
					for register := uint32(0); register < 8; register++ {
						check(t, b.WriteRegister(register, 0x2400+register*8+(raw&3)))
					}
					check(t, b.WriteRegister(cpu.RegisterSP, 0x2400+(raw&3)))
					check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
				}
				block := jit.jitBlockAt(0x1000)
				if block == nil || len(block.thumb) != 2 || block.extraLoop != nil || block.countedLoop != nil {
					t.Fatalf("opcode %04x did not form a complete ordinary application block", raw)
				}
				assertThumbApplicationTransferParity(t, precise, jit, uint16(raw), budget)
			}
		}
	}
}

func TestThumbApplicationTransferFaultsMatchPrecise(t *testing.T) {
	for _, permission := range []cpu.Permissions{cpu.PermissionRead, cpu.PermissionWrite, cpu.PermissionRead | cpu.PermissionWrite} {
		precise, jit := New(), NewJIT()
		for _, b := range []*Backend{precise, jit} {
			t.Cleanup(func() { _ = b.Close() })
			check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
			check(t, b.Map(0x3000, 32, permission))
		}
		for _, raw := range []uint16{0x5088, 0x5288, 0x5488, 0x5688, 0x5888, 0x5a88, 0x5c88, 0x5e88, 0x6008, 0x6808, 0x7008, 0x7808} {
			for _, address := range []uint32{0x2fff, 0x3000, 0x3001, 0x301d, 0x301f, 0x3020, 0xfffffffc, 0xffffffff} {
				for _, alignment := range []bool{false, true} {
					t.Run(fmt.Sprintf("%04x/address=%x/perm=%d/align=%t", raw, address, permission, alignment), func(t *testing.T) {
						for _, b := range []*Backend{precise, jit} {
							check(t, b.WriteMemory(0x1000, thumbTestCode(raw, 0xbe00)))
							for index := range b.regions[1].data {
								b.regions[1].data[index] = byte(index*17 + 129)
							}
							check(t, b.WriteRegister(cpu.RegisterR0, 0x89abcdef))
							check(t, b.WriteRegister(cpu.RegisterR1, address))
							check(t, b.WriteRegister(cpu.RegisterR2, 0))
							check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagZ|flagC|flagV|uint32(processorModeSystem)))
							b.cp15.control &^= 2
							if alignment {
								b.cp15.control |= 2
							}
							b.cacheData(&b.regions[1], 0x3000, cpu.PermissionRead)
							b.cacheData(&b.regions[1], 0x3000, cpu.PermissionWrite)
						}
						if block := jit.jitBlockAt(0x1000); block == nil || len(block.thumb) != 2 {
							t.Fatal("fault oracle did not form a complete application block")
						}
						assertThumbApplicationTransferParity(t, precise, jit, raw, 2)
					})
				}
			}
		}
	}
}
