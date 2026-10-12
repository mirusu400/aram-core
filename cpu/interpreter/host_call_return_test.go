package interpreter

import (
	"bytes"
	"errors"
	"github.com/mirusu400/aram-core/cpu"
	"testing"
)

type hostReturnFallback struct{ cpu.Backend }

func TestHostCallReturnMatchesSeparateCaptureAndCommit(t *testing.T) {
	for _, create := range []func() *Backend{New, NewJIT} {
		for _, fallback := range []bool{false, true} {
			expected, actual := create(), create()
			for _, b := range []*Backend{expected, actual} {
				t.Cleanup(func() { _ = b.Close() })
			}
			for _, mode := range []processorMode{processorModeUser, processorModeSystem, processorModeFIQ, processorModeSupervisor} {
				for _, thumb := range []uint32{0, cpu.StatusThumb} {
					for _, lr := range []uint32{0, 0x1000, 0x1001, 0x1002, 0x1003, 0xffffffff} {
						for _, b := range []*Backend{expected, actual} {
							check(t, b.WriteRegister(cpu.RegisterCPSR, uint32(mode)|thumb|flagN|flagC|flagV))
							for r := uint32(0); r < cpu.RegisterPC; r++ {
								check(t, b.WriteRegister(r, 0x2468ace0+r*4))
							}
							check(t, b.WriteRegister(cpu.RegisterLR, lr))
							check(t, b.WriteRegister(cpu.RegisterPC, 0x8000))
							b.setNZCV(0, true, false)
							b.instructionWindow = new(instructionCacheLine)
							b.instructionWindowTag = 17
							b.instructionWindowSerial = 19
							b.tlb = newNativeTLB()
							b.tlb[0] = tlbEntry{tag: 23, victim: 1, host: 0x12345678}
						}
						var want, got cpu.HostCallFrame
						want.Stack[0] = 0x12
						want.Parameters[0] = 0x34
						want.StackWords = 7
						want.ParameterWords = 5
						got = want
						check(t, cpu.CaptureHostCallFrame(expected, &want, cpu.HostCallFrameRequest{}))
						returnCPSR := want.Registers[cpu.RegisterCPSR]
						if lr&1 != 0 {
							returnCPSR |= cpu.StatusThumb
						} else {
							returnCPSR &^= cpu.StatusThumb
						}
						var commit cpu.RegisterCommit
						check(t, commit.Set(cpu.RegisterR0, 0xaabbccdd))
						check(t, commit.Set(cpu.RegisterR1, 0x11223344))
						check(t, commit.Set(cpu.RegisterPC, lr&^1))
						check(t, commit.Set(cpu.RegisterCPSR, returnCPSR))
						check(t, cpu.CommitHostCallRegisters(expected, commit))
						var target cpu.Backend = actual
						if fallback {
							target = hostReturnFallback{actual}
						}
						check(t, cpu.ReturnHostCallFrame(target, &got, 0xaabbccdd, 0x11223344))
						if actual.instructionWindow != nil || actual.instructionWindowTag != 0 || actual.instructionWindowSerial != 0 {
							t.Fatal("return retained the prior instruction fetch window")
						}
						if actual.tlb[0] != (tlbEntry{tag: 23, victim: 1, host: 0x12345678}) {
							t.Fatal("same-mode return invalidated data translations")
						}
						if got != want {
							t.Fatalf("return frame changed: mode=%x thumb=%x lr=%x fallback=%v", mode, thumb, lr, fallback)
						}
						before, err := expected.SaveContext()
						check(t, err)
						after, err := actual.SaveContext()
						check(t, err)
						if !bytes.Equal(before, after) {
							t.Fatalf("return context changed: mode=%x thumb=%x lr=%x fallback=%v", mode, thumb, lr, fallback)
						}
					}
				}
			}
			if !fallback {
				a, b := actual.ExecutionStatistics(), expected.ExecutionStatistics()
				if a.HostFrameCaptures != b.HostFrameCaptures || a.HostRegisterCommits != b.HostRegisterCommits || a.HostCallReturnBatches != a.HostRegisterCommits {
					t.Fatalf("return counters: actual=%+v expected=%+v", a, b)
				}
			}
		}
	}
	b := New()
	var frame cpu.HostCallFrame
	if !errors.Is(cpu.ReturnHostCallFrame(nil, &frame, 0, 0), cpu.ErrInvalidAddress) {
		t.Fatal("nil backend accepted")
	}
	if !errors.Is(cpu.ReturnHostCallFrame(b, nil, 0, 0), cpu.ErrInvalidAddress) {
		t.Fatal("nil frame accepted")
	}
	check(t, b.Close())
	if !errors.Is(cpu.ReturnHostCallFrame(b, &frame, 0, 0), cpu.ErrClosed) {
		t.Fatal("closed backend accepted")
	}
}
