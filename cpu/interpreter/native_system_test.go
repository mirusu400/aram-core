//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package interpreter

import (
	"context"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

type nativeSystemBus struct {
	ram        []byte
	dataReads  int
	dataWrites int
	direct     int
	mmio       uint32
	onMMIO     func()
	invalidate func()
}

func (b *nativeSystemBus) Read(address uint32, destination []byte, permission cpu.Permissions) error {
	if permission != cpu.PermissionExecute {
		b.dataReads++
	}
	copy(destination, b.ram[int(address):int(address)+len(destination)])
	return nil
}

func (b *nativeSystemBus) Write(address uint32, source []byte, _ cpu.Permissions) error {
	b.dataWrites++
	if address == b.mmio {
		if b.onMMIO != nil {
			b.onMMIO()
		}
		return nil
	}
	copy(b.ram[int(address):int(address)+len(source)], source)
	return nil
}

func (b *nativeSystemBus) DirectMemoryRegion(
	address uint32,
	size int,
	permission cpu.Permissions,
) (cpu.DirectMemoryRegion, bool) {
	if permission&cpu.PermissionExecute != 0 || address == b.mmio ||
		uint64(address)+uint64(size) > uint64(len(b.ram)) {
		return cpu.DirectMemoryRegion{}, false
	}
	b.direct++
	return cpu.DirectMemoryRegion{
		Address: 0,
		Data:    b.ram,
		Permissions: cpu.PermissionRead | cpu.PermissionWrite |
			cpu.PermissionExecute,
	}, true
}

func (b *nativeSystemBus) SetDirectMemoryInvalidator(invalidate func()) {
	b.invalidate = invalidate
}

func newNativeSystemBackend(t *testing.T) (*Backend, *nativeSystemBus) {
	t.Helper()
	backend := NewNativeJIT()
	if backend.nativeBlocks == nil {
		backend.Close()
		t.Skip("native executable arena unavailable")
	}
	bus := &nativeSystemBus{ram: make([]byte, 0x10000), mmio: 0x9000}
	if err := backend.AttachSystemBus(bus); err != nil {
		backend.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close() })
	return backend, bus
}

func putThumb(ram []byte, address uint32, words ...uint16) {
	for index, word := range words {
		binary.LittleEndian.PutUint16(ram[int(address)+index*2:], word)
	}
}

func putARM(ram []byte, address uint32, words ...uint32) {
	for index, word := range words {
		binary.LittleEndian.PutUint32(ram[int(address)+index*4:], word)
	}
}

func armMultiInstruction(
	preIndex, increment, writeback, load bool,
	base uint32,
	registers uint16,
) uint32 {
	instruction := uint32(0xe8000000) | base<<16 | uint32(registers)
	for bit, set := range map[uint32]bool{
		24: preIndex, 23: increment, 21: writeback, 20: load,
	} {
		if set {
			instruction |= 1 << bit
		}
	}
	return instruction
}

func armSingleInstruction(
	preIndex, increment, byteTransfer, writeback, load bool,
	base, destination, offset uint32,
) uint32 {
	instruction := uint32(0xe4000000) | base<<16 | destination<<12 | offset
	for bit, set := range map[uint32]bool{
		24: preIndex, 23: increment, 22: byteTransfer, 21: writeback, 20: load,
	} {
		if set {
			instruction |= 1 << bit
		}
	}
	return instruction
}

func armHalfwordInstruction(
	preIndex, increment, immediate, writeback, load bool,
	operation uint32,
	base, destination, offset uint32,
) uint32 {
	instruction := uint32(0xe0000090) | operation<<5 | base<<16 | destination<<12
	for bit, set := range map[uint32]bool{
		24: preIndex, 23: increment, 22: immediate, 21: writeback, 20: load,
	} {
		if set {
			instruction |= 1 << bit
		}
	}
	if immediate {
		instruction |= offset&0xf | offset&0xf0<<4
	} else {
		instruction |= offset & 0xf
	}
	return instruction
}

func armRegisterShiftInstruction(shiftType, destination, value, amount uint32) uint32 {
	return 0xe1a00010 | destination<<12 | amount<<8 | shiftType<<5 | value
}

func armSingleRegisterInstruction(
	preIndex, increment, byteTransfer, writeback, load bool,
	base, destination, index, shiftType, shift uint32,
) uint32 {
	instruction := uint32(0xe6000000) | base<<16 | destination<<12 |
		shift<<7 | shiftType<<5 | index
	for bit, set := range map[uint32]bool{
		24: preIndex, 23: increment, 22: byteTransfer, 21: writeback, 20: load,
	} {
		if set {
			instruction |= 1 << bit
		}
	}
	return instruction
}

func TestNativeDispatchCacheRetainsNegativeTranslation(t *testing.T) {
	backend, _ := newNativeSystemBackend(t)
	const pc = uint32(0x1000)
	backend.nativeBlocks[pc] = nil
	if block := backend.nativeBlockAt(pc); block != nil {
		t.Fatalf("first negative lookup = %p", block)
	}

	sentinel := &nativeBlock{start: pc, end: pc + 2}
	backend.nativeBlocks[pc] = sentinel
	if block := backend.nativeBlockAt(pc); block != nil {
		t.Fatalf("negative cache miss returned map replacement %p", block)
	}

	backend.nativeGen++
	if block := backend.nativeBlockAt(pc); block != sentinel {
		t.Fatalf("generation refresh = %p, want %p", block, sentinel)
	}
}

func TestNativeDispatchCacheRetainsTwoCollidingBlocks(t *testing.T) {
	backend, _ := newNativeSystemBackend(t)
	firstPC := uint32(0x1000)
	secondPC := firstPC + 2*nativeCacheSize
	first := &nativeBlock{start: firstPC, end: firstPC + 2}
	second := &nativeBlock{start: secondPC, end: secondPC + 2}
	backend.nativeBlocks[firstPC] = first
	backend.nativeBlocks[secondPC] = second

	if backend.nativeBlockAt(firstPC) != first || backend.nativeBlockAt(secondPC) != second {
		t.Fatal("failed to populate colliding native cache ways")
	}
	delete(backend.nativeBlocks, firstPC)
	delete(backend.nativeBlocks, secondPC)

	if block := backend.nativeBlockAt(firstPC); block != first {
		t.Fatalf("first colliding lookup = %p, want %p", block, first)
	}
	if block := backend.nativeBlockAt(secondPC); block != second {
		t.Fatalf("second colliding lookup = %p, want %p", block, second)
	}
}

func TestNativeDirectBlockLinkRunsPublishedTargetWithoutDispatch(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putThumb(bus.ram, 0x1000, 0x2001, 0xe07d) // movs r0,#1; b 0x1100
	putThumb(bus.ram, 0x1100, 0x3002, 0xbe00) // adds r0,#2; bkpt
	target := backend.nativeBlockAt(0x1100)
	source := backend.nativeBlockAt(0x1000)
	if source == nil || target == nil {
		t.Fatal("failed to translate linked source/target")
	}
	if got := backend.nativeLinks[nativeLinkKey{mode: cpu.ModeThumb, pc: 0x1100}].Load(); got != target.gate {
		t.Fatalf("published gate = %#x, want %#x", got, target.gate)
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)|cpu.StatusThumb); err != nil {
		t.Fatal(err)
	}
	backend.nativeRemain = 8
	status := uint32(callNativeBlock(source.entry, &backend.regs[0], &backend.nativeRemain))
	if status != nativeStatusBKPT || backend.regs[cpu.RegisterR0] != 3 || backend.nativeRemain != 4 {
		t.Fatalf("linked call status=%#x r0=%d remain=%d, want BKPT r0=3 remain=4",
			status, backend.regs[cpu.RegisterR0], backend.nativeRemain)
	}
}

func TestNativeARMEmitsConditionsAndDirectRAMMemory(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putARM(bus.ram, 0x1000,
		0xe5902000, // ldr   r2,[r0]
		0x12822001, // addne r2,r2,#1 (skipped)
		0x02822002, // addeq r2,r2,#2
		0xe5802004, // str   r2,[r0,#4]
		0xe1200070, // bkpt
	)
	binary.LittleEndian.PutUint32(bus.ram[0x2000:], 40)
	if err := backend.WriteRegister(cpu.RegisterR0, 0x2000); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)|flagZ); err != nil {
		t.Fatal(err)
	}
	if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 5 {
		t.Fatalf("ARM native block = %#v, want five emitted instructions", block)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 16)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 5 {
		t.Fatalf("ARM native run = %+v", result)
	}
	if got := binary.LittleEndian.Uint32(bus.ram[0x2004:]); got != 42 {
		t.Fatalf("stored value = %d, want 42", got)
	}
}

func TestNativeARMEmitsImmediateRegisterShifts(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putARM(bus.ram, 0x1000,
		0xe1a00f01, // mov r0,r1,lsl #30
		0xe1a02da0, // mov r2,r0,lsr #27
		0xe0823101, // add r3,r2,r1,lsl #2
		0xe1a040e1, // mov r4,r1,ror #1
		0xe1a05021, // mov r5,r1,lsr #32
		0xe1a06040, // mov r6,r0,asr #32
		0xe1200070, // bkpt
	)
	backend.regs[cpu.RegisterR1] = 3
	if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 7 {
		t.Fatalf("ARM shifted native block = %#v, want seven instructions", block)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 16)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 7 {
		t.Fatalf("run = %+v", result)
	}
	for register, want := range map[uint32]uint32{
		cpu.RegisterR0: 0xc0000000,
		cpu.RegisterR2: 0x18,
		cpu.RegisterR3: 0x24,
		cpu.RegisterR4: 0x80000001,
		cpu.RegisterR5: 0,
		cpu.RegisterR6: 0xffffffff,
	} {
		if got := backend.regs[register]; got != want {
			t.Fatalf("r%d = %#x, want %#x", register, got, want)
		}
	}
}

func TestNativeARMLogicalImmediateShiftsCommitCarry(t *testing.T) {
	for _, test := range []struct {
		name   string
		shift  uint32
		value  uint32
		result uint32
		flags  uint32
	}{
		{name: "LSL-30", shift: 30 << 7, value: 5, result: 0x40000000, flags: flagC},
		{name: "LSR-32", shift: 1 << 5, value: 0x80000001, result: 0, flags: flagZ | flagC},
		{name: "ASR-32", shift: 2 << 5, value: 0x80000001, result: ^uint32(0), flags: flagN | flagC},
		{name: "ROR-4", shift: 4<<7 | 3<<5, value: 0x10000008, result: 0x81000000, flags: flagN | flagC},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, bus := newNativeSystemBackend(t)
			putARM(bus.ram, 0x1000,
				0xe1b00001|test.shift, // movs r0,r1,<shift>
				0xe1200070,            // bkpt
			)
			backend.regs[cpu.RegisterR1] = test.value
			backend.regs[cpu.RegisterCPSR] = uint32(processorModeSystem) | flagV
			if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 2 {
				t.Fatalf("native block = %#v, want shifted MOVS plus BKPT", block)
			}
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
				backend.regs[cpu.RegisterR0] != test.result {
				t.Fatalf("run=%+v r0=%#x, want %#x", result, backend.regs[cpu.RegisterR0], test.result)
			}
			wantFlags := test.flags | flagV
			if flags := backend.regs[cpu.RegisterCPSR] & (flagN | flagZ | flagC | flagV); flags != wantFlags {
				t.Fatalf("flags=%#x, want %#x", flags, wantFlags)
			}
		})
	}
}

func TestNativeARMEmitsMultiplyAndAccumulate(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putARM(bus.ram, 0x1000,
		0xe0010190, // mul r1,r0,r1
		0xe0211290, // mla r1,r0,r2,r1
		0xe1200070, // bkpt
	)
	backend.regs[cpu.RegisterR0] = 3
	backend.regs[cpu.RegisterR1] = 4
	backend.regs[cpu.RegisterR2] = 5
	if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 3 {
		t.Fatalf("multiply native block = %#v, want MUL/MLA plus BKPT", block)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 3)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || backend.regs[cpu.RegisterR1] != 27 {
		t.Fatalf("run=%+v r1=%d, want 27", result, backend.regs[cpu.RegisterR1])
	}

	backend, bus = newNativeSystemBackend(t)
	putARM(bus.ram, 0x1000, 0xe0110190, 0xe1200070) // muls r1,r0,r1; bkpt
	backend.regs[cpu.RegisterR0] = 0
	backend.regs[cpu.RegisterR1] = 4
	backend.regs[cpu.RegisterCPSR] = uint32(processorModeSystem) | flagC | flagV | flagN
	result = backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
	wantFlags := flagZ | flagC | flagV
	if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
		backend.regs[cpu.RegisterR1] != 0 ||
		backend.regs[cpu.RegisterCPSR]&(flagN|flagZ|flagC|flagV) != wantFlags {
		t.Fatalf("MULS run=%+v r1=%#x flags=%#x", result, backend.regs[cpu.RegisterR1],
			backend.regs[cpu.RegisterCPSR]&(flagN|flagZ|flagC|flagV))
	}
}

func TestNativeARMEmitsRegisterSpecifiedShifts(t *testing.T) {
	const value = uint32(0x80000001)
	for shiftType, operation := range []struct {
		name string
		want func(uint32, uint8, bool) (uint32, bool)
	}{
		{name: "LSL", want: shiftLSL},
		{name: "LSR", want: shiftLSR},
		{name: "ASR", want: shiftASR},
		{name: "ROR", want: shiftROR},
	} {
		for _, amount := range []uint32{0, 1, 31, 32, 33, 255} {
			t.Run(fmt.Sprintf("%s-%d", operation.name, amount), func(t *testing.T) {
				backend, bus := newNativeSystemBackend(t)
				instruction := armRegisterShiftInstruction(uint32(shiftType), cpu.RegisterR0,
					cpu.RegisterR1, cpu.RegisterR2)
				putARM(bus.ram, 0x1000,
					instruction,
					0xe1200070,
				)
				backend.regs[cpu.RegisterR1] = value
				backend.regs[cpu.RegisterR2] = amount
				if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 2 {
					t.Fatalf("native block = %#v, want MOV plus BKPT", block)
				}
				result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
				if result.Err != nil || result.Reason != cpu.StopBreakpoint {
					t.Fatalf("run = %+v", result)
				}
				want, _ := operation.want(value, uint8(amount), false)
				if got := backend.regs[cpu.RegisterR0]; got != want {
					t.Fatalf("result = %#x, want %#x", got, want)
				}
			})
		}
	}
}

func TestNativeARMEmitsCarryArithmetic(t *testing.T) {
	for _, test := range []struct {
		name        string
		instruction uint32
		left, right uint32
		carry       bool
		want        uint32
		wantFlags   uint32
	}{
		{name: "ADCS", instruction: 0xe0b10002, left: 0xffffffff, carry: true,
			want: 0, wantFlags: flagZ | flagC},
		{name: "SBCS", instruction: 0xe0d10002,
			want: 0xffffffff, wantFlags: flagN},
		{name: "RSCS", instruction: 0xe0f10002, left: 1, right: 5, carry: true,
			want: 4, wantFlags: flagC},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, bus := newNativeSystemBackend(t)
			putARM(bus.ram, 0x1000, test.instruction, 0xe1200070)
			backend.regs[cpu.RegisterR1] = test.left
			backend.regs[cpu.RegisterR2] = test.right
			if test.carry {
				backend.regs[cpu.RegisterCPSR] = flagC
			}
			if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 2 {
				t.Fatalf("native block = %#v, want arithmetic plus BKPT", block)
			}
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint {
				t.Fatalf("run = %+v", result)
			}
			if got := backend.regs[cpu.RegisterR0]; got != test.want {
				t.Fatalf("result = %#x, want %#x", got, test.want)
			}
			if got := backend.regs[cpu.RegisterCPSR] & (flagN | flagZ | flagC | flagV); got != test.wantFlags {
				t.Fatalf("flags = %#x, want %#x", got, test.wantFlags)
			}
		})
	}
}

func TestNativeARMSingleTransferWritebackModes(t *testing.T) {
	cases := []struct {
		name                 string
		preIndex, increment  bool
		load                 bool
		base, address, final uint32
	}{
		{name: "STR-post-add", increment: true, base: 0x3000, address: 0x3000, final: 0x3004},
		{name: "STR-pre-add", preIndex: true, increment: true, base: 0x3000, address: 0x3004, final: 0x3004},
		{name: "LDR-post-sub", load: true, base: 0x3004, address: 0x3004, final: 0x3000},
		{name: "LDR-pre-sub", preIndex: true, load: true, base: 0x3004, address: 0x3000, final: 0x3000},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			backend, bus := newNativeSystemBackend(t)
			putARM(bus.ram, 0x1000,
				armSingleInstruction(test.preIndex, test.increment, false, test.preIndex,
					test.load, cpu.RegisterR0, cpu.RegisterR2, 4),
				0xe1200070,
			)
			backend.regs[cpu.RegisterR0] = test.base
			if test.load {
				binary.LittleEndian.PutUint32(bus.ram[test.address:], 0x89abcdef)
			} else {
				backend.regs[cpu.RegisterR2] = 0x89abcdef
			}
			block := backend.nativeARMBlockAt(0x1000)
			if block == nil || block.count != 2 {
				t.Fatalf("native block = %#v, want transfer plus BKPT", block)
			}
			if test.load {
				if _, err := backend.read32(test.address, cpu.PermissionRead); err != nil {
					t.Fatal(err)
				}
			} else if err := backend.write32(test.address, 0, cpu.PermissionWrite); err != nil {
				t.Fatal(err)
			} else {
				binary.LittleEndian.PutUint32(bus.ram[test.address:], 0)
			}
			dataReads, dataWrites := bus.dataReads, bus.dataWrites
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 2 {
				t.Fatalf("run = %+v", result)
			}
			if backend.regs[cpu.RegisterR0] != test.final {
				t.Fatalf("base = %#x, want %#x", backend.regs[cpu.RegisterR0], test.final)
			}
			if test.load {
				if backend.regs[cpu.RegisterR2] != 0x89abcdef {
					t.Fatalf("loaded value = %#x", backend.regs[cpu.RegisterR2])
				}
			} else if got := binary.LittleEndian.Uint32(bus.ram[test.address:]); got != 0x89abcdef {
				t.Fatalf("stored value = %#x", got)
			}
			if bus.dataReads != dataReads || bus.dataWrites != dataWrites {
				t.Fatalf("native transfer reached scalar bus: reads %d->%d writes %d->%d",
					dataReads, bus.dataReads, dataWrites, bus.dataWrites)
			}
		})
	}
}

func TestNativeARMSingleTransferBailDoesNotWriteBack(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putARM(bus.ram, 0x1000,
		armSingleInstruction(false, true, false, false, true,
			cpu.RegisterR0, cpu.RegisterR2, 4),
		0xe1200070,
	)
	backend.regs[cpu.RegisterR0] = 0x3000
	backend.regs[cpu.RegisterR2] = 0xaaaaaaaa
	block := backend.nativeARMBlockAt(0x1000)
	if block == nil {
		t.Fatal("post-index LDR was not translated")
	}
	backend.nativeRemain = 8 // data TLB deliberately left cold
	status := uint32(callNativeBlock(block.entry, &backend.regs[0], &backend.nativeRemain))
	if status&0xff != nativeStatusBail || backend.regs[cpu.RegisterR0] != 0x3000 ||
		backend.regs[cpu.RegisterR2] != 0xaaaaaaaa {
		t.Fatalf("status=%#x base=%#x rd=%#x", status,
			backend.regs[cpu.RegisterR0], backend.regs[cpu.RegisterR2])
	}
}

func TestNativeARMSingleTransferShiftedRegisterOffset(t *testing.T) {
	for _, test := range []struct {
		name                       string
		load, increment, writeback bool
		base, index                uint32
		shiftType, shift           uint32
		address, final             uint32
	}{
		{name: "LDR-add-LSL", load: true, increment: true,
			base: 0x3000, index: 3, shift: 2, address: 0x300c, final: 0x3000},
		{name: "STR-sub-LSR-writeback", writeback: true,
			base: 0x3010, index: 8, shiftType: 1, shift: 1,
			address: 0x300c, final: 0x300c},
		{name: "LDR-LSR32", load: true, increment: true,
			base: 0x3000, index: 0xffffffff, shiftType: 1,
			address: 0x3000, final: 0x3000},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, bus := newNativeSystemBackend(t)
			putARM(bus.ram, 0x1000,
				armSingleRegisterInstruction(true, test.increment, false, test.writeback,
					test.load, cpu.RegisterR0, cpu.RegisterR2, cpu.RegisterR1,
					test.shiftType, test.shift),
				0xe1200070,
			)
			backend.regs[cpu.RegisterR0] = test.base
			backend.regs[cpu.RegisterR1] = test.index
			if test.load {
				binary.LittleEndian.PutUint32(bus.ram[test.address:], 0x89abcdef)
			} else {
				backend.regs[cpu.RegisterR2] = 0x89abcdef
			}
			block := backend.nativeARMBlockAt(0x1000)
			if block == nil || block.count != 2 {
				t.Fatalf("native block = %#v, want transfer plus BKPT", block)
			}
			if test.load {
				if _, err := backend.read32(test.address, cpu.PermissionRead); err != nil {
					t.Fatal(err)
				}
			} else if err := backend.write32(test.address, 0, cpu.PermissionWrite); err != nil {
				t.Fatal(err)
			} else {
				binary.LittleEndian.PutUint32(bus.ram[test.address:], 0)
			}
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint {
				t.Fatalf("run = %+v", result)
			}
			if backend.regs[cpu.RegisterR0] != test.final {
				t.Fatalf("base = %#x, want %#x", backend.regs[cpu.RegisterR0], test.final)
			}
			if test.load {
				if backend.regs[cpu.RegisterR2] != 0x89abcdef {
					t.Fatalf("loaded = %#x", backend.regs[cpu.RegisterR2])
				}
			} else if got := binary.LittleEndian.Uint32(bus.ram[test.address:]); got != 0x89abcdef {
				t.Fatalf("stored = %#x", got)
			}
		})
	}
}

func TestNativeARMHalfwordAndSignedTransfers(t *testing.T) {
	cases := []struct {
		name      string
		load      bool
		operation uint32
		preIndex  bool
		offset    uint32
		address   uint32
		final     uint32
		input     uint32
		want      uint32
	}{
		{name: "STRH", operation: 1, preIndex: true, offset: 2,
			address: 0x3002, final: 0x3000, input: 0x1234abcd, want: 0xabcd},
		{name: "LDRH-post", load: true, operation: 1, offset: 2,
			address: 0x3000, final: 0x3002, input: 0xabcd, want: 0xabcd},
		{name: "LDRSB", load: true, operation: 2, preIndex: true, offset: 1,
			address: 0x3001, final: 0x3000, input: 0x80, want: 0xffffff80},
		{name: "LDRSH", load: true, operation: 3, preIndex: true, offset: 2,
			address: 0x3002, final: 0x3000, input: 0x8001, want: 0xffff8001},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			backend, bus := newNativeSystemBackend(t)
			putARM(bus.ram, 0x1000,
				armHalfwordInstruction(test.preIndex, true, true, false, test.load,
					test.operation, cpu.RegisterR0, cpu.RegisterR2, test.offset),
				0xe1200070,
			)
			backend.regs[cpu.RegisterR0] = 0x3000
			if test.load {
				if test.operation == 2 {
					bus.ram[test.address] = byte(test.input)
				} else {
					binary.LittleEndian.PutUint16(bus.ram[test.address:], uint16(test.input))
				}
			} else {
				backend.regs[cpu.RegisterR2] = test.input
			}
			block := backend.nativeARMBlockAt(0x1000)
			if block == nil || block.count != 2 {
				t.Fatalf("native block = %#v, want transfer plus BKPT", block)
			}
			if test.load {
				if test.operation == 2 {
					if _, err := backend.read8(test.address, cpu.PermissionRead); err != nil {
						t.Fatal(err)
					}
				} else if _, err := backend.read16(test.address, cpu.PermissionRead); err != nil {
					t.Fatal(err)
				}
			} else if err := backend.write16(test.address, 0, cpu.PermissionWrite); err != nil {
				t.Fatal(err)
			} else {
				binary.LittleEndian.PutUint16(bus.ram[test.address:], 0)
			}
			dataReads, dataWrites := bus.dataReads, bus.dataWrites
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 2 {
				t.Fatalf("run = %+v", result)
			}
			if backend.regs[cpu.RegisterR0] != test.final {
				t.Fatalf("base = %#x, want %#x", backend.regs[cpu.RegisterR0], test.final)
			}
			if test.load {
				if backend.regs[cpu.RegisterR2] != test.want {
					t.Fatalf("loaded value = %#x, want %#x", backend.regs[cpu.RegisterR2], test.want)
				}
			} else if got := uint32(binary.LittleEndian.Uint16(bus.ram[test.address:])); got != test.want {
				t.Fatalf("stored value = %#x, want %#x", got, test.want)
			}
			if bus.dataReads != dataReads || bus.dataWrites != dataWrites {
				t.Fatalf("native transfer reached scalar bus: reads %d->%d writes %d->%d",
					dataReads, bus.dataReads, dataWrites, bus.dataWrites)
			}
		})
	}
}

func TestNativeARMMultiTransferAddressingModes(t *testing.T) {
	modes := []struct {
		name                string
		preIndex, increment bool
		start, final        uint32
	}{
		{name: "IA", increment: true, start: 0x3000, final: 0x3008},
		{name: "IB", preIndex: true, increment: true, start: 0x3004, final: 0x3008},
		{name: "DA", start: 0x2ffc, final: 0x2ff8},
		{name: "DB", preIndex: true, start: 0x2ff8, final: 0x2ff8},
	}
	const registers = uint16(1<<cpu.RegisterR1 | 1<<cpu.RegisterR3)
	for _, mode := range modes {
		for _, load := range []bool{false, true} {
			operation := "STM"
			if load {
				operation = "LDM"
			}
			t.Run(operation+mode.name, func(t *testing.T) {
				backend, bus := newNativeSystemBackend(t)
				putARM(bus.ram, 0x1000,
					armMultiInstruction(mode.preIndex, mode.increment, true, load,
						cpu.RegisterR4, registers),
					0xe1200070, // bkpt
				)
				backend.regs[cpu.RegisterR4] = 0x3000
				if load {
					binary.LittleEndian.PutUint32(bus.ram[mode.start:], 0x11223344)
					binary.LittleEndian.PutUint32(bus.ram[mode.start+4:], 0xaabbccdd)
				} else {
					backend.regs[cpu.RegisterR1] = 0x11223344
					backend.regs[cpu.RegisterR3] = 0xaabbccdd
				}
				block := backend.nativeARMBlockAt(0x1000)
				if block == nil || block.count != 2 {
					t.Fatalf("native block = %#v, want LDM/STM plus BKPT", block)
				}
				// Translation grows the executable span and clears the write TLB,
				// so warm the relevant data half only after the block exists.
				if load {
					if _, err := backend.read32(mode.start, cpu.PermissionRead); err != nil {
						t.Fatal(err)
					}
				} else if err := backend.write32(mode.start, 0, cpu.PermissionWrite); err != nil {
					t.Fatal(err)
				} else {
					binary.LittleEndian.PutUint32(bus.ram[mode.start:], 0)
				}
				dataReads, dataWrites := bus.dataReads, bus.dataWrites
				result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
				if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 2 {
					t.Fatalf("native %s%s run = %+v", operation, mode.name, result)
				}
				if backend.regs[cpu.RegisterR4] != mode.final {
					t.Fatalf("base = %#x, want %#x", backend.regs[cpu.RegisterR4], mode.final)
				}
				if load {
					if backend.regs[cpu.RegisterR1] != 0x11223344 ||
						backend.regs[cpu.RegisterR3] != 0xaabbccdd {
						t.Fatalf("loaded r1=%#x r3=%#x", backend.regs[cpu.RegisterR1], backend.regs[cpu.RegisterR3])
					}
				} else if got0, got1 :=
					binary.LittleEndian.Uint32(bus.ram[mode.start:]),
					binary.LittleEndian.Uint32(bus.ram[mode.start+4:]); got0 != 0x11223344 || got1 != 0xaabbccdd {
					t.Fatalf("stored words = %#x %#x", got0, got1)
				}
				if bus.dataReads != dataReads || bus.dataWrites != dataWrites {
					t.Fatalf("native transfer reached scalar bus: reads %d->%d writes %d->%d",
						dataReads, bus.dataReads, dataWrites, bus.dataWrites)
				}
			})
		}
	}
}

func TestNativeARMLDMSuppressesWritebackWhenBaseIsLoaded(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	const registers = uint16(1<<cpu.RegisterR1 | 1<<cpu.RegisterR2)
	putARM(bus.ram, 0x1000,
		armMultiInstruction(false, true, true, true, cpu.RegisterR1, registers),
		0xe1200070,
	)
	binary.LittleEndian.PutUint32(bus.ram[0x3000:], 0x55667788)
	binary.LittleEndian.PutUint32(bus.ram[0x3004:], 0x99aabbcc)
	backend.regs[cpu.RegisterR1] = 0x3000
	if backend.nativeARMBlockAt(0x1000) == nil {
		t.Fatal("LDM with base in list was not translated")
	}
	if _, err := backend.read32(0x3000, cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("run = %+v", result)
	}
	if backend.regs[cpu.RegisterR1] != 0x55667788 || backend.regs[cpu.RegisterR2] != 0x99aabbcc {
		t.Fatalf("loaded r1=%#x r2=%#x", backend.regs[cpu.RegisterR1], backend.regs[cpu.RegisterR2])
	}
}

func TestNativeARMLDMLoadedPCBranchExchanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		target uint32
		thumb  bool
	}{
		{name: "ARM", target: 0x1100},
		{name: "Thumb", target: 0x1101, thumb: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, bus := newNativeSystemBackend(t)
			const registers = uint16(1<<cpu.RegisterR0 | 1<<cpu.RegisterPC)
			putARM(bus.ram, 0x1000,
				armMultiInstruction(false, true, true, true, cpu.RegisterR1, registers),
			)
			if test.thumb {
				putThumb(bus.ram, 0x1100, 0x3002, 0xbe00) // adds r0,#2; bkpt
			} else {
				putARM(bus.ram, 0x1100, 0xe2800002, 0xe1200070) // add r0,#2; bkpt
			}
			binary.LittleEndian.PutUint32(bus.ram[0x3000:], 1)
			binary.LittleEndian.PutUint32(bus.ram[0x3004:], test.target)
			backend.regs[cpu.RegisterR1] = 0x3000
			if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 1 {
				t.Fatalf("LDM-to-PC native block = %#v, want one terminator", block)
			}
			// Populate the native read TLB after translation clears it.
			if _, err := backend.read32(0x3000, cpu.PermissionRead); err != nil {
				t.Fatal(err)
			}
			dataReads := bus.dataReads
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
				result.Instructions != 3 || backend.regs[cpu.RegisterR0] != 3 ||
				backend.regs[cpu.RegisterR1] != 0x3008 {
				t.Fatalf("run=%+v r0=%d r1=%#x", result, backend.regs[cpu.RegisterR0], backend.regs[cpu.RegisterR1])
			}
			if bus.dataReads != dataReads {
				t.Fatalf("native LDM reached scalar bus: reads %d->%d", dataReads, bus.dataReads)
			}
			wantT := uint32(0)
			if test.thumb {
				wantT = cpu.StatusThumb
			}
			if got := backend.regs[cpu.RegisterCPSR] & cpu.StatusThumb; got != wantT {
				t.Fatalf("CPSR.T=%#x, want %#x", got, wantT)
			}
		})
	}
}

func TestNativeARMConditionalLDMPCFallsThroughWithoutAccess(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	const registers = uint16(1<<cpu.RegisterR0 | 1<<cpu.RegisterPC)
	instruction := armMultiInstruction(false, true, true, true, cpu.RegisterR1, registers)
	instruction = instruction&0x0fffffff | 0x10000000 // ldmne r1!,{r0,pc}
	putARM(bus.ram, 0x1000,
		instruction,
		0xe2800004, // add r0,r0,#4
		0xe1200070, // bkpt
	)
	backend.regs[cpu.RegisterR0] = 1
	backend.regs[cpu.RegisterR1] = bus.mmio
	backend.regs[cpu.RegisterCPSR] = uint32(processorModeSystem) | flagZ
	if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 1 {
		t.Fatalf("conditional LDM-to-PC block = %#v, want one terminator", block)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
		result.Instructions != 3 || backend.regs[cpu.RegisterR0] != 5 ||
		backend.regs[cpu.RegisterR1] != bus.mmio || bus.dataReads != 0 {
		t.Fatalf("run=%+v r0=%d r1=%#x reads=%d",
			result, backend.regs[cpu.RegisterR0], backend.regs[cpu.RegisterR1], bus.dataReads)
	}
}

func TestNativeARMMultiTransferBailsBeforeCrossingPage(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	const registers = uint16(1<<cpu.RegisterR1 | 1<<cpu.RegisterR3)
	putARM(bus.ram, 0x1000,
		armMultiInstruction(false, true, true, true, cpu.RegisterR4, registers),
		0xe1200070,
	)
	backend.regs[cpu.RegisterR1] = 0xaaaaaaaa
	backend.regs[cpu.RegisterR3] = 0xbbbbbbbb
	backend.regs[cpu.RegisterR4] = 0x2ffc
	binary.LittleEndian.PutUint32(bus.ram[0x2ffc:], 0x11223344)
	binary.LittleEndian.PutUint32(bus.ram[0x3000:], 0x55667788)
	block := backend.nativeARMBlockAt(0x1000)
	if block == nil {
		t.Fatal("cross-page LDM was not translated")
	}
	if _, err := backend.read32(0x2ffc, cpu.PermissionRead); err != nil {
		t.Fatal(err)
	}
	backend.nativeRemain = 8
	status := uint32(callNativeBlock(block.entry, &backend.regs[0], &backend.nativeRemain))
	if status&0xff != nativeStatusBail || backend.nativeBailAddress != 0x2ffc ||
		backend.regs[cpu.RegisterPC] != 0x1000 {
		t.Fatalf("status=%#x bailAddress=%#x pc=%#x", status,
			backend.nativeBailAddress, backend.regs[cpu.RegisterPC])
	}
	if backend.regs[cpu.RegisterR1] != 0xaaaaaaaa || backend.regs[cpu.RegisterR3] != 0xbbbbbbbb {
		t.Fatal("cross-page native path partially modified registers before bailing")
	}
}

func TestNativeARMMultiTransferColdMissWithFullListBails(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putARM(bus.ram, 0x1000,
		armMultiInstruction(false, true, false, true, cpu.RegisterR4, 0x7fff),
		0xe1200070,
	)
	backend.regs[cpu.RegisterR4] = 0x3000
	block := backend.nativeARMBlockAt(0x1000)
	if block == nil {
		t.Fatal("full-list LDM was not translated")
	}
	// Leave the data TLB cold. With fifteen emitted loads the bailout is well
	// beyond an x86 rel8 branch, so this also guards the rel32 miss patching.
	backend.nativeRemain = 8
	status := uint32(callNativeBlock(block.entry, &backend.regs[0], &backend.nativeRemain))
	if status&0xff != nativeStatusBail || backend.nativeBailAddress != 0x3000 ||
		backend.regs[cpu.RegisterPC] != 0x1000 {
		t.Fatalf("status=%#x bailAddress=%#x pc=%#x", status,
			backend.nativeBailAddress, backend.regs[cpu.RegisterPC])
	}
}

func TestNativeARMMultiTransferColdMissPrimesTLB(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	const registers = uint16(1<<cpu.RegisterR1 | 1<<cpu.RegisterR3)
	putARM(bus.ram, 0x1000,
		armMultiInstruction(false, true, false, true, cpu.RegisterR4, registers),
		0xe1200070,
	)
	binary.LittleEndian.PutUint32(bus.ram[0x3000:], 0x11223344)
	binary.LittleEndian.PutUint32(bus.ram[0x3004:], 0x55667788)
	backend.regs[cpu.RegisterR4] = 0x3000
	block := backend.nativeARMBlockAt(0x1000)
	if block == nil {
		t.Fatal("LDM was not translated")
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint {
		t.Fatalf("cold recovery run = %+v", result)
	}
	if !backend.tlbHit(0x3000, cpu.PermissionRead) {
		t.Fatal("portable block-transfer recovery did not populate the native TLB")
	}
	backend.regs[cpu.RegisterR1] = 0
	backend.regs[cpu.RegisterR3] = 0
	backend.regs[cpu.RegisterR4] = 0x3000
	backend.nativeRemain = 8
	status := uint32(callNativeBlock(block.entry, &backend.regs[0], &backend.nativeRemain))
	if status != nativeStatusBKPT || backend.regs[cpu.RegisterR1] != 0x11223344 ||
		backend.regs[cpu.RegisterR3] != 0x55667788 {
		t.Fatalf("warm native retry status=%#x r1=%#x r3=%#x", status,
			backend.regs[cpu.RegisterR1], backend.regs[cpu.RegisterR3])
	}
}

func TestNativeARMDirectBlockLinkRunsPublishedTargetWithoutDispatch(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putARM(bus.ram, 0x1000,
		0xe3a00001, // mov r0,#1
		0xea00003d, // b 0x1100
	)
	putARM(bus.ram, 0x1100,
		0xe2800002, // add r0,r0,#2
		0xe1200070, // bkpt
	)
	target := backend.nativeARMBlockAt(0x1100)
	source := backend.nativeARMBlockAt(0x1000)
	if source == nil || target == nil {
		t.Fatal("failed to translate linked ARM source/target")
	}
	if got := backend.nativeLinks[nativeLinkKey{mode: cpu.ModeARM, pc: 0x1100}].Load(); got != target.gate {
		t.Fatalf("published gate = %#x, want %#x", got, target.gate)
	}
	backend.nativeRemain = 8
	status := uint32(callNativeBlock(source.entry, &backend.regs[0], &backend.nativeRemain))
	if status != nativeStatusBKPT || backend.regs[cpu.RegisterR0] != 3 || backend.nativeRemain != 4 {
		t.Fatalf("linked call status=%#x r0=%d remain=%d, want BKPT r0=3 remain=4",
			status, backend.regs[cpu.RegisterR0], backend.nativeRemain)
	}
}

func TestNativeARMRegisterBranchExchange(t *testing.T) {
	for _, test := range []struct {
		name        string
		instruction uint32
		target      uint32
		thumb       bool
		wantLink    uint32
	}{
		{name: "BX-ARM", instruction: 0xe12fff11, target: 0x1100},
		{name: "BX-Thumb", instruction: 0xe12fff11, target: 0x1101, thumb: true},
		{name: "BLX-Thumb", instruction: 0xe12fff31, target: 0x1101, thumb: true, wantLink: 0x1004},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, bus := newNativeSystemBackend(t)
			putARM(bus.ram, 0x1000, test.instruction)
			if test.thumb {
				putThumb(bus.ram, 0x1100, 0x3002, 0xbe00) // adds r0,#2; bkpt
			} else {
				putARM(bus.ram, 0x1100, 0xe2800002, 0xe1200070) // add r0,#2; bkpt
			}
			backend.regs[cpu.RegisterR0] = 1
			backend.regs[cpu.RegisterR1] = test.target
			if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 1 {
				t.Fatalf("BX native block = %#v, want one terminator", block)
			}
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 16)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
				result.Instructions != 3 || backend.regs[cpu.RegisterR0] != 3 {
				t.Fatalf("run = %+v r0=%d", result, backend.regs[cpu.RegisterR0])
			}
			if backend.regs[cpu.RegisterLR] != test.wantLink {
				t.Fatalf("lr = %#x, want %#x", backend.regs[cpu.RegisterLR], test.wantLink)
			}
			wantT := uint32(0)
			if test.thumb {
				wantT = cpu.StatusThumb
			}
			if got := backend.regs[cpu.RegisterCPSR] & cpu.StatusThumb; got != wantT {
				t.Fatalf("CPSR.T = %#x, want %#x", got, wantT)
			}
		})
	}
}

func TestNativeARMConditionalBranchExchange(t *testing.T) {
	for _, test := range []struct {
		name      string
		cpsr      uint32
		wantValue uint32
	}{
		{name: "taken", cpsr: uint32(processorModeSystem), wantValue: 3},
		{name: "fallthrough", cpsr: uint32(processorModeSystem) | flagZ, wantValue: 5},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, bus := newNativeSystemBackend(t)
			putARM(bus.ram, 0x1000,
				0x112fff11, // bxne r1
				0xe2800004, // add r0,r0,#4
				0xe1200070, // bkpt
			)
			putARM(bus.ram, 0x1100,
				0xe2800002, // add r0,r0,#2
				0xe1200070, // bkpt
			)
			backend.regs[cpu.RegisterR0] = 1
			backend.regs[cpu.RegisterR1] = 0x1100
			backend.regs[cpu.RegisterCPSR] = test.cpsr
			if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 1 {
				t.Fatalf("conditional BX block = %#v, want one terminator", block)
			}
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
				result.Instructions != 3 || backend.regs[cpu.RegisterR0] != test.wantValue {
				t.Fatalf("run=%+v r0=%d, want %d", result, backend.regs[cpu.RegisterR0], test.wantValue)
			}
		})
	}
}

func TestNativeARMConditionalBranchLink(t *testing.T) {
	for _, test := range []struct {
		name      string
		cpsr      uint32
		wantValue uint32
		wantLink  uint32
	}{
		{name: "taken", cpsr: uint32(processorModeSystem), wantValue: 3, wantLink: 0x1004},
		{name: "fallthrough", cpsr: uint32(processorModeSystem) | flagZ, wantValue: 5, wantLink: 0xfeedface},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend, bus := newNativeSystemBackend(t)
			putARM(bus.ram, 0x1000,
				0x1b00003e, // blne 0x1100
				0xe2800004, // add r0,r0,#4
				0xe1200070, // bkpt
			)
			putARM(bus.ram, 0x1100,
				0xe2800002, // add r0,r0,#2
				0xe1200070, // bkpt
			)
			backend.regs[cpu.RegisterR0] = 1
			backend.regs[cpu.RegisterLR] = 0xfeedface
			backend.regs[cpu.RegisterCPSR] = test.cpsr
			if block := backend.nativeARMBlockAt(0x1000); block == nil || block.count != 1 {
				t.Fatalf("conditional BL block = %#v, want one terminator", block)
			}
			result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 8)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
				result.Instructions != 3 || backend.regs[cpu.RegisterR0] != test.wantValue ||
				backend.regs[cpu.RegisterLR] != test.wantLink {
				t.Fatalf("run=%+v r0=%d lr=%#x", result, backend.regs[cpu.RegisterR0], backend.regs[cpu.RegisterLR])
			}
		})
	}
}

func TestNativePersistentMMIOBailBecomesInterpreterBoundary(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putThumb(bus.ram, 0x1000, 0x6008, 0xe7fd) // str r0,[r1]; b 0x1000
	if err := backend.WriteRegister(cpu.RegisterR1, bus.mmio); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 12)
	if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != 12 {
		t.Fatalf("MMIO loop = %+v", result)
	}
	if !backend.nativeSlowAt(cpu.ModeThumb, 0x1000) {
		t.Fatal("persistent MMIO instruction was not promoted to an interpreter boundary")
	}
	if backend.nativeBailAddress != bus.mmio {
		t.Fatalf("recorded bail address = %#x, want MMIO %#x", backend.nativeBailAddress, bus.mmio)
	}
	if block := backend.nativeBlocks[0x1000]; block != nil {
		t.Fatalf("slow MMIO PC retained native block %#v", block)
	}
	if bus.dataWrites != 6 {
		t.Fatalf("MMIO writes = %d, want 6", bus.dataWrites)
	}
}

func TestNativeConditionalSlowARMOnlyExitsWhenConditionPasses(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putARM(bus.ram, 0x1000,
		0x15810000, // strne r0,[r1]
		0xe1200070, // bkpt
	)
	backend.nativeSlow[nativeLinkKey{mode: cpu.ModeARM, pc: 0x1000}] = nativeSlowState{
		address: bus.mmio,
		count:   nativeSlowThreshold,
	}
	backend.regs[cpu.RegisterR0] = 0x12345678
	backend.regs[cpu.RegisterR1] = bus.mmio
	block := backend.nativeARMBlockAt(0x1000)
	if block == nil || block.count != 2 {
		t.Fatalf("conditional slow ARM block = %#v, want two instructions", block)
	}

	// NE is false: count the skipped instruction and stay in native code.
	backend.regs[cpu.RegisterCPSR] = uint32(processorModeSystem) | flagZ
	backend.nativeRemain = 2
	status := uint32(callNativeBlock(block.entry, &backend.regs[0], &backend.nativeRemain))
	if status != nativeStatusBKPT || backend.nativeRemain != 0 || bus.dataWrites != 0 {
		t.Fatalf("false condition status=%#x remain=%d writes=%d, want BKPT/0/0",
			status, backend.nativeRemain, bus.dataWrites)
	}

	// NE is true: return at the instruction boundary without touching MMIO;
	// the Run loop will execute this one instruction through the portable tier.
	backend.regs[cpu.RegisterPC] = 0x1000
	backend.regs[cpu.RegisterCPSR] = uint32(processorModeSystem)
	backend.nativeRemain = 2
	status = uint32(callNativeBlock(block.entry, &backend.regs[0], &backend.nativeRemain))
	if status != nativeSlowStatus(0) || backend.regs[cpu.RegisterPC] != 0x1000 ||
		backend.nativeRemain != 0 || bus.dataWrites != 0 {
		t.Fatalf("true condition status=%#x pc=%#x remain=%d writes=%d",
			status, backend.regs[cpu.RegisterPC], backend.nativeRemain, bus.dataWrites)
	}

	backend.regs[cpu.RegisterCPSR] = uint32(processorModeSystem)
	result := backend.Run(context.Background(), 0x1000, cpu.ModeARM, 2)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint ||
		result.Instructions != 2 || bus.dataWrites != 1 {
		t.Fatalf("slow-boundary run=%+v writes=%d, want breakpoint after one MMIO write",
			result, bus.dataWrites)
	}
}

func TestNativeRangeInvalidationPreservesUnrelatedBlocksAndLinks(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putThumb(bus.ram, 0x1000, 0x2001, 0xbe00)
	putThumb(bus.ram, 0x2000, 0x2002, 0xbe00)
	first := backend.nativeBlockAt(0x1000)
	second := backend.nativeBlockAt(0x2000)
	if first == nil || second == nil {
		t.Fatal("failed to translate range-invalidation fixtures")
	}
	// A negative cache entry inside the translated hull is dropped like a real
	// block, so changed code gets a fresh translation attempt.
	backend.cacheNativeBlock(0x1010, nil)
	backend.invalidateTranslationRange(0x1000, 2)
	if _, ok := backend.nativeBlocks[0x1000]; ok {
		t.Fatal("overlapping block survived range invalidation")
	}
	if got := backend.nativeBlocks[0x2000]; got != second {
		t.Fatal("unrelated block was discarded")
	}
	if got := backend.nativeLinks[nativeLinkKey{mode: cpu.ModeThumb, pc: 0x1000}].Load(); got != 0 {
		t.Fatalf("invalidated target link = %#x, want zero", got)
	}
	if got := backend.nativeLinks[nativeLinkKey{mode: cpu.ModeThumb, pc: 0x2000}].Load(); got != second.gate {
		t.Fatalf("unrelated target link = %#x, want %#x", got, second.gate)
	}
	backend.invalidateTranslationRange(0x1010, 2)
	if _, ok := backend.nativeBlocks[0x1010]; ok {
		t.Fatal("overlapping cached native fallback survived range invalidation")
	}
}

// Whole-system guests run CP15 c7,c5,1 as a loop over every line of a buffer
// they have just filled. Walking the block maps for a range that holds no
// translated code would make that loop cost O(blocks) per 32 bytes, so the
// range invalidator has to short-circuit on the same conservative hull and
// code-page bitmap that self-modifying-write detection uses.
func TestNativeRangeInvalidationSkipsRangesHoldingNoTranslatedCode(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putThumb(bus.ram, 0x1000, 0x2001, 0xbe00)
	putThumb(bus.ram, 0x7000, 0x2002, 0xbe00)
	low := backend.nativeBlockAt(0x1000)
	high := backend.nativeBlockAt(0x7000)
	if low == nil || high == nil {
		t.Fatal("failed to translate the hull fixtures")
	}
	// The hull now spans 0x1000..0x7004 with only pages 1 and 7 marked, so the
	// two guards can be exercised apart: 0x4000 sits inside the hull on an
	// unmarked page, 0xf000 sits past nativeCodeHi entirely. Whether the walk
	// ran is observable through a negative cache entry, which a walk deletes.
	backend.cacheNativeBlock(0x4000, nil)
	backend.cacheNativeBlock(0xf000, nil)
	generation := backend.nativeGen

	backend.invalidateTranslationRange(0x4000, instructionCacheLineSize)
	if _, ok := backend.nativeBlocks[0x4000]; !ok {
		t.Fatal("maintenance on an unmarked page inside the hull walked the block maps")
	}
	backend.invalidateTranslationRange(0xf000, instructionCacheLineSize)
	if _, ok := backend.nativeBlocks[0xf000]; !ok {
		t.Fatal("maintenance past nativeCodeHi walked the block maps")
	}
	if backend.nativeGen != generation {
		t.Fatal("maintenance of an unrelated range advanced the dispatch generation")
	}
	if got := backend.nativeBlocks[0x1000]; got != low {
		t.Fatal("maintenance of an unrelated range discarded a translated block")
	}
	if got := backend.nativeLinks[nativeLinkKey{mode: cpu.ModeThumb, pc: 0x1000}].Load(); got != low.gate {
		t.Fatalf("unrelated maintenance cleared a published link: %#x", got)
	}

	// The line that actually holds a block still invalidates.
	backend.invalidateTranslationRange(0x7000, instructionCacheLineSize)
	if _, ok := backend.nativeBlocks[0x7000]; ok {
		t.Fatal("maintenance of the translated line kept the block")
	}
}

// Link slots outlive the blocks that published them: nativeInvalidate can only
// zero them, because its other caller is an in-progress translation holding
// their addresses. A full translation flush is the safe point to reclaim the
// map, and without that it grows one entry per branch target for the life of
// the backend.
func TestNativeFullInvalidationReclaimsLinkSlots(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putThumb(bus.ram, 0x1000, 0x2001, 0xe07d) // movs r0,#1; b 0x1100
	putThumb(bus.ram, 0x1100, 0x3002, 0xbe00) // adds r0,#2; bkpt
	if backend.nativeBlockAt(0x1100) == nil || backend.nativeBlockAt(0x1000) == nil {
		t.Fatal("failed to translate the linked fixtures")
	}
	if len(backend.nativeLinks) == 0 {
		t.Fatal("translation published no link slots")
	}

	if err := backend.SetExecutionTraps(nil); err != nil {
		t.Fatal(err)
	}
	if len(backend.nativeLinks) != 0 {
		t.Fatalf("full invalidation retained %d link slots", len(backend.nativeLinks))
	}
	if len(backend.nativeBlocks) != 0 || len(backend.nativeBlockPages) != 0 {
		t.Fatal("full invalidation retained blocks or their page index")
	}
}

// Invalidation reaches the block maps through a per-page index, so a write or
// a maintained line only examines the blocks that could actually overlap it.
// Being page-scoped must not cost precision in either direction.
func TestNativeRangeInvalidationIsScopedToTheMaintainedPage(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	starts := []uint32{0x1000, 0x1100, 0x2000, 0x3000}
	translated := make(map[uint32]*nativeBlock, len(starts))
	for _, pc := range starts {
		putThumb(bus.ram, pc, 0x2001, 0xbe00)
		block := backend.nativeBlockAt(pc)
		if block == nil {
			t.Fatalf("failed to translate the fixture at %#x", pc)
		}
		translated[pc] = block
	}

	backend.invalidateTranslationRange(0x1000, instructionCacheLineSize)
	if _, ok := backend.nativeBlocks[0x1000]; ok {
		t.Fatal("the block inside the maintained line survived")
	}
	// 0x1100 shares a page with the maintained line but not the line itself,
	// and the other two are on different pages entirely.
	for _, pc := range []uint32{0x1100, 0x2000, 0x3000} {
		if got := backend.nativeBlocks[pc]; got != translated[pc] {
			t.Fatalf("block at %#x was discarded by unrelated maintenance", pc)
		}
	}
}

// A block is registered only under the page its start PC lies on, so a scan has
// to look one page back to catch a block that reaches across the boundary.
// maxTranslatedBlockBytes is what makes a single page of lookback exact.
func TestNativePageIndexFindsBlockReachingAcrossAPageBoundary(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	const start = 2*codePageSize - 4
	putThumb(bus.ram, start, 0x2001, 0x3002, 0xbe00)
	block := backend.nativeBlockAt(start)
	if block == nil || block.end <= 2*codePageSize {
		t.Fatalf("fixture block %+v does not reach past the page boundary", block)
	}
	backend.invalidateTranslationRange(2*codePageSize, instructionCacheLineSize)
	if _, ok := backend.nativeBlocks[start]; ok {
		t.Fatal("a block reaching into the maintained page was not invalidated")
	}
}

func TestNativeWholeSystemRunsDirectRAMAndMMUDataInline(t *testing.T) {
	for _, mmu := range []bool{false, true} {
		t.Run(map[bool]string{false: "physical", true: "mmu-identity"}[mmu], func(t *testing.T) {
			backend, bus := newNativeSystemBackend(t)
			// loop: ldr r2,[r0]; adds r2,#1; str r2,[r0]; subs r1,#1; bne loop; bkpt
			putThumb(bus.ram, 0x1000, 0x6802, 0x3201, 0x6002, 0x3901, 0xd1fa, 0xbe00)
			binary.LittleEndian.PutUint32(bus.ram[0x2000:], 41)
			if mmu {
				// One manager-domain identity section covers code, data, vectors,
				// and the translation table itself.
				binary.LittleEndian.PutUint32(bus.ram[0x4000:], 0x00000c02)
				if err := backend.writeCP15(2, 0, 0, 0x4000); err != nil {
					t.Fatal(err)
				}
				if err := backend.writeCP15(3, 0, 0, 3); err != nil {
					t.Fatal(err)
				}
				if err := backend.writeCP15(1, 0, 0, 1); err != nil {
					t.Fatal(err)
				}
			}
			for register, value := range map[uint32]uint32{
				cpu.RegisterR0: 0x2000,
				cpu.RegisterR1: 3,
			} {
				if err := backend.WriteRegister(register, value); err != nil {
					t.Fatal(err)
				}
			}
			result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 64)
			if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 16 {
				t.Fatalf("run = %+v", result)
			}
			if got := binary.LittleEndian.Uint32(bus.ram[0x2000:]); got != 44 {
				t.Fatalf("RAM value = %d, want 44", got)
			}
			if bus.dataReads != 0 || bus.dataWrites != 0 {
				t.Fatalf("ordinary RAM bus calls = read %d write %d", bus.dataReads, bus.dataWrites)
			}
			if len(backend.nativeBlocks) == 0 {
				t.Fatal("whole-system Thumb run retained no native blocks")
			}
			for key, state := range backend.nativeSlow {
				if state.count != 0 {
					t.Fatalf("cold RAM bail at %+v counted as persistent: %+v", key, state)
				}
			}
			if mmu {
				for address := uint32(0x2400); address < 0x3000; address += 0x400 {
					key := address >> 10
					entry := backend.mmuTLBTable[key&(mmuTLBEntries-1)]
					if entry.valid && entry.gen == backend.mappingGen && entry.tag == key {
						t.Fatalf("native 1 KiB validation polluted architectural MMU TLB at %#x", address)
					}
				}
			}
		})
	}
}

func TestNativeWholeSystemExecutionTrapSplitsBlock(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putThumb(bus.ram, 0x1000, 0x2001, 0x3001, 0xbe00) // movs r0,#1; adds r0,#1; bkpt
	if err := backend.SetExecutionTraps([]cpu.ExecutionTrap{{
		Address: 0x1002,
		Mode:    cpu.ModeThumb,
	}}); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 8)
	if result.Err != nil || result.Reason != cpu.StopExecutionTrap ||
		result.Instructions != 1 || result.PC != 0x1002 {
		t.Fatalf("trap run = %+v", result)
	}
	if got := register(t, backend, cpu.RegisterR0); got != 1 {
		t.Fatalf("r0 = %d, want 1 before trapped add", got)
	}
}

func TestNativeWholeSystemEmitsInterruptPoll(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putThumb(bus.ram, 0x1000, 0x2001, 0xbe00) // movs r0,#1; bkpt
	block := backend.translateNativeBlock(0x1000)
	if block == nil {
		t.Fatal("failed to translate system block")
	}
	if err := backend.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)|cpu.StatusThumb); err != nil {
		t.Fatal(err)
	}
	if err := backend.SetInterruptLine(cpu.InterruptIRQ, true); err != nil {
		t.Fatal(err)
	}
	backend.nativeRemain = 8
	status := uint32(callNativeBlock(block.entry, &backend.regs[0], &backend.nativeRemain))
	if status != nativeInterruptStatus(0) || backend.regs[cpu.RegisterPC] != 0x1000 {
		t.Fatalf("poll status=%#x pc=%#x, want status=%#x pc=0x1000",
			status, backend.regs[cpu.RegisterPC], nativeInterruptStatus(0))
	}

	// Masked IRQ must stay in native code and reach the BKPT.
	if err := backend.WriteRegister(
		cpu.RegisterCPSR,
		uint32(processorModeSystem)|cpu.StatusThumb|statusIRQDisable,
	); err != nil {
		t.Fatal(err)
	}
	backend.nativeRemain = 8
	status = uint32(callNativeBlock(block.entry, &backend.regs[0], &backend.nativeRemain))
	if status != nativeStatusBKPT || backend.regs[cpu.RegisterR0] != 1 {
		t.Fatalf("masked poll status=%#x r0=%d, want BKPT and r0=1", status, backend.regs[cpu.RegisterR0])
	}
}

func TestNativeWholeSystemMMIOBailsAndTakesRaisedIRQ(t *testing.T) {
	backend, bus := newNativeSystemBackend(t)
	putThumb(bus.ram, 0x1000, 0x6008, 0x2207, 0xbe00)              // str r0,[r1]; movs r2,#7; bkpt
	binary.LittleEndian.PutUint32(bus.ram[vectorIRQ:], 0xe1200070) // ARM BKPT at IRQ vector
	bus.onMMIO = func() {
		if err := backend.SetInterruptLine(cpu.InterruptIRQ, true); err != nil {
			t.Errorf("assert IRQ: %v", err)
		}
	}
	if err := backend.WriteRegister(cpu.RegisterR0, 0x12345678); err != nil {
		t.Fatal(err)
	}
	if err := backend.WriteRegister(cpu.RegisterR1, bus.mmio); err != nil {
		t.Fatal(err)
	}
	result := backend.Run(context.Background(), 0x1000, cpu.ModeThumb, 8)
	if result.Err != nil || result.Reason != cpu.StopBreakpoint || result.Instructions != 2 {
		t.Fatalf("MMIO/IRQ run = %+v", result)
	}
	if bus.dataWrites != 1 {
		t.Fatalf("MMIO writes = %d, want 1", bus.dataWrites)
	}
	if got := register(t, backend, cpu.RegisterR2); got != 0 {
		t.Fatalf("r2 = %d, instruction after IRQ-raising MMIO executed", got)
	}
}
