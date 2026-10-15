package guest

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
	"github.com/mirusu400/aram-core/cpu/interpreter"
)

// Both variants use the same compiled executor. This measures the host-frame
// bridge kernel only, not DispatchAPI, Backend.Run, StepFrame or product A/B.
func BenchmarkConcreteHostCallBridge(b *testing.B) {
	for _, specimen := range []string{
		"capture-direct-zero", "capture-direct-stack", "capture-direct-parameters",
		"capture-direct-both", "return-direct-thumb", "return-direct-arm",
		"capture-scalar", "return-scalar", "capture-override", "return-override",
	} {
		b.Run(specimen, func(b *testing.B) {
			for _, bridge := range []bool{false, true} {
				variant := "old"
				if bridge {
					variant = "bridge"
				}
				b.Run(variant, func(b *testing.B) {
					concrete := interpreter.New()
					b.Cleanup(func() { _ = concrete.Close() })
					must := func(err error) {
						b.Helper()
						if err != nil {
							b.Fatal(err)
						}
					}
					must(concrete.Map(0x1000, 0x1000, cpu.PermissionRead|cpu.PermissionWrite))
					for register := uint32(0); register < cpu.RegisterPC; register++ {
						must(concrete.WriteRegister(register, 0x10000000+register))
					}
					must(concrete.WriteRegister(cpu.RegisterSP, 0x1080))
					lr := uint32(0x1001)
					if specimen == "return-direct-arm" {
						lr = 0x1000
					}
					must(concrete.WriteRegister(cpu.RegisterLR, lr))
					must(concrete.WriteRegister(cpu.RegisterCPSR, 0x9000001f))
					var words [32]byte
					for index := range 8 {
						binary.LittleEndian.PutUint32(words[index*4:], 0x100+uint32(index))
					}
					must(concrete.WriteMemory(0x1080, words[:]))
					for index := range 8 {
						binary.LittleEndian.PutUint32(words[index*4:], 0x200+uint32(index))
					}
					must(concrete.WriteMemory(0x1100, words[:]))
					var backend cpu.Backend = concrete
					var override *concreteHostCallBenchmarkOverride
					if strings.HasSuffix(specimen, "scalar") {
						backend = concreteHostCallBenchmarkScalar{Backend: concrete}
					} else if strings.HasSuffix(specimen, "override") {
						override = &concreteHostCallBenchmarkOverride{Backend: concrete}
						backend = override
					}
					request := cpu.HostCallFrameRequest{}
					if specimen == "capture-direct-stack" || specimen == "capture-direct-both" {
						request.StackWords = 8
					}
					if specimen == "capture-direct-parameters" || specimen == "capture-direct-both" {
						request.ParameterWords, request.ParameterAddress = 8, 0x1100
					}
					var frame cpu.HostCallFrame
					capture, hostReturn := cpu.CaptureHostCallFrame, cpu.ReturnHostCallFrame
					if bridge {
						capture, hostReturn = CaptureHostCallFrame, ReturnHostCallFrame
					}
					returning := strings.HasPrefix(specimen, "return-")
					var invoke func() error
					if returning {
						invoke = func() error { return hostReturn(backend, &frame, 0x12345678, 0x87654321) }
					} else {
						invoke = func() error { return capture(backend, &frame, request) }
					}
					must(invoke())
					if frame.Registers[cpu.RegisterSP] != 0x1080 || frame.Registers[cpu.RegisterLR] != lr ||
						frame.Registers[cpu.RegisterCPSR] != 0x9000001f ||
						frame.StackWords != request.StackWords || frame.ParameterWords != request.ParameterWords {
						b.Fatalf("warm frame: %+v", frame)
					}
					for index := uint32(0); index < request.StackWords; index++ {
						if frame.Stack[index] != 0x100+index {
							b.Fatal("wrong warm stack word")
						}
					}
					for index := uint32(0); index < request.ParameterWords; index++ {
						if frame.Parameters[index] != 0x200+index {
							b.Fatal("wrong warm parameter word")
						}
					}
					if returning {
						value, err := concrete.ReadRegister(cpu.RegisterPC)
						must(err)
						if value != lr&^1 {
							b.Fatal("wrong warm return PC")
						}
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if err := invoke(); err != nil {
							b.Fatal(err)
						}
					}
					b.StopTimer()
					if override != nil {
						calls := override.captures
						if returning {
							calls = override.returns
						}
						if calls != uint64(b.N)+1 {
							b.Fatal("bridge bypassed an embedding override")
						}
					}
				})
			}
		})
	}
}

// Embedding only the required contract deliberately hides optional capabilities.
type concreteHostCallBenchmarkScalar struct{ cpu.Backend }

type concreteHostCallBenchmarkOverride struct {
	*interpreter.Backend
	captures, returns uint64
}

func (b *concreteHostCallBenchmarkOverride) CaptureHostCallFrame(destination *cpu.HostCallFrame, request cpu.HostCallFrameRequest) error {
	b.captures++
	return b.Backend.CaptureHostCallFrame(destination, request)
}

func (b *concreteHostCallBenchmarkOverride) ReturnHostCallFrame(destination *cpu.HostCallFrame, low, high uint32) error {
	b.returns++
	return b.Backend.ReturnHostCallFrame(destination, low, high)
}
