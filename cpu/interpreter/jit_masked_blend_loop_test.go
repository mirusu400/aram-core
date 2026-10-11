package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func maskedBlendTestProgram(start uint32, regs [6]uint16) []uint16 {
	d, n, x, r, m, bias := regs[0], regs[1], regs[2], regs[3], regs[4], regs[5]
	first := uint16((0x1080 - ((start + 10) &^ 3)) / 4)
	second := uint16((0x1084 - ((start + 18) &^ 3)) / 4)
	return []uint16{0x2800 | n<<8, 0xdd0d, 0x8800 | d<<3 | x, 0x4800 | m<<8 | first,
		0x0400 | x<<3 | x, 0x0c40 | x<<3 | r, 0x4000 | m<<3 | r, 0x4800 | m<<8 | second,
		0x0c80 | x<<3 | x, 0x4000 | m<<3 | x, 0x1800 | x<<6 | r<<3 | r,
		0x1800 | bias<<6 | r<<3 | r, 0x8000 | d<<3 | r, 0x3801 | n<<8, 0x3002 | d<<8, 0xe7ef}
}

func newMaskedBlendTestBackend(t *testing.T, b *Backend, start uint32, regs [6]uint16, destination, count, bias uint32) *Backend {
	t.Helper()
	b = newThumbStackPaletteTestBackend(t, b, destination)
	code := make([]byte, 256)
	words := maskedBlendTestProgram(start, regs)
	for i, w := range words {
		binary.LittleEndian.PutUint16(code[int(start-0x1000)+i*2:], w)
	}
	binary.LittleEndian.PutUint16(code[int(start-0x1000)+32:], 0xbe00)
	binary.LittleEndian.PutUint32(code[0x80:], 0x739c)
	binary.LittleEndian.PutUint32(code[0x84:], 0x3186)
	check(t, b.WriteMemory(0x1000, code))
	check(t, b.WriteMemory(0x5000, []byte{0x5a, 0xa5, 0xff, 0xff, 0x01, 0x80, 0x01, 0x00}))
	if destination == 0x7ffffffe || destination == 0xfffffffe {
		check(t, b.Map(destination, 2, cpu.PermissionRead|cpu.PermissionWrite))
		check(t, b.WriteMemory(destination, []byte{0x5a, 0xa5}))
	}
	for i := uint32(0); i < 13; i++ {
		check(t, b.WriteRegister(i, 0xdead0000+i))
	}
	check(t, b.WriteRegister(uint32(regs[0]), destination))
	check(t, b.WriteRegister(uint32(regs[1]), count))
	check(t, b.WriteRegister(uint32(regs[5]), bias))
	check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
	return b
}

func TestThumbMaskedBlendLoopMatchesPrecise(t *testing.T) {
	for _, regs := range [][6]uint16{{7, 2, 5, 0, 4, 1}, {1, 6, 2, 5, 0, 7}} {
		for _, start := range []uint32{0x1000, 0x1002} {
			for budget := uint64(1); budget <= 52; budget++ {
				t.Run(fmt.Sprintf("regs=%v/alignment=%d/budget=%d", regs, start&3, budget), func(t *testing.T) {
					p := newMaskedBlendTestBackend(t, New(), start, regs, 0x5000, 3, 0x6a31)
					j := newMaskedBlendTestBackend(t, NewJIT(), start, regs, 0x5000, 3, 0x6a31)
					if j.classifyThumbMaskedBlendLoop(start) == nil {
						t.Fatal("synthetic loop not recognized")
					}
					assertThumbFillParity(t, p, j, start, budget)
					if budget == 52 && j.ExecutionStatistics().AcceleratedLoopIterations != 3 {
						t.Fatal("loop not accelerated")
					}
				})
			}
		}
	}
}

func TestThumbMaskedBlendLoopAliasesFaultsFlagsAndSignedCounts(t *testing.T) {
	regs := [6]uint16{7, 2, 5, 0, 4, 1}
	for _, destination := range []uint32{0x1080, 0x1082, 0x1084, 0x1000, 0x101c, 0x50fe, 0x8000, 0x5001, 0x7ffffffe, 0xfffffffe} {
		for _, count := range []uint32{0, 1, 3, 0x80000000, 0x7fffffff, 0xffffffff} {
			for _, budget := range []uint64{1, 2, 15, 16, 17, 18, 52} {
				t.Run(fmt.Sprintf("dest=%x/count=%x/budget=%d", destination, count, budget), func(t *testing.T) {
					p := newMaskedBlendTestBackend(t, New(), 0x1000, regs, destination, count, 0xfffffedc)
					j := newMaskedBlendTestBackend(t, NewJIT(), 0x1000, regs, destination, count, 0xfffffedc)
					assertThumbFillParity(t, p, j, 0x1000, budget)
				})
			}
		}
	}
	for _, perms := range []cpu.Permissions{cpu.PermissionRead, cpu.PermissionWrite} {
		p := newMaskedBlendTestBackend(t, New(), 0x1000, regs, 0x5000, 3, 0x6a31)
		j := newMaskedBlendTestBackend(t, NewJIT(), 0x1000, regs, 0x5000, 3, 0x6a31)
		for _, b := range []*Backend{p, j} {
			for i := range b.regions {
				if b.regions[i].address == 0x5000 {
					b.regions[i].permissions = perms
				}
			}
			b.clearDataCaches()
		}
		assertThumbFillParity(t, p, j, 0x1000, 52)
	}
}

func TestThumbMaskedBlendLoopRequiresStructureAndObservationGuards(t *testing.T) {
	regs := [6]uint16{7, 2, 5, 0, 4, 1}
	words := maskedBlendTestProgram(0x1000, regs)
	for i := range words {
		b := newMaskedBlendTestBackend(t, NewJIT(), 0x1000, regs, 0x5000, 3, 0x6a31)
		var changed [2]byte
		binary.LittleEndian.PutUint16(changed[:], words[i]^0x1000)
		check(t, b.WriteMemory(0x1000+uint32(i)*2, changed[:]))
		if b.classifyThumbMaskedBlendLoop(0x1000) != nil {
			t.Fatalf("changed instruction %d recognized", i)
		}
	}
	b := newMaskedBlendTestBackend(t, NewJIT(), 0x1000, regs, 0x5000, 3, 0x6a31)
	l := b.classifyThumbMaskedBlendLoop(0x1000)
	for _, flags := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
		if b.accelerateThumbMaskedBlendLoop(l, 52, flags[0], flags[1], flags[2]) != 0 {
			t.Fatal("observed execution accelerated")
		}
	}
	b.physicalAccess = true
	if b.accelerateThumbMaskedBlendLoop(l, 52, false, false, false) != 0 {
		t.Fatal("physical execution accelerated")
	}
	b.physicalAccess = false
	b.tlb = make([]tlbEntry, nativeTLBEntries*2)
	b.tlbClear()
	if b.accelerateThumbMaskedBlendLoop(l, 52, false, false, false) != 0 {
		t.Fatal("TLB execution accelerated")
	}
	p := newMaskedBlendTestBackend(t, New(), 0x1000, regs, 0x5000, 3, 0x6a31)
	j := newMaskedBlendTestBackend(t, NewJIT(), 0x1000, regs, 0x5000, 3, 0x6a31)
	p.SetPCHistoryLimit(512)
	j.SetPCHistoryLimit(512)
	assertThumbFillParity(t, p, j, 0x1000, 52)
	if j.ExecutionStatistics().AcceleratedLoopIterations != 0 {
		t.Fatal("traced execution accelerated")
	}
}

func TestThumbMaskedBlendLoopUnmappedLiteral(t *testing.T) {
	regs := [6]uint16{7, 2, 5, 0, 4, 1}
	for _, position := range []uint32{3, 7} {
		p := newMaskedBlendTestBackend(t, New(), 0x1000, regs, 0x5000, 3, 0x6a31)
		j := newMaskedBlendTestBackend(t, NewJIT(), 0x1000, regs, 0x5000, 3, 0x6a31)
		var literal [2]byte
		binary.LittleEndian.PutUint16(literal[:], 0x48ff|regs[4]<<8)
		check(t, p.WriteMemory(0x1000+position*2, literal[:]))
		check(t, j.WriteMemory(0x1000+position*2, literal[:]))
		if j.classifyThumbMaskedBlendLoop(0x1000) == nil {
			t.Fatal("loop with unmapped literal should be recognized and fall back")
		}
		assertThumbFillParity(t, p, j, 0x1000, 52)
	}
}
