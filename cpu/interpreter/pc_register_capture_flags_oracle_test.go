package interpreter

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

type pcCaptureFlagsOracleOrigin struct {
	name       string
	mode       cpu.Mode
	code       []byte
	count      uint64
	r0, r1, r3 uint32
	mask, bits uint32
}

func pcCaptureFlagsOracleThumb(words ...uint16) []byte {
	code := make([]byte, 2*len(words))
	for i, word := range words {
		binary.LittleEndian.PutUint16(code[2*i:], word)
	}
	return code
}

func pcCaptureFlagsOracleARM(words ...uint32) []byte {
	code := make([]byte, 4*len(words))
	for i, word := range words {
		binary.LittleEndian.PutUint32(code[4*i:], word)
	}
	return code
}

// These flag expectations are architectural literals, independent of the lazy
// flag setters, JIT classifier and instruction fixtures used by production.
func pcCaptureFlagsOracleOrigins() []pcCaptureFlagsOracleOrigin {
	return []pcCaptureFlagsOracleOrigin{
		{"thumb-add-overflow", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x3001), 1, 0x7fffffff, 0, 0, 0xf0000000, 0x90000000},
		{"thumb-add-carry", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x3001), 1, 0xffffffff, 0, 0, 0xf0000000, 0x60000000},
		{"thumb-sub-borrow", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x3801), 1, 0, 0, 0, 0xf0000000, 0x80000000},
		{"thumb-sub-overflow", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x3801), 1, 0x80000000, 0, 0, 0xf0000000, 0x30000000},
		{"thumb-compare-equal", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x2801), 1, 1, 0, 0, 0xf0000000, 0x60000000},
		{"thumb-lsl-carry", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x0048), 1, 0, 0x80000000, 0, 0xe0000000, 0x60000000},
		{"thumb-lsl-negative", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x0048), 1, 0, 0x40000000, 0, 0xe0000000, 0x80000000},
		{"thumb-lsr-carry", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x0848), 1, 0, 1, 0, 0xe0000000, 0x60000000},
		{"thumb-asr-negative-carry", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x1048), 1, 0, 0x80000001, 0, 0xe0000000, 0xa0000000},
		{"thumb-mov-zero", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x2000), 1, 0xffffffff, 0, 0, 0xc0000000, 0x40000000},
		{"thumb-and-zero", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x4008), 1, 0xf0, 0x0f, 0, 0xc0000000, 0x40000000},
		{"thumb-eor-negative", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x4048), 1, 0, 0x80000000, 0, 0xc0000000, 0x80000000},
		{"thumb-pending-cv-then-nz", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x1840, 0x2201), 2, 0x80000000, 0x80000000, 0, 0xf0000000, 0x30000000},
		{"thumb-pending-cv-then-nzc", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x1840, 0x005a), 2, 0x80000000, 0x80000000, 1, 0xf0000000, 0x10000000},
		{"thumb-pending-cv-then-nzc-nz", cpu.ModeThumb, pcCaptureFlagsOracleThumb(0x1840, 0x005a, 0x2400), 3, 0x80000000, 0x80000000, 1, 0xf0000000, 0x50000000},
		{"arm-add-overflow", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe2900001), 1, 0x7fffffff, 0, 0, 0xf0000000, 0x90000000},
		{"arm-add-carry", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe2900001), 1, 0xffffffff, 0, 0, 0xf0000000, 0x60000000},
		{"arm-sub-borrow", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe2500001), 1, 0, 0, 0, 0xf0000000, 0x80000000},
		{"arm-sub-overflow", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe2500001), 1, 0x80000000, 0, 0, 0xf0000000, 0x30000000},
		{"arm-compare-equal", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe3500001), 1, 1, 0, 0, 0xf0000000, 0x60000000},
		{"arm-movs-zero-preserve-cv", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe3b00000), 1, 0xffffffff, 0, 0, 0xc0000000, 0x40000000},
		{"arm-shift-carry-preserve-v", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe1b00081), 1, 0, 0x80000000, 0, 0xe0000000, 0x60000000},
		{"arm-ands-zero-preserve-cv", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe0100001), 1, 0xf0, 0x0f, 0, 0xc0000000, 0x40000000},
		{"arm-muls-zero-preserve-cv", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe0100190), 1, 0, 0xffffffff, 0, 0xc0000000, 0x40000000},
		{"arm-pending-cv-then-nz", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe0900001, 0xe3b02001), 2, 0x80000000, 0x80000000, 0, 0xf0000000, 0x30000000},
		{"arm-pending-cv-then-nzc", cpu.ModeARM, pcCaptureFlagsOracleARM(0xe0900001, 0xe1b02083), 2, 0x80000000, 0x80000000, 1, 0xf0000000, 0x10000000},
	}
}

func pcCaptureFlagsOracleInit(t *testing.T, b *Backend, cpsr uint32) {
	t.Helper()
	b.pcHits = nil
	check(t, b.SetPCHistoryLimit(0))
	check(t, b.SetPCRegisterCapture(0, 0))
	for register := uint32(0); register < cpu.RegisterPC; register++ {
		check(t, b.WriteRegister(register, 0x11000000+register))
	}
	check(t, b.WriteRegister(cpu.RegisterCPSR, cpsr))
}

func pcCaptureFlagsOracleBudget(t *testing.T, b *Backend, address uint32, mode cpu.Mode, budget uint64, next uint32) {
	t.Helper()
	result := b.Run(context.Background(), address, mode, budget)
	if result.Err != nil || result.Reason != cpu.StopBudget || result.Instructions != budget || result.PC != next {
		t.Fatalf("Run = %+v, want budget %d and PC %#x", result, budget, next)
	}
}

func TestPCRegisterCaptureFlagsOracleOrigins(t *testing.T) {
	for _, origin := range pcCaptureFlagsOracleOrigins() {
		for nibble := uint32(0); nibble < 16; nibble++ {
			for _, nextRun := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/nzcv=%x/next-run=%t", origin.name, nibble, nextRun), func(t *testing.T) {
					initial := nibble<<28 | 0x080000d3
					width := uint32(4)
					trailer := pcCaptureFlagsOracleARM(0xe1a08008, 0xe1200070)
					if origin.mode == cpu.ModeThumb {
						initial |= 0x20
						width = 2
						trailer = pcCaptureFlagsOracleThumb(0x46c0, 0xbe00)
					}
					address := uint32(0x1000 + len(origin.code))
					wantCPSR := initial&^origin.mask | origin.bits
					var reference PCRegisterCapture
					for index, b := range []*Backend{New(), NewJIT()} {
						t.Cleanup(func() { _ = b.Close() })
						code := append(append([]byte(nil), origin.code...), trailer...)
						check(t, b.Map(0x1000, uint32(len(code)), cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
						check(t, b.WriteMemory(0x1000, code))
						pcCaptureFlagsOracleInit(t, b, initial)
						check(t, b.WriteRegister(0, origin.r0))
						check(t, b.WriteRegister(1, origin.r1))
						check(t, b.WriteRegister(3, origin.r3))
						if nextRun {
							pcCaptureFlagsOracleBudget(t, b, 0x1000, origin.mode, origin.count, address)
							// No SaveContext, CPSR read, host frame or flag reader
							// is allowed between the origin and this actual capture.
							check(t, b.SetPCRegisterCapture(address, 1))
							pcCaptureFlagsOracleBudget(t, b, address, origin.mode, 1, address+width)
						} else {
							check(t, b.SetPCRegisterCapture(address, 1))
							pcCaptureFlagsOracleBudget(t, b, 0x1000, origin.mode, origin.count+1, address+width)
						}
						captures := b.PCRegisterCaptures()
						if len(captures) != 1 || captures[0].Address != address || captures[0].Registers[cpu.RegisterPC] != address || captures[0].Registers[cpu.RegisterCPSR] != wantCPSR {
							t.Fatalf("backend %d captures = %+v, want PC %#x and literal CPSR %#x", index, captures, address, wantCPSR)
						}
						if index == 0 {
							reference = captures[0]
						} else if captures[0] != reference {
							t.Fatalf("translated public capture = %+v, precise = %+v", captures[0], reference)
						}
						if effective, err := b.ReadRegister(cpu.RegisterCPSR); err != nil || effective != wantCPSR {
							t.Fatalf("effective CPSR = (%#x, %v), want %#x", effective, err, wantCPSR)
						}
					}
				})
			}
		}
	}
}

func pcCaptureFlagsOraclePending(t *testing.T, b *Backend) {
	t.Helper()
	check(t, b.WriteRegister(0, 0x7fffffff))
	pcCaptureFlagsOracleBudget(t, b, 0x1000, cpu.ModeThumb, 1, 0x1002)
	if !b.flags.dirty {
		t.Fatal("literal ADDS fixture did not leave a deferred flag update")
	}
}

func pcCaptureFlagsOracleLazyBackend(t *testing.T, constructor func() *Backend) *Backend {
	t.Helper()
	b := constructor()
	t.Cleanup(func() { _ = b.Close() })
	check(t, b.Map(0x1000, 0x24, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	check(t, b.WriteMemory(0x1000, pcCaptureFlagsOracleThumb(0x3001)))
	check(t, b.WriteMemory(0x1020, pcCaptureFlagsOracleThumb(0x46c0, 0x46c0)))
	pcCaptureFlagsOracleInit(t, b, 0x280000f3)
	return b
}

func TestPCRegisterCaptureFlagsOracleFalseAdmission(t *testing.T) {
	for _, constructor := range []backendConstructor{{"precise", New}, {"go-jit", NewJIT}} {
		for _, route := range []string{"mismatch", "zero-limit", "exhausted", "history-only", "hits-only", "history-and-hits"} {
			t.Run(constructor.name+"/"+route, func(t *testing.T) {
				b := pcCaptureFlagsOracleLazyBackend(t, constructor.new)
				if route == "exhausted" {
					check(t, b.SetPCRegisterCapture(0x1020, 1))
					pcCaptureFlagsOracleBudget(t, b, 0x1020, cpu.ModeThumb, 1, 0x1022)
				}
				switch route {
				case "mismatch":
					check(t, b.SetPCRegisterCapture(0x1022, 1))
				case "zero-limit":
					check(t, b.SetPCRegisterCapture(0x1020, 0))
					check(t, b.SetPCHistoryLimit(2))
				case "history-only", "history-and-hits":
					check(t, b.SetPCHistoryLimit(2))
				}
				if route == "hits-only" || route == "history-and-hits" {
					b.pcHits = make(map[uint32]uint64)
				}
				pcCaptureFlagsOraclePending(t, b)
				beforeCPSR, beforeFlags := b.regs[cpu.RegisterCPSR], b.flags
				beforeCaptures := b.PCRegisterCaptures()
				pcCaptureFlagsOracleBudget(t, b, 0x1020, cpu.ModeThumb, 1, 0x1022)
				if b.regs[cpu.RegisterCPSR] != beforeCPSR || b.flags != beforeFlags {
					t.Fatalf("non-capturing route materialized flags: raw %#x/%+v -> %#x/%+v", beforeCPSR, beforeFlags, b.regs[cpu.RegisterCPSR], b.flags)
				}
				if got := b.PCRegisterCaptures(); !reflect.DeepEqual(got, beforeCaptures) {
					t.Fatalf("non-capturing route changed stored captures: %+v -> %+v", beforeCaptures, got)
				}
				if effective, err := b.ReadRegister(cpu.RegisterCPSR); err != nil || effective != 0x980000f3 {
					t.Fatalf("effective CPSR = (%#x, %v), want 0x980000f3", effective, err)
				}
			})
		}
	}
}

func TestPCRegisterCaptureFlagsOracleGettersConfigurationAndClone(t *testing.T) {
	for _, constructor := range []backendConstructor{{"precise", New}, {"go-jit", NewJIT}} {
		t.Run(constructor.name, func(t *testing.T) {
			b := pcCaptureFlagsOracleLazyBackend(t, constructor.new)
			pcCaptureFlagsOraclePending(t, b)
			check(t, b.SetPCRegisterCapture(0x1020, 1))
			pcCaptureFlagsOracleBudget(t, b, 0x1020, cpu.ModeThumb, 1, 0x1022)
			stored := b.PCRegisterCaptures()
			if len(stored) != 1 || stored[0].Registers[cpu.RegisterCPSR] != 0x980000f3 {
				t.Fatalf("first actual capture = %+v", stored)
			}
			pcCaptureFlagsOraclePending(t, b)
			beforeRegs, beforeFlags := b.regs, b.flags
			clone := b.PCRegisterCaptures()
			clone[0].Address ^= 0xffffffff
			clone[0].Registers = [17]uint32{}
			_ = b.PCHistory()
			_ = b.PCHits()
			_ = b.ExecutionStatistics()
			if got := b.PCRegisterCaptures(); !reflect.DeepEqual(got, stored) {
				t.Fatalf("caller mutation escaped cloned captures: %+v, want %+v", got, stored)
			}
			if b.regs != beforeRegs || b.flags != beforeFlags {
				t.Fatal("diagnostic getters changed live architectural/lazy state")
			}
			if err := b.SetPCRegisterCapture(0x1040, 4097); err == nil {
				t.Fatal("oversized capture configuration accepted")
			}
			if b.pcCaptureAddress != 0x1020 || b.pcCaptureLimit != 1 || !reflect.DeepEqual(b.PCRegisterCaptures(), stored) {
				t.Fatal("invalid configuration changed the existing capture")
			}
			check(t, b.SetPCRegisterCapture(0x1022, 2))
			if len(b.PCRegisterCaptures()) != 0 {
				t.Fatal("valid reconfiguration did not clear old captures")
			}
			check(t, b.SetPCRegisterCapture(0x1020, 0))
			check(t, b.SetPCHistoryLimit(3))
			if b.regs != beforeRegs || b.flags != beforeFlags {
				t.Fatal("capture/history configuration materialized pending flags")
			}
		})
	}
}

func TestPCRegisterCaptureFlagsOracleLiteralObjectReturn(t *testing.T) {
	for nibble := uint32(0); nibble < 16; nibble++ {
		t.Run(fmt.Sprintf("nzcv=%x", nibble), func(t *testing.T) {
			var reference PCRegisterCapture
			for index, b := range []*Backend{New(), NewJIT()} {
				t.Cleanup(func() { _ = b.Close() })
				for _, address := range []uint32{0x1000, 0x2000, 0x3000, 0x6000} {
					permission := cpu.PermissionRead | cpu.PermissionWrite
					if address == 0x1000 {
						permission |= cpu.PermissionExecute
					}
					check(t, b.Map(address, 0x100, permission))
				}
				// Literal nine-instruction helper; do not use the production
				// recognizer's word array or another object's fixture builder.
				check(t, b.WriteMemory(0x1000, pcCaptureFlagsOracleThumb(0xb510, 0x1c04, 0x6800, 0x2301, 0x4003, 0x2b00, 0xd101, 0x68c0, 0xbd10)))
				check(t, b.WriteMemory(0x1040, pcCaptureFlagsOracleThumb(0x46c0, 0xbe00)))
				check(t, b.WriteMemory(0x2000, []byte{0, 0x30, 0, 0}))
				check(t, b.WriteMemory(0x300c, []byte{0x78, 0x56, 0x34, 0x12}))
				pcCaptureFlagsOracleInit(t, b, nibble<<28|0x080000f3)
				check(t, b.WriteRegister(0, 0x2000))
				check(t, b.WriteRegister(3, 99))
				check(t, b.WriteRegister(4, 0xaabbccdd))
				check(t, b.WriteRegister(cpu.RegisterSP, 0x6080))
				check(t, b.WriteRegister(cpu.RegisterLR, 0x1041))
				pcCaptureFlagsOracleBudget(t, b, 0x1000, cpu.ModeThumb, 9, 0x1040)
				if index == 1 {
					block := b.jitBlocks[0x1000]
					if block == nil || block.extraLoop == nil || block.extraLoop.kind != jitObjectLookupLoop || block.end != 0x1012 {
						t.Fatal("literal nine-instruction fixture did not classify the actual whole application entry")
					}
					// With tracing/bus/TLB disabled and all literal spans aligned,
					// disjoint and mapped, that entry admits the complete nine-op
					// accelerator. Do not infer a hit from a counter this older
					// helper does not maintain, or require eager/lazy raw equality.
					if b.tracing() || b.systemBus != nil || b.physicalAccess || b.tlb != nil {
						t.Fatal("literal whole-entry fixture unexpectedly enabled an accelerator guard")
					}
				}
				check(t, b.SetPCRegisterCapture(0x1040, 1))
				pcCaptureFlagsOracleBudget(t, b, 0x1040, cpu.ModeThumb, 1, 0x1042)
				captures := b.PCRegisterCaptures()
				if len(captures) != 1 || captures[0].Address != 0x1040 || captures[0].Registers[cpu.RegisterCPSR] != 0x680000f3 || captures[0].Registers[0] != 0x12345678 || captures[0].Registers[3] != 0 || captures[0].Registers[4] != 0xaabbccdd || captures[0].Registers[cpu.RegisterSP] != 0x6080 {
					t.Fatalf("object-return public capture = %+v", captures)
				}
				if index == 0 {
					reference = captures[0]
				} else if captures[0] != reference {
					t.Fatalf("object-return translated capture = %+v, precise = %+v", captures[0], reference)
				}
			}
		})
	}
}

type pcCaptureFlagsOracleBus struct{ data [0x1100]byte }

func (bus *pcCaptureFlagsOracleBus) Read(address uint32, destination []byte, _ cpu.Permissions) error {
	if uint64(address)+uint64(len(destination)) > uint64(len(bus.data)) {
		return cpu.ErrInvalidAddress
	}
	copy(destination, bus.data[int(address):int(address)+len(destination)])
	return nil
}

func (bus *pcCaptureFlagsOracleBus) Write(address uint32, source []byte, _ cpu.Permissions) error {
	if uint64(address)+uint64(len(source)) > uint64(len(bus.data)) {
		return cpu.ErrInvalidAddress
	}
	copy(bus.data[int(address):int(address)+len(source)], source)
	return nil
}

func pcCaptureFlagsOracleSystemTier(t *testing.T, constructor backendConstructor) {
	t.Helper()
	for _, mode := range []cpu.Mode{cpu.ModeARM, cpu.ModeThumb} {
		for nibble := uint32(0); nibble < 16; nibble++ {
			t.Run(fmt.Sprintf("%s/mode=%d/nzcv=%x", constructor.name, mode, nibble), func(t *testing.T) {
				b := constructor.new()
				t.Cleanup(func() { _ = b.Close() })
				if (constructor.name == "native-jit" || constructor.name == "hybrid-jit") && b.nativeBlocks == nil {
					t.Skip("native executable arena unavailable")
				}
				bus := &pcCaptureFlagsOracleBus{}
				initial := nibble<<28 | 0x080000d3
				width, returnAddress := uint32(4), uint32(0x1040)
				code := pcCaptureFlagsOracleARM(0xe2900001, 0xe12fff1c) // ADDS; BX r12.
				trailer := pcCaptureFlagsOracleARM(0xe1a08008)
				if mode == cpu.ModeThumb {
					initial |= 0x20
					width, returnAddress = 2, 0x1041
					code = pcCaptureFlagsOracleThumb(0x3001, 0x4760)
					trailer = pcCaptureFlagsOracleThumb(0x46c0)
				}
				copy(bus.data[0x1000:], code)
				copy(bus.data[0x1040:], trailer)
				check(t, b.AttachSystemBus(bus))
				pcCaptureFlagsOracleInit(t, b, initial)
				check(t, b.WriteRegister(0, 0x7fffffff))
				check(t, b.WriteRegister(12, returnAddress))
				pcCaptureFlagsOracleBudget(t, b, 0x1000, mode, 2, 0x1040)
				if b.nativeBlocks != nil {
					switch {
					case mode == cpu.ModeARM:
						if b.nativeARMBlocks[0x1000] == nil {
							t.Fatal("native ARM origin did not enter a translated block")
						}
					case b.jitBlocks != nil:
						if b.jitBlocks[0x1000] == nil {
							t.Fatal("hybrid system Thumb origin did not enter the Go translated tier")
						}
					default:
						if b.nativeBlocks[0x1000] == nil {
							t.Fatal("native Thumb origin did not enter a translated block")
						}
					}
				}
				check(t, b.SetPCRegisterCapture(0x1040, 1))
				pcCaptureFlagsOracleBudget(t, b, 0x1040, mode, 1, 0x1040+width)
				captures := b.PCRegisterCaptures()
				wantCPSR := initial&0x0fffffff | 0x90000000
				var wantRegisters [17]uint32
				for register := uint32(0); register < cpu.RegisterPC; register++ {
					wantRegisters[register] = 0x11000000 + register
				}
				wantRegisters[0], wantRegisters[12] = 0x80000000, returnAddress
				wantRegisters[cpu.RegisterPC], wantRegisters[cpu.RegisterCPSR] = 0x1040, wantCPSR
				if len(captures) != 1 || captures[0].Address != 0x1040 || captures[0].Registers != wantRegisters {
					t.Fatalf("system tier public capture = %+v, want literal CPSR %#x", captures, wantCPSR)
				}
			})
		}
	}
}

func TestPCRegisterCaptureFlagsOracleSupportedSystemTiers(t *testing.T) {
	for _, constructor := range armExecutionTierConstructors() {
		pcCaptureFlagsOracleSystemTier(t, constructor)
	}
}
