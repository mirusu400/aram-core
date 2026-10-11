package interpreter

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Synthetic assembly varies registers, direction, parity, and stack layout.
func nibblePaletteTestProgram(keyed, reverse bool, regs [7]uint16) []uint16 {
	c, d, n, x, s, tmp, next := regs[0], regs[1], regs[2], regs[3], regs[4], regs[5], regs[6]
	step := uint16(0x1c40 | c<<3 | s)
	if reverse {
		step = 0x1e40 | c<<3 | s
	}
	var words []uint16
	if keyed {
		step = 0x1c40 | c<<3 | tmp
		if reverse {
			step = 0x1e40 | c<<3 | tmp
		}
		words = []uint16{0x0840 | c<<3 | s, step, 0x07c0 | c<<3 | x, 0xd502,
			0x7800 | s<<3 | s, 0x0900 | s<<3 | x, 0xe002,
			0x7800 | s<<3 | x, 0x200f | s<<8, 0x4000 | s<<3 | x,
			0x9810 | s<<8, 0x1c00 | tmp<<3 | c, 0x1e40 | n<<3 | next, 0x1c80 | d<<3 | tmp,
			0x4280 | s<<3 | x, 0xd003, 0x9805 | n<<8, 0x0040 | x<<3 | s,
			0x5a00 | n<<6 | s<<3 | s, 0x8000 | d<<3 | s, 0x1c00 | tmp<<3 | d,
			0x1c00 | next<<3 | n, 0x2800 | next<<8, 0}
	} else {
		words = []uint16{0x1c80 | d<<3 | s, 0x9013 | s<<8, 0x3801 | n<<8, step,
			0x0840 | c<<3 | x, 0x9011 | s<<8, 0x900a | n<<8, 0x07c0 | c<<3 | n, 0xd505,
			0x7800 | x<<3 | x, 0x9805 | s<<8, 0x0900 | x<<3 | x, 0x0040 | x<<3 | x,
			0x5a00 | s<<6 | x<<3 | x, 0xe005, 0x7800 | x<<3 | x, 0x200f | s<<8,
			0x4000 | s<<3 | x, 0x9805 | n<<8, 0x0040 | x<<3 | x, 0x5a00 | n<<6 | x<<3 | x,
			0x980a | n<<8, 0x8000 | d<<3 | x, 0x9811 | c<<8, 0x9813 | d<<8, 0x2800 | n<<8, 0}
	}
	words[len(words)-1] = 0xdc00 | uint16(uint8(-len(words)-1))
	return words
}

func newNibblePaletteTestBackend(t *testing.T, b *Backend, keyed, reverse bool, regs [7]uint16, cursor, destination, count uint32) *Backend {
	t.Helper()
	b = newThumbStackPaletteTestBackend(t, b, destination)
	words := nibblePaletteTestProgram(keyed, reverse, regs)
	code := make([]byte, 256)
	for i, w := range words {
		binary.LittleEndian.PutUint16(code[i*2:], w)
	}
	binary.LittleEndian.PutUint16(code[len(words)*2:], 0xbe00)
	check(t, b.WriteMemory(0x1000, code))
	check(t, b.WriteMemory(0x3000, []byte{0x21, 0x10, 0x32, 0x21}))
	var slot [4]byte
	binary.LittleEndian.PutUint32(slot[:], 0x4000)
	check(t, b.WriteMemory(0x6014, slot[:]))
	binary.LittleEndian.PutUint32(slot[:], 0)
	check(t, b.WriteMemory(0x6040, slot[:]))
	for r := uint32(0); r < 13; r++ {
		check(t, b.WriteRegister(r, 0xdead0000+r))
	}
	check(t, b.WriteRegister(uint32(regs[0]), cursor))
	check(t, b.WriteRegister(uint32(regs[1]), destination))
	check(t, b.WriteRegister(uint32(regs[2]), count))
	check(t, b.WriteRegister(cpu.RegisterCPSR, flagN|flagC|flagV|uint32(processorModeSystem)))
	return b
}

func TestThumbNibblePaletteLoopMatchesPrecise(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			for _, regs := range [][7]uint16{{6, 0, 5, 7, 3, 2, 4}, {2, 5, 1, 3, 7, 4, 6}} {
				for _, parity := range []uint32{0, 1} {
					for budget := uint64(1); budget <= 67; budget++ {
						t.Run(fmt.Sprintf("keyed=%t/reverse=%t/regs=%v/parity=%d/budget=%d", keyed, reverse, regs, parity, budget), func(t *testing.T) {
							cursor := uint32(0x6000) + parity
							if reverse {
								cursor += 4
							}
							p := newNibblePaletteTestBackend(t, New(), keyed, reverse, regs, cursor, 0x5000, 3)
							j := newNibblePaletteTestBackend(t, NewJIT(), keyed, reverse, regs, cursor, 0x5000, 3)
							if j.classifyThumbNibblePaletteLoop(0x1000) == nil {
								t.Fatal("synthetic loop not recognized")
							}
							assertThumbFillParity(t, p, j, 0x1000, budget)
							if budget == 67 && j.ExecutionStatistics().AcceleratedLoopIterations != 3 {
								t.Fatal("loop not accelerated")
							}
						})
					}
				}
			}
		}
	}
}

func TestThumbNibblePaletteLoopAliasesFaultsAndSignedCounts(t *testing.T) {
	regs := [7]uint16{6, 0, 5, 7, 3, 2, 4}
	for _, keyed := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			for _, dest := range []uint32{0x6014, 0x6028, 0x6044, 0x604c, 0x6040, 0x3000, 0x4002, 0x1000, 0x101c, 0x50fe, 0x8000, 0x5001} {
				for _, count := range []uint32{0, 1, 3, 0x80000000, 0x7fffffff, 0xffffffff} {
					t.Run(fmt.Sprintf("keyed=%t/reverse=%t/dest=%x/count=%x", keyed, reverse, dest, count), func(t *testing.T) {
						p := newNibblePaletteTestBackend(t, New(), keyed, reverse, regs, 0x6004, dest, count)
						j := newNibblePaletteTestBackend(t, NewJIT(), keyed, reverse, regs, 0x6004, dest, count)
						assertThumbFillParity(t, p, j, 0x1000, 67)
					})
				}
			}
			for _, address := range []uint32{0x3000, 0x4000, 0x5000, 0x6000} {
				p := newNibblePaletteTestBackend(t, New(), keyed, reverse, regs, 0x6004, 0x5000, 3)
				j := newNibblePaletteTestBackend(t, NewJIT(), keyed, reverse, regs, 0x6004, 0x5000, 3)
				for _, b := range []*Backend{p, j} {
					for i := range b.regions {
						if b.regions[i].address == address {
							b.regions[i].permissions = cpu.PermissionRead
						}
					}
					b.clearDataCaches()
				}
				assertThumbFillParity(t, p, j, 0x1000, 67)
			}
			for _, sp := range []uint32{0x6001, 0x1000, 0xfffffffc} {
				p := newNibblePaletteTestBackend(t, New(), keyed, reverse, regs, 0x6004, 0x5000, 3)
				j := newNibblePaletteTestBackend(t, NewJIT(), keyed, reverse, regs, 0x6004, 0x5000, 3)
				check(t, p.WriteRegister(cpu.RegisterSP, sp))
				check(t, j.WriteRegister(cpu.RegisterSP, sp))
				assertThumbFillParity(t, p, j, 0x1000, 67)
			}
		}
	}
}

func TestThumbNibblePaletteLoopRequiresStructureAndObservationGuards(t *testing.T) {
	regs := [7]uint16{6, 0, 5, 7, 3, 2, 4}
	for _, keyed := range []bool{false, true} {
		words := nibblePaletteTestProgram(keyed, false, regs)
		for index := range words {
			b := newNibblePaletteTestBackend(t, NewJIT(), keyed, false, regs, 0x6000, 0x5000, 3)
			var changed [2]byte
			binary.LittleEndian.PutUint16(changed[:], words[index]^0x1000)
			check(t, b.WriteMemory(0x1000+uint32(index)*2, changed[:]))
			if b.classifyThumbNibblePaletteLoop(0x1000) != nil {
				t.Fatalf("changed instruction %d recognized", index)
			}
		}
		b := newNibblePaletteTestBackend(t, NewJIT(), keyed, false, regs, 0x6000, 0x5000, 3)
		loop := b.classifyThumbNibblePaletteLoop(0x1000)
		for _, flags := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}} {
			if b.accelerateThumbNibblePaletteLoop(loop, 67, flags[0], flags[1], flags[2]) != 0 {
				t.Fatal("observed execution accelerated")
			}
		}
		b.physicalAccess = true
		if b.accelerateThumbNibblePaletteLoop(loop, 67, false, false, false) != 0 {
			t.Fatal("physical execution accelerated")
		}
		b.physicalAccess = false
		b.tlb = make([]tlbEntry, nativeTLBEntries*2)
		b.tlbClear()
		if b.accelerateThumbNibblePaletteLoop(loop, 67, false, false, false) != 0 {
			t.Fatal("TLB execution accelerated")
		}
		p := newNibblePaletteTestBackend(t, New(), keyed, false, regs, 0x6000, 0x5000, 3)
		j := newNibblePaletteTestBackend(t, NewJIT(), keyed, false, regs, 0x6000, 0x5000, 3)
		p.SetPCHistoryLimit(512)
		j.SetPCHistoryLimit(512)
		assertThumbFillParity(t, p, j, 0x1000, 67)
		if j.ExecutionStatistics().AcceleratedLoopIterations != 0 {
			t.Fatal("traced execution accelerated")
		}
	}
}

func TestThumbNibblePaletteLoopStackSourceAliasesAndPaletteFaults(t *testing.T) {
	regs := [7]uint16{6, 0, 5, 7, 3, 2, 4}
	for _, keyed := range []bool{false, true} {
		for _, cursor := range []uint32{0xc028, 0xc050, 0xc088, 0xc098, 0x61ff, 0x10000} {
			p := newNibblePaletteTestBackend(t, New(), keyed, false, regs, cursor, 0x5000, 3)
			j := newNibblePaletteTestBackend(t, NewJIT(), keyed, false, regs, cursor, 0x5000, 3)
			assertThumbFillParity(t, p, j, 0x1000, 67)
		}
		for _, palette := range []uint32{0x6000, 0x6008, 0x4001, 0x8000} {
			p := newNibblePaletteTestBackend(t, New(), keyed, false, regs, 0x6000, 0x5000, 3)
			j := newNibblePaletteTestBackend(t, NewJIT(), keyed, false, regs, 0x6000, 0x5000, 3)
			var value [4]byte
			binary.LittleEndian.PutUint32(value[:], palette)
			check(t, p.WriteMemory(0x6014, value[:]))
			check(t, j.WriteMemory(0x6014, value[:]))
			assertThumbFillParity(t, p, j, 0x1000, 67)
		}
	}
}
