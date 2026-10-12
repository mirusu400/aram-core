package interpreter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func prologueBranchBackend(t *testing.T, b *Backend, condition uint16, regs [4]uint16, value uint32) *Backend {
	b = stackTransferBackend(t, b, 0xb530, 0x3040, true, cpu.PermissionRead|cpu.PermissionWrite)
	words := []uint16{0xb530, 0x1c00 | regs[0]<<3 | regs[1], 0x1c00 | regs[2]<<3 | regs[3], 0x2807 | regs[3]<<8, 0xd000 | condition<<8 | 2, 0xbe00, 0xbe00, 0xbe00}
	var code [16]byte
	for i, word := range words {
		binary.LittleEndian.PutUint16(code[i*2:], word)
	}
	check(t, b.WriteMemory(0x1000, code[:]))
	check(t, b.WriteRegister(uint32(regs[2]), value))
	return b
}

func TestThumbPrologueBranchMatchesPrecise(t *testing.T) {
	for condition := uint16(0); condition < 14; condition++ {
		for _, regs := range [][4]uint16{{0, 5, 1, 4}, {3, 7, 2, 6}, {0, 1, 1, 2}, {7, 7, 7, 7}} {
			for _, value := range []uint32{0, 7, 8, 0x80000000, 0xffffffff} {
				for budget := uint64(1); budget <= 6; budget++ {
					t.Run(fmt.Sprintf("cond=%d/regs=%v/value=%x/budget=%d", condition, regs, value, budget), func(t *testing.T) {
						p := prologueBranchBackend(t, New(), condition, regs, value)
						j := prologueBranchBackend(t, NewJIT(), condition, regs, value)
						block := j.jitBlockAt(0x1000)
						if block.extraLoop == nil || block.extraLoop.kind != jitPrologueBranch {
							t.Fatal("synthetic prologue not recognized")
						}
						assertThumbFillParity(t, p, j, 0x1000, budget)
					})
				}
			}
		}
	}
}

func TestThumbPrologueBranchRequiresCompleteShapeAndBudget(t *testing.T) {
	b := prologueBranchBackend(t, NewJIT(), 0, [4]uint16{0, 5, 1, 4}, 7)
	block := b.jitBlockAt(0x1000)
	p := classifyThumbPrologueBranch(block)
	if p == nil {
		t.Fatal("synthetic prologue not recognized")
	}
	for _, guard := range []struct {
		budget               uint64
		system, trap, traced bool
	}{
		{4, false, false, false}, {5, true, false, false}, {5, false, true, false}, {5, false, false, true},
	} {
		before := b.regs
		memory := append([]byte(nil), b.regions[1].data...)
		if b.accelerateThumbPrologueBranch(p, guard.budget, guard.system, guard.trap, guard.traced) != 0 {
			t.Fatal("guarded prologue was accelerated")
		}
		if before != b.regs || !bytes.Equal(memory, b.regions[1].data) {
			t.Fatal("guard changed guest state")
		}
	}
	for _, change := range []struct {
		index int
		word  uint16
	}{
		{0, 0xb400}, {1, 0x1c45}, {2, 0x2001}, {3, 0x3007}, {4, 0xde00}, {4, 0xdf00},
	} {
		copyBlock := *block
		copyBlock.thumb = append([]thumbMicroInstr(nil), block.thumb...)
		copyBlock.thumb[change.index].raw = change.word
		if classifyThumbPrologueBranch(&copyBlock) != nil {
			t.Fatal("incomplete prologue shape accepted")
		}
	}
}
