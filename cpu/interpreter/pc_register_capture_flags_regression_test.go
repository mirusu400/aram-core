package interpreter

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// This fixture deliberately does not depend on the optional object data-cache
// implementation or its tests: the observer fix survives rejection of that cache.
func objectLookupCaptureRegressionMachine(t *testing.T, create func() *Backend, initial uint32) *Backend {
	t.Helper()
	b := create()
	t.Cleanup(func() { _ = b.Close() })
	for _, address := range []uint32{0x1000, 0x2000, 0x3000, 0x6000} {
		permission := cpu.PermissionRead | cpu.PermissionWrite
		if address == 0x1000 {
			permission |= cpu.PermissionExecute
		}
		check(t, b.Map(address, 0x100, permission))
	}
	words := []uint16{0xb510, 0x1c04, 0x6800, 0x2301, 0x4003, 0x2b00, 0xd101, 0x68c0, 0xbd10}
	code := make([]byte, 0x100)
	for index, word := range words {
		binary.LittleEndian.PutUint16(code[2*index:], word)
	}
	binary.LittleEndian.PutUint16(code[0x40:], 0x46c0) // MOV r8,r8: does not change flags.
	check(t, b.WriteMemory(0x1000, code))
	check(t, b.WriteMemory(0x2000, []byte{0x00, 0x30, 0x00, 0x00}))
	check(t, b.WriteMemory(0x300c, []byte{0x78, 0x56, 0x34, 0x12}))
	for register, value := range map[uint32]uint32{
		0: 0x2000, 3: 99, 4: 0xaabbccdd,
		cpu.RegisterSP: 0x6080, cpu.RegisterLR: 0x1041,
		cpu.RegisterCPSR: initial | uint32(processorModeSystem),
	} {
		check(t, b.WriteRegister(register, value))
	}
	check(t, b.SetPCHistoryLimit(0))
	check(t, b.SetPCRegisterCapture(0, 0))
	b.pcHits = nil
	return b
}

func TestPCRegisterCaptureObjectLookupFlagsRegression(t *testing.T) {
	for flags := uint32(0); flags < 16; flags++ {
		for _, bridge := range []string{"none", "ReadRegisterCPSR", "CaptureHostCallFrame", "SaveExecutionContext", "SaveContext"} {
			t.Run(fmt.Sprintf("initial=%x/bridge=%s", flags, bridge), func(t *testing.T) {
				precise := objectLookupCaptureRegressionMachine(t, New, flags<<28)
				jit := objectLookupCaptureRegressionMachine(t, NewJIT, flags<<28)
				if block := jit.jitBlockAt(0x1000); block == nil || block.extraLoop == nil || block.extraLoop.kind != jitObjectLookupLoop {
					t.Fatal("positive accelerator admission failed")
				}
				want := precise.Run(context.Background(), 0x1000, cpu.ModeThumb, 9)
				got := jit.Run(context.Background(), 0x1000, cpu.ModeThumb, 9)
				if want.Reason != cpu.StopBudget || got.Reason != cpu.StopBudget || want.Instructions != 9 ||
					got.Instructions != 9 || want.PC != 0x1040 || got.PC != want.PC || want.Err != nil || got.Err != nil {
					t.Fatalf("exact9 want=%+v got=%+v", want, got)
				}
				for register := 0; register < int(cpu.RegisterCPSR); register++ {
					if precise.regs[register] != jit.regs[register] {
						t.Fatal("GPR differs after exact9")
					}
				}
				for index := range precise.regions {
					if !bytes.Equal(precise.regions[index].data, jit.regions[index].data) {
						t.Fatal("memory differs after exact9")
					}
				}
				const expectedCPSR = flagZ | flagC | flagT | uint32(processorModeSystem)
				for _, b := range []*Backend{precise, jit} {
					// Lazy storage differences are valid; architectural flags must match.
					effective := b.regs[cpu.RegisterCPSR]
					if b.flags.dirty {
						effective = effective&^b.flags.mask | b.flags.bits
					}
					if effective != expectedCPSR {
						t.Fatal("effective architectural CPSR is wrong")
					}
					switch bridge {
					case "ReadRegisterCPSR":
						value, err := b.ReadRegister(cpu.RegisterCPSR)
						check(t, err)
						if value != expectedCPSR {
							t.Fatal("public CPSR read is wrong")
						}
					case "CaptureHostCallFrame":
						var frame cpu.HostCallFrame
						check(t, b.CaptureHostCallFrame(&frame, cpu.HostCallFrameRequest{}))
						if frame.Registers[cpu.RegisterCPSR] != expectedCPSR {
							t.Fatal("public host frame capture is wrong")
						}
					case "SaveExecutionContext":
						saved, err := b.SaveExecutionContext(nil)
						check(t, err)
						if saved.(*executionContext).regs[cpu.RegisterCPSR] != expectedCPSR {
							t.Fatal("fast context captured incorrect architectural flags")
						}
					case "SaveContext":
						saved, err := b.SaveContext()
						check(t, err)
						if binary.LittleEndian.Uint32(saved[8+4*int(cpu.RegisterCPSR):]) != expectedCPSR {
							t.Fatal("serialized context captured incorrect architectural flags")
						}
					}
					check(t, b.SetPCRegisterCapture(0x1040, 1))
				}
				// bridge=none has made no materializing state API call since exact9.
				want = precise.Run(context.Background(), 0x1040, cpu.ModeThumb, 1)
				got = jit.Run(context.Background(), 0x1040, cpu.ModeThumb, 1)
				if want.Err != nil || got.Err != nil || want.Instructions != 1 || got.Instructions != 1 || want.PC != got.PC {
					t.Fatal("next public Run differs")
				}
				wc, gc := precise.PCRegisterCaptures(), jit.PCRegisterCaptures()
				if len(wc) != 1 || len(gc) != 1 || !reflect.DeepEqual(wc, gc) || wc[0].Registers[cpu.RegisterCPSR] != expectedCPSR {
					t.Fatal("next public PC capture did not expose the precise architectural CPSR")
				}
			})
		}
	}
}
