package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func loadMaskBranchBackend(t *testing.T, b *Backend, condition uint16, regs [6]uint16, value, address uint32, warm bool) *Backend {
	b = stackTransferBackend(t, b, 0xbe00, 0x3040, true, cpu.PermissionRead|cpu.PermissionWrite)
	words := []uint16{0x6800 | regs[0]<<3 | regs[1], 0x201f | regs[2]<<8, 0x4000 | regs[3]<<3 | regs[4], 0x2807 | regs[5]<<8, 0xd000 | condition<<8 | 2, 0xbe00, 0xbe00, 0xbe00}
	var code [16]byte
	for i, w := range words {
		binary.LittleEndian.PutUint16(code[i*2:], w)
	}
	check(t, b.WriteMemory(0x1000, code[:]))
	check(t, b.WriteRegister(uint32(regs[0]), address))
	binary.LittleEndian.PutUint32(b.regions[1].data[:], value)
	if !warm {
		b.clearDataCaches()
	}
	return b
}

func TestThumbLoadMaskBranchMatchesPrecise(t *testing.T) {
	for condition := uint16(0); condition < 14; condition++ {
		for _, regs := range [][6]uint16{{5, 1, 3, 1, 3, 3}, {4, 2, 7, 2, 7, 7}, {0, 0, 0, 0, 0, 0}, {0, 1, 1, 1, 0, 0}} {
			for _, value := range []uint32{0, 7, 8, 0xffffffff} {
				for _, address := range []uint32{0x3000, 0x3001, 0x307f, 0x4000} {
					for budget := uint64(1); budget <= 6; budget++ {
						t.Run(fmt.Sprintf("cond=%d/regs=%v/value=%x/address=%x/budget=%d", condition, regs, value, address, budget), func(t *testing.T) {
							p := loadMaskBranchBackend(t, New(), condition, regs, value, address, true)
							j := loadMaskBranchBackend(t, NewJIT(), condition, regs, value, address, true)
							if block := j.jitBlockAt(0x1000); block.extraLoop == nil || block.extraLoop.kind != jitLoadMaskBranch {
								t.Fatal("synthetic sequence not recognized")
							}
							assertThumbFillParity(t, p, j, 0x1000, budget)
						})
					}
				}
			}
		}
	}
}
