package interpreter

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func stackTransferBackend(t *testing.T, b *Backend, instruction uint16, sp uint32, warm bool, permission cpu.Permissions) *Backend {
	t.Helper()
	t.Cleanup(func() { b.Close() })
	check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	check(t, b.Map(0x3000, 0x80, permission))
	var code [4]byte
	binary.LittleEndian.PutUint16(code[:], instruction)
	binary.LittleEndian.PutUint16(code[2:], 0xbe00)
	check(t, b.WriteMemory(0x1000, code[:]))
	for i := range b.regions[1].data {
		b.regions[1].data[i] = byte(i*3 + 1)
	}
	for r := uint32(0); r < 15; r++ {
		check(t, b.WriteRegister(r, 0x55000000+r*0x101))
	}
	check(t, b.WriteRegister(cpu.RegisterSP, sp))
	check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
	if warm {
		b.cacheData(&b.regions[1], sp, cpu.PermissionRead)
		b.cacheData(&b.regions[1], sp, cpu.PermissionWrite)
	}
	return b
}

func TestThumbPrivateStackTransfersMatchPrecise(t *testing.T) {
	for _, alignment := range []bool{false, true} {
		for _, warm := range []bool{false, true} {
			for _, mask := range []uint16{0, 1, 0x30, 0x55, 0xff, 0x100, 0x130, 0x1ff} {
				for _, sp := range []uint32{0x3000, 0x3004, 0x3011, 0x3040, 0x307c, 0x3080, 0, 0xfffffffc} {
					for _, permission := range []cpu.Permissions{cpu.PermissionRead | cpu.PermissionWrite, cpu.PermissionRead, cpu.PermissionWrite} {
						instruction := uint16(0xb400) | mask
						t.Run(fmt.Sprintf("align=%t/warm=%t/mask=%x/sp=%x/perm=%d", alignment, warm, mask, sp, permission), func(t *testing.T) {
							p := stackTransferBackend(t, New(), instruction, sp, warm, permission)
							j := stackTransferBackend(t, NewJIT(), instruction, sp, warm, permission)
							if alignment {
								p.cp15.control |= 2
								j.cp15.control |= 2
							}
							j.mode = cpu.ModeThumb
							j.setModeFlag()
							j.regs[cpu.RegisterPC] = 0x1002
							if !j.pushPrivateThumbRegisters(instruction) {
								assertThumbFillParity(t, p, j, 0x1000, 1)
								return
							}
							want := p.Run(context.Background(), 0x1000, cpu.ModeThumb, 1)
							if want.Err != nil || want.Instructions != 1 {
								t.Fatalf("fast span accepted a fault: %+v", want)
							}
							for register := uint32(0); register <= cpu.RegisterCPSR; register++ {
								got, _ := j.ReadRegister(register)
								expected, _ := p.ReadRegister(register)
								if got != expected {
									t.Fatalf("register %d differs: %x / %x", register, got, expected)
								}
							}
							for i := range p.regions {
								if !bytes.Equal(p.regions[i].data, j.regions[i].data) {
									t.Fatal("stack store order differs")
								}
							}
						})
					}
				}
			}
		}
	}
}

func TestThumbPrivateStackSpanRetainsGuards(t *testing.T) {
	b := stackTransferBackend(t, NewJIT(), 0xb530, 0x3040, true, cpu.PermissionRead|cpu.PermissionWrite)
	if _, hit := b.privateThumbStackSpan(0x3020, 3, cpu.PermissionWrite); !hit {
		t.Fatal("warm private span missed")
	}
	b.physicalAccess = true
	if b.pushPrivateThumbRegisters(0xb530) {
		t.Fatal("physical transfer bypassed scalar access")
	}
	b.physicalAccess = false
	b.tlb = make([]tlbEntry, nativeTLBEntries*2)
	if b.pushPrivateThumbRegisters(0xb530) {
		t.Fatal("TLB transfer bypassed bookkeeping")
	}
	b.tlb = nil
	b.jitCodeLo, b.jitCodeHi = 0x3000, 0x3080
	b.jitCodePages = make([]uint64, nativeCodePageWords)
	b.jitCodePages[(0x3000>>codePageBits)>>6] |= 1 << ((0x3000 >> codePageBits) & 63)
	if b.pushPrivateThumbRegisters(0xb530) {
		t.Fatal("code-aliasing push bypassed invalidation")
	}
}
