//go:build (windows && amd64) || ((android || linux) && arm64) || (darwin && arm64 && cgo)

package interpreter

import (
	"context"
	"encoding/binary"
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
	backend.nativeBlocks[0x1010] = nil
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
	backend.nativeBlocks[0x4000] = nil
	backend.nativeBlocks[0xf000] = nil
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
						t.Fatalf("native 4 KiB validation polluted architectural MMU TLB at %#x", address)
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
