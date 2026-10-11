package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Assemble synthetic register-renamed programs independently of the matcher.
func bytePaletteTestProgram(keyed bool, registers [6]uint16) []uint16 {
	s, d, x, c, tmp, next := registers[0], registers[1], registers[2], registers[3], registers[4], registers[5]
	var words []uint16
	if !keyed {
		words = []uint16{0x7800 | s<<3 | x, 0x9805 | tmp<<8, 0x0040 | x<<3 | x, 0x5a00 | tmp<<6 | x<<3 | x,
			0x3801 | c<<8, 0x8000 | d<<3 | x, 0x3001 | s<<8, 0x3002 | d<<8, 0x2800 | c<<8, 0}
	} else {
		words = []uint16{0x7800 | s<<3 | x, 0x1e40 | c<<3 | tmp, 0x9810 | c<<8, 0x3001 | s<<8,
			0x1c80 | d<<3 | next, 0x4280 | c<<3 | x, 0xd003, 0x9805 | c<<8, 0x0040 | x<<3 | x,
			0x5a00 | c<<6 | x<<3 | x, 0x8000 | d<<3 | x, 0x1c00 | next<<3 | d, 0x1c00 | tmp<<3 | c, 0x2800 | tmp<<8, 0}
	}
	words[len(words)-1] = 0xdc00 | uint16(uint8(-len(words)-1))
	return words
}

func newBytePaletteTestBackend(t *testing.T, b *Backend, keyed bool, registers [6]uint16, destination, count uint32) *Backend {
	t.Helper()
	b = newThumbStackPaletteTestBackend(t, b, destination)
	words := bytePaletteTestProgram(keyed, registers)
	code := make([]byte, 0x100)
	for i, w := range words {
		binary.LittleEndian.PutUint16(code[i*2:], w)
	}
	binary.LittleEndian.PutUint16(code[len(words)*2:], 0xbe00)
	check(t, b.WriteMemory(0x1000, code))
	check(t, b.WriteMemory(0x3000, []byte{1, 0, 2, 1, 2, 0}))
	var slot [4]byte
	binary.LittleEndian.PutUint32(slot[:], 0x4000)
	check(t, b.WriteMemory(0x6014, slot[:]))
	binary.LittleEndian.PutUint32(slot[:], 0)
	check(t, b.WriteMemory(0x6040, slot[:]))
	for r := uint32(0); r < 13; r++ {
		check(t, b.WriteRegister(r, 0xdead0000+r))
	}
	check(t, b.WriteRegister(uint32(registers[0]), 0x3000))
	check(t, b.WriteRegister(uint32(registers[1]), destination))
	check(t, b.WriteRegister(uint32(registers[3]), count))
	check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
	return b
}

func TestThumbBytePaletteLoopMatchesPrecise(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		for _, regs := range [][6]uint16{{5, 2, 0, 7, 3, 6}, {1, 4, 6, 0, 2, 5}, {0, 7, 2, 6, 5, 1}} {
			for budget := uint64(1); budget <= 46; budget++ {
				t.Run(fmt.Sprintf("keyed=%t/regs=%v/budget=%d", keyed, regs, budget), func(t *testing.T) {
					precise := newBytePaletteTestBackend(t, New(), keyed, regs, 0x5000, 3)
					jit := newBytePaletteTestBackend(t, NewJIT(), keyed, regs, 0x5000, 3)
					if jit.classifyThumbBytePaletteLoop(0x1000) == nil {
						t.Fatal("synthetic loop not recognized")
					}
					assertThumbFillParity(t, precise, jit, 0x1000, budget)
					if budget == 46 && jit.ExecutionStatistics().AcceleratedLoopIterations != 3 {
						t.Fatal("loop was not accelerated")
					}
				})
			}
		}
	}
}

func TestThumbBytePaletteLoopAliasesFaultsAndSignedCounts(t *testing.T) {
	regs := [6]uint16{5, 2, 0, 7, 3, 6}
	permissions := func(address uint32, perms cpu.Permissions) func(*Backend) {
		return func(b *Backend) {
			for i := range b.regions {
				if b.regions[i].address == address {
					b.regions[i].permissions = perms
				}
			}
			b.clearDataCaches()
		}
	}
	for _, keyed := range []bool{false, true} {
		for _, destination := range []uint32{0x6014, 0x6040, 0x3000, 0x4002, 0x1000, 0x101c, 0x50fe, 0x8000, 0x5001} {
			for _, count := range []uint32{0, 1, 3, 0x80000000, 0x7fffffff, 0xffffffff} {
				t.Run(fmt.Sprintf("keyed=%t/dest=%x/count=%x", keyed, destination, count), func(t *testing.T) {
					p := newBytePaletteTestBackend(t, New(), keyed, regs, destination, count)
					j := newBytePaletteTestBackend(t, NewJIT(), keyed, regs, destination, count)
					assertThumbFillParity(t, p, j, 0x1000, 46)
				})
			}
		}
		for _, alter := range []func(*Backend){
			permissions(0x3000, cpu.PermissionWrite),
			permissions(0x4000, cpu.PermissionWrite),
			permissions(0x5000, cpu.PermissionRead),
			func(b *Backend) { check(t, b.WriteRegister(cpu.RegisterSP, 0x6001)) },
			func(b *Backend) {
				var a [4]byte
				binary.LittleEndian.PutUint32(a[:], 0x4001)
				check(t, b.WriteMemory(0x6014, a[:]))
			},
		} {
			p := newBytePaletteTestBackend(t, New(), keyed, regs, 0x5000, 3)
			j := newBytePaletteTestBackend(t, NewJIT(), keyed, regs, 0x5000, 3)
			alter(p)
			alter(j)
			assertThumbFillParity(t, p, j, 0x1000, 46)
		}
	}
}

func TestThumbBytePaletteLoopRequiresStructureAndObservationGuards(t *testing.T) {
	regs := [6]uint16{5, 2, 0, 7, 3, 6}
	for _, keyed := range []bool{false, true} {
		words := bytePaletteTestProgram(keyed, regs)
		for index := range words {
			b := newBytePaletteTestBackend(t, NewJIT(), keyed, regs, 0x5000, 3)
			var changed [2]byte
			binary.LittleEndian.PutUint16(changed[:], words[index]^0x1000)
			check(t, b.WriteMemory(0x1000+uint32(index)*2, changed[:]))
			if b.classifyThumbBytePaletteLoop(0x1000) != nil {
				t.Fatalf("changed instruction %d recognized", index)
			}
		}
		b := newBytePaletteTestBackend(t, NewJIT(), keyed, regs, 0x5000, 3)
		loop := b.classifyThumbBytePaletteLoop(0x1000)
		for _, flags := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
			if b.accelerateThumbBytePaletteLoop(loop, 46, flags[0], flags[1], flags[2]) != 0 {
				t.Fatal("observed execution accelerated")
			}
		}
		b.physicalAccess = true
		if b.accelerateThumbBytePaletteLoop(loop, 46, false, false, false) != 0 {
			t.Fatal("physical execution accelerated")
		}
		b.physicalAccess = false
		b.tlb = make([]tlbEntry, nativeTLBEntries*2)
		b.tlbClear()
		if b.accelerateThumbBytePaletteLoop(loop, 46, false, false, false) != 0 {
			t.Fatal("TLB execution accelerated")
		}
		for _, traced := range []bool{false, true} {
			p := newBytePaletteTestBackend(t, New(), keyed, regs, 0x5000, 3)
			j := newBytePaletteTestBackend(t, NewJIT(), keyed, regs, 0x5000, 3)
			if traced {
				p.SetPCHistoryLimit(512)
				j.SetPCHistoryLimit(512)
			} else {
				// The software TLB admits only complete mapped pages.
				for _, backend := range []*Backend{p, j} {
					for i := range backend.regions {
						r := &backend.regions[i]
						r.data = append(r.data, make([]byte, 0x1000-len(r.data))...)
						r.size = 0x1000
					}
					backend.clearDataCaches()
				}
				j.tlb = make([]tlbEntry, nativeTLBEntries*2)
				j.tlbClear()
				j.nativeCodePages = make([]uint64, (1<<(32-codePageBits))/64)
			}
			assertThumbFillParity(t, p, j, 0x1000, 46)
			if j.ExecutionStatistics().AcceleratedLoopIterations != 0 {
				t.Fatal("observed execution accelerated")
			}
			if !traced && (!j.tlbHit(0x3000, cpu.PermissionRead) || !j.tlbHit(0x5000, cpu.PermissionWrite)) {
				t.Fatalf("fallback TLB keyed=%t read=%t write=%t", keyed, j.tlbHit(0x3000, cpu.PermissionRead), j.tlbHit(0x5000, cpu.PermissionWrite))
			}
		}
	}
}
