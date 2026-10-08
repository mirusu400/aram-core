package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	ktfrt "github.com/mirusu400/aram-core/application/internal/ktf"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	wipirt "github.com/mirusu400/aram-core/application/internal/wipi"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/cpu"
)

// Issue #506 reported a callback fault PC alongside startup registers and no
// stack window: restoring the interrupted context had erased the fault state.
func TestCallbackDebugStateSurvivesFault(t *testing.T) {
	for _, backend := range []string{PreciseBackend, "jit", FastestBackend} {
		for _, path := range []string{"wipi", "raptor-callback", "raptor-java"} {
			for _, fault := range []bool{false, true} {
				name := backend + "/" + path + "/return"
				if fault {
					name = backend + "/" + path + "/fault"
				}
				t.Run(name, func(t *testing.T) {
					factory := NewFactory()
					var err error
					factory.NewCPU, err = ResolveCPUBackend(backend)
					check(t, err)
					data := syntheticEADS()
					created, err := factory.Create(context.Background(), machinecore.Source{
						Name: "synthetic.dat", ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
					})
					check(t, err)
					m := created.(*Machine)
					t.Cleanup(func() { _ = m.Close() })
					const procedure = uint32(0x04000000)
					check(t, m.cpu.Map(procedure, 0x1000,
						cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
					code := []byte{
						0x2a, 0x24, // movs r4, #42
						0x10, 0xb5, // push {r4, lr}
						0x00, 0x21, // movs r1, #0
						0xc9, 0x43, // mvns r1, r1
						0x10, 0xbd, // pop {r4, pc}
					}
					if fault {
						code[8], code[9] = 0x0a, 0x78 // ldrb r2, [r1] at 0xffffffff
					}
					check(t, m.cpu.WriteMemory(procedure, code))
					outerSP, err := m.cpu.ReadRegister(cpu.RegisterSP)
					check(t, err)
					outerSP -= 0x1000
					check(t, m.cpu.WriteRegister(cpu.RegisterSP, outerSP))
					const outerPC, outerR4 = uint32(0x02000000), uint32(0xdeadbeef)
					check(t, m.cpu.WriteRegister(cpu.RegisterPC, outerPC))
					check(t, m.cpu.WriteRegister(cpu.RegisterR4, outerR4))
					check(t, m.cpu.WriteRegister(cpu.RegisterCPSR, 0x10))
					m.raptor = &raptorrt.Runtime{CPU: m.cpu, Public: m.wipi, Started: true}
					stack := outerSP
					switch path {
					case "wipi":
						m.state = machinecore.StateRunning
						check(t, m.wipi.BeginServiceExecution())
						var result cpu.Result
						result, _, err = m.invokeWIPICallback(context.Background(), wipirt.GuestCallback{
							Procedure: procedure | 1,
						})
						err = m.finishRaptorCall(result, err, result.Instructions)
					case "raptor-callback":
						stack -= 0x200
						m.frameRunBudget = 100
						m.raptor.CallbackTasks = []*raptorrt.CallbackTask{{
							Callback: wipirt.GuestCallback{Procedure: procedure | 1}, Stack: stack,
						}}
						err = m.StepFrame(context.Background())
					case "raptor-java":
						stack -= 0x100
						m.raptor.Java = &raptorrt.JavaRuntime{
							Host:  &ktfrt.Runtime{},
							Tasks: []*raptorrt.JavaTask{{Procedure: procedure | 1, Stack: stack}},
						}
						err = m.stepRaptorJavaAfterSafepoint(context.Background())
					}
					if (err != nil) != fault {
						t.Fatalf("callback fault=%t, error=%v", fault, err)
					}
					snapshot := m.DebugSnapshot(8)
					registers := make(map[string]uint32)
					for _, register := range snapshot.CPU.Registers {
						registers[register.Name] = register.Value
					}
					if !fault {
						if registers["pc"] != outerPC || registers["r4"] != outerR4 ||
							registers["sp"] != outerSP || snapshot.CPU.Mode != "arm" {
							t.Fatalf("successful callback did not restore outer context: %+v", snapshot.CPU)
						}
						if regions := m.DebugMemoryRegions(0); regions != nil {
							t.Fatal("successful callback exposes fault memory")
						}
						return
					}
					if m.State() != machinecore.StateFaulted || snapshot.LastResult.Reason != "fault" ||
						snapshot.LastResult.PC != procedure+10 || registers["pc"] != snapshot.LastResult.PC ||
						registers["r1"] != 0xffffffff || registers["r4"] != 42 ||
						registers["sp"] != stack-8 || registers["lr"] != guest.ReturnSentinel|1 ||
						snapshot.CPU.Mode != "thumb" {
						t.Fatalf("fault context was lost: %+v, %+v", snapshot.CPU, snapshot.LastResult)
					}
					var foundStack bool
					for _, region := range m.DebugMemoryRegions(0) {
						if region.Label != "stack" {
							continue
						}
						foundStack = true
						if region.Base != stack-8 || len(region.Data) < 8 ||
							binary.LittleEndian.Uint32(region.Data) != 42 ||
							binary.LittleEndian.Uint32(region.Data[4:]) != guest.ReturnSentinel|1 {
							t.Fatalf("wrong fault stack: %+v", region)
						}
					}
					if !foundStack {
						t.Fatal("fault stack window is absent")
					}
				})
			}
		}
	}
}
