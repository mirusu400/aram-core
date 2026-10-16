package interpreter

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

const (
	capture42BoundarySP        = uint32(0x80001000)
	capture42BoundaryParameter = uint32(0x80002000)
	capture42BoundaryTTBR      = uint32(0x00004000)
)

// All expected flags are literal. No capture/resolve helper supplies the oracle.
func capture42BoundaryLiteralFrame(t *testing.T, b *Backend, thumb uint32) (cpu.HostCallFrame, cpu.HostCallFrame) {
	t.Helper()
	check(t, b.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)|thumb|flagN|flagC|flagV))
	for register := uint32(0); register <= cpu.RegisterPC; register++ {
		check(t, b.WriteRegister(register, 0x24680000+register*4))
	}
	check(t, b.WriteRegister(cpu.RegisterSP, capture42BoundarySP))
	b.cp15.dataFaultStatus = 0x11
	b.cp15.instructionFaultStatus = 0x22
	b.cp15.faultAddress = 0x12345678
	b.flags = pendingFlags{dirty: true, mask: flagN | flagZ, bits: flagZ}
	var frame cpu.HostCallFrame
	for i := range frame.Stack {
		frame.Stack[i], frame.Parameters[i] = 0x98760000+uint32(i), 0xabcd0000+uint32(i)
	}
	frame.StackWords, frame.ParameterWords = 17, 19
	want := frame
	want.Registers = b.regs
	want.Registers[cpu.RegisterCPSR] = uint32(processorModeSystem) | thumb | flagZ | flagC | flagV
	want.StackWords, want.ParameterWords = 0, 0
	for i := range b.hostCallScratch {
		b.hostCallScratch[i] = 0xd7
	}
	wantMode := cpu.ModeARM
	if thumb != 0 {
		wantMode = cpu.ModeThumb
	}
	if b.mode != wantMode {
		t.Fatal("fixture instruction-set mode was not installed")
	}
	return frame, want
}

func capture42BoundaryAssertEmpty(t *testing.T, b *Backend, frame *cpu.HostCallFrame, want cpu.HostCallFrame) {
	t.Helper()
	if b.mmuTLBTable != nil {
		t.Fatal("fixture must start with a cold MMU translation table")
	}
	beforeCP15, beforeMode, beforeGen := b.cp15, b.mode, b.mappingGen
	beforeTable, scratch := b.mmuTLBTable, b.hostCallScratch
	before := b.ExecutionStatistics()
	check(t, b.CaptureHostCallFrame(frame, cpu.HostCallFrameRequest{ParameterAddress: capture42BoundaryParameter}))
	if *frame != want || b.regs != want.Registers || b.flags != (pendingFlags{}) {
		t.Fatal("empty system capture changed the literal frame or architectural flags")
	}
	if b.cp15 != beforeCP15 || b.mode != beforeMode || b.mappingGen != beforeGen ||
		b.mmuTLBTable != beforeTable || b.hostCallScratch != scratch {
		t.Fatal("empty system capture changed CP15, mode, translation state or scratch")
	}
	before.HostFrameCaptures++
	if b.ExecutionStatistics() != before {
		t.Fatal("empty system capture changed a counter other than its one capture")
	}
}

func TestEmptyHostCapture42AttachedBusAndMMUNoMemory(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func() *Backend
	}{{"New", New}, {"NewJIT", NewJIT}} {
		for _, mode := range []struct {
			name  string
			thumb uint32
		}{{"ARM", 0}, {"Thumb", cpu.StatusThumb}} {
			for _, enabled := range []bool{false, true} {
				name := "MMU-off"
				if enabled {
					name = "MMU-on"
				}
				t.Run(factory.name+"/"+mode.name+"/"+name, func(t *testing.T) {
					b := factory.new()
					defer b.Close()
					bus := &contextTestSystemBus{testSystemBus: testSystemBus{memory: make(map[uint32]byte)}}
					bus.writeU32(capture42BoundarySP, 0x44332211)
					bus.writeU32(capture42BoundaryParameter, 0xd8c7b6a5)
					check(t, b.AttachSystemBus(bus))
					b.cp15.translationTableBase = capture42BoundaryTTBR
					b.cp15.domainAccessControl = 1
					control := uint32(0)
					if enabled {
						control = 1
					}
					b.setCP15Control(control)
					if b.systemBus != bus || b.contextBus != bus || b.blockBus != nil || b.directBus != nil ||
						!b.physicalAccess || b.mmuEnabled() != enabled || b.cp15.control != control {
						t.Fatal("attached context bus or real MMU routing was not installed")
					}
					frame, want := capture42BoundaryLiteralFrame(t, b, mode.thumb)
					capture42BoundaryAssertEmpty(t, b, &frame, want)
					if bus.executeReads != 0 || bus.dataReads != 0 || bus.dataWrites != 0 || len(bus.dataContexts) != 0 {
						t.Fatal("empty capture consulted the attached bus")
					}

					// Same-backend control: MMU-off reads the literal physical word;
					// MMU-on encounters a deliberately zero first-level descriptor.
					before := b.ExecutionStatistics()
					beforeCP15, beforeMode, beforeGen := b.cp15, b.mode, b.mappingGen
					wantScratch := b.hostCallScratch
					err := b.CaptureHostCallFrame(&frame, cpu.HostCallFrameRequest{StackWords: 1,
						ParameterAddress: capture42BoundaryParameter})
					controlWant := want
					controlWant.StackWords = 1
					if enabled {
						var fault *MMUFault
						if !errors.Is(err, ErrMMUTranslationFault) || !errors.As(err, &fault) ||
							fault.Status != 5 || fault.Address != capture42BoundarySP || fault.Permission != cpu.PermissionRead {
							t.Fatal("nonempty control did not take the real MMU translation-fault path")
						}
						beforeCP15.dataFaultStatus = 5
						beforeCP15.faultAddress = capture42BoundarySP
						if b.mmuTLBTable == nil {
							t.Fatal("MMU control did not reach the translation-table lookup")
						}
					} else {
						check(t, err)
						controlWant.Stack[0] = 0x44332211
						copy(wantScratch[:4], []byte{0x11, 0x22, 0x33, 0x44})
						before.HostFrameCaptures++
						if b.mmuTLBTable != nil {
							t.Fatal("MMU-off control unexpectedly allocated an MMU table")
						}
					}
					if frame != controlWant || b.regs != want.Registers || b.flags != (pendingFlags{}) ||
						b.cp15 != beforeCP15 || b.mode != beforeMode || b.mappingGen != beforeGen ||
						b.hostCallScratch != wantScratch || b.ExecutionStatistics() != before {
						t.Fatal("nonempty bus control changed its literal state or capture-counter contract")
					}
					if bus.executeReads != 0 || bus.dataReads != 1 || bus.dataWrites != 0 || len(bus.dataContexts) != 1 {
						t.Fatal("nonempty control did not perform exactly one context-attributed physical read")
					}
				})
			}
		}
	}
}

func TestEmptyHostCapture42ExternalAbortBusNoRead(t *testing.T) {
	for _, factory := range []struct {
		name string
		new  func() *Backend
	}{{"New", New}, {"NewJIT", NewJIT}} {
		for _, mode := range []struct {
			name  string
			thumb uint32
		}{{"ARM", 0}, {"Thumb", cpu.StatusThumb}} {
			t.Run(factory.name+"/"+mode.name, func(t *testing.T) {
				b := factory.new()
				defer b.Close()
				bus := &externalAbortSystemBus{
					testSystemBus: testSystemBus{memory: make(map[uint32]byte)},
					abortAddress:  capture42BoundarySP,
				}
				check(t, b.AttachSystemBus(bus))
				b.setCP15Control(0)
				if b.systemBus != bus || !b.physicalAccess || b.mmuEnabled() ||
					b.contextBus != nil || b.blockBus != nil || b.directBus != nil {
					t.Fatal("external-abort physical bus control was not installed")
				}
				frame, want := capture42BoundaryLiteralFrame(t, b, mode.thumb)
				capture42BoundaryAssertEmpty(t, b, &frame, want)
				if bus.executeReads != 0 || bus.dataReads != 0 || bus.dataWrites != 0 {
					t.Fatal("empty capture consulted the aborting bus")
				}

				before := b.ExecutionStatistics()
				beforeCP15, beforeMode, beforeGen := b.cp15, b.mode, b.mappingGen
				beforeTable, scratch := b.mmuTLBTable, b.hostCallScratch
				err := b.CaptureHostCallFrame(&frame, cpu.HostCallFrameRequest{StackWords: 1})
				var external cpu.ExternalAbortError
				var cause externalAbortTestError
				if err == nil || !errors.As(err, &external) || !external.ExternalAbort() ||
					!errors.As(err, &cause) || !errors.Is(err, externalAbortTestError{}) {
					t.Fatal("nonempty bus control did not retain its typed external-abort cause")
				}
				// The existing aborting Read returns before incrementing dataReads.
				// Error type/cause, NOT a read-counter increase, proves this control.
				controlWant := want
				controlWant.StackWords = 1
				if frame != controlWant || b.regs != want.Registers || b.flags != (pendingFlags{}) ||
					b.cp15 != beforeCP15 || b.mode != beforeMode || b.mappingGen != beforeGen ||
					b.mmuTLBTable != beforeTable || b.hostCallScratch != scratch || b.ExecutionStatistics() != before {
					t.Fatal("external-abort control changed its exact partial frame or CPU state")
				}
			})
		}
	}
}
