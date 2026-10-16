package interpreter

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestEmptyHostCapture42LiteralSnapshotAndNoMemory(t *testing.T) {
	comparisons := 0
	for _, create := range []func() *Backend{New, NewJIT} {
		for _, thumb := range []uint32{0, cpu.StatusThumb} {
			for _, address := range []uint32{0, 1, 0x1000, 0xffffffff} {
				for _, pending := range []bool{false, true} {
					b := create()
					for register := uint32(0); register <= cpu.RegisterPC; register++ {
						check(t, b.WriteRegister(register, 0x13570000+register*4))
					}
					check(t, b.WriteRegister(cpu.RegisterSP, address))
					check(t, b.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)|thumb|flagN|flagC|flagV))
					if pending {
						b.flags = pendingFlags{dirty: true, mask: flagN | flagZ | flagC | flagV, bits: flagZ | flagC}
					}
					var frame cpu.HostCallFrame
					for i := range frame.Stack {
						frame.Stack[i], frame.Parameters[i] = 0x98760000+uint32(i), 0xabcd0000+uint32(i)
					}
					frame.StackWords, frame.ParameterWords = 17, 19
					want := frame
					want.Registers = b.regs
					if pending {
						want.Registers[cpu.RegisterCPSR] = uint32(processorModeSystem) | thumb | flagZ | flagC
					}
					want.StackWords, want.ParameterWords = 0, 0
					for i := range b.hostCallScratch {
						b.hostCallScratch[i] = 0xd7
					}
					scratch := b.hostCallScratch
					before := b.ExecutionStatistics()
					check(t, cpu.CaptureHostCallFrame(b, &frame, cpu.HostCallFrameRequest{ParameterAddress: address}))
					if frame != want || b.regs != want.Registers || b.hostCallScratch != scratch || b.flags != (pendingFlags{}) {
						t.Fatal("empty capture changed literal snapshot, raw flags, untouched words or scratch")
					}
					after := b.ExecutionStatistics()
					before.HostFrameCaptures++
					if after != before {
						t.Fatal("empty capture changed another execution counter")
					}
					check(t, b.Close())
					comparisons++
				}
			}
		}
	}
	if comparisons != 32 {
		t.Fatal("literal empty-request matrix incomplete")
	}
	t.Logf("empty_capture_comparisons=%d", comparisons)
}

func TestEmptyHostCapture42ValidationBeforeMutation(t *testing.T) {
	b := New()
	defer b.Close()
	b.flags = pendingFlags{dirty: true, mask: flagZ, bits: flagZ}
	var frame cpu.HostCallFrame
	frame.StackWords, frame.ParameterWords = 7, 9
	for _, request := range []cpu.HostCallFrameRequest{
		{StackWords: cpu.MaxHostCallWords + 1},
		{ParameterWords: cpu.MaxHostCallWords + 1, ParameterAddress: 0x1000},
		{ParameterWords: 1},
	} {
		beforeFrame, beforeFlags, beforeCounters := frame, b.flags, b.ExecutionStatistics()
		if err := b.CaptureHostCallFrame(&frame, request); !errors.Is(err, cpu.ErrInvalidAddress) {
			t.Fatal("invalid request did not retain its error")
		}
		if frame != beforeFrame || b.flags != beforeFlags || b.ExecutionStatistics() != beforeCounters {
			t.Fatal("invalid request changed state")
		}
	}
	if !errors.Is(b.CaptureHostCallFrame(nil, cpu.HostCallFrameRequest{}), cpu.ErrInvalidAddress) {
		t.Fatal("nil destination accepted")
	}
	check(t, b.Close())
	if !errors.Is(b.CaptureHostCallFrame(&frame, cpu.HostCallFrameRequest{}), cpu.ErrClosed) {
		t.Fatal("closed backend accepted")
	}
}

func TestEmptyHostCapture42AsymmetricNonemptyRequests(t *testing.T) {
	comparisons := 0
	stackData := [4]byte{0x11, 0x22, 0x33, 0x44}
	parameterData := [4]byte{0xa5, 0xb6, 0xc7, 0xd8}
	for _, create := range []func() *Backend{New, NewJIT} {
		for _, thumb := range []uint32{0, cpu.StatusThumb} {
			for _, parameterOnly := range []bool{false, true} {
				b := create()
				check(t, b.Map(0x1000, 0x80, cpu.PermissionRead|cpu.PermissionWrite))
				check(t, b.WriteMemory(0x1020, stackData[:]))
				check(t, b.WriteMemory(0x1040, parameterData[:]))
				for register := uint32(0); register <= cpu.RegisterPC; register++ {
					check(t, b.WriteRegister(register, 0x24680000+register*4))
				}
				check(t, b.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)|thumb|flagN|flagC|flagV))
				request := cpu.HostCallFrameRequest{StackWords: 1, ParameterAddress: 0xffffffff}
				check(t, b.WriteRegister(cpu.RegisterSP, 0x1020))
				if parameterOnly {
					request = cpu.HostCallFrameRequest{ParameterAddress: 0x1040, ParameterWords: 1}
					// A zero-word stack must not be consulted, even at an unmapped address.
					check(t, b.WriteRegister(cpu.RegisterSP, 0xffffffff))
				}
				var frame cpu.HostCallFrame
				for i := range frame.Stack {
					frame.Stack[i], frame.Parameters[i] = 0x98760000+uint32(i), 0xabcd0000+uint32(i)
				}
				frame.StackWords, frame.ParameterWords = 17, 19
				want := frame
				want.Registers = b.regs
				want.StackWords, want.ParameterWords = request.StackWords, request.ParameterWords
				if parameterOnly {
					want.Parameters[0] = 0xd8c7b6a5
				} else {
					want.Stack[0] = 0x44332211
				}
				for i := range b.hostCallScratch {
					b.hostCallScratch[i] = 0xe7
				}
				wantScratch := b.hostCallScratch
				if parameterOnly {
					copy(wantScratch[:4], parameterData[:])
				} else {
					copy(wantScratch[:4], stackData[:])
				}
				before, beforeMode := b.ExecutionStatistics(), b.mode
				check(t, b.CaptureHostCallFrame(&frame, request))
				if frame != want || b.regs != want.Registers || b.flags != (pendingFlags{}) ||
					b.mode != beforeMode || b.hostCallScratch != wantScratch {
					t.Fatal("asymmetric nonempty capture skipped the requested word or touched the empty side")
				}
				before.HostFrameCaptures++
				if b.ExecutionStatistics() != before {
					t.Fatal("asymmetric capture changed another execution counter")
				}
				var gotStack, gotParameters [4]byte
				check(t, b.ReadMemory(0x1020, gotStack[:]))
				check(t, b.ReadMemory(0x1040, gotParameters[:]))
				if gotStack != stackData || gotParameters != parameterData {
					t.Fatal("capture wrote guest memory")
				}
				check(t, b.Close())
				comparisons++
			}
		}
	}
	if comparisons != 8 {
		t.Fatal("asymmetric nonempty-request matrix incomplete")
	}
	t.Logf("asymmetric_capture_comparisons=%d", comparisons)
}

func TestEmptyHostCapture42PartialPendingFlagsPreserveOtherBits(t *testing.T) {
	comparisons := 0
	for _, create := range []func() *Backend{New, NewJIT} {
		for _, thumb := range []uint32{0, cpu.StatusThumb} {
			for _, pending := range []struct {
				mask, bits, literalFlags uint32
			}{
				{flagN | flagZ, flagZ, flagZ | flagC | flagV},
				{flagC, 0, flagN | flagV},
			} {
				b := create()
				for register := uint32(0); register <= cpu.RegisterPC; register++ {
					check(t, b.WriteRegister(register, 0x35790000+register*4))
				}
				check(t, b.WriteRegister(cpu.RegisterSP, 0xffffffff))
				check(t, b.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)|thumb|flagN|flagC|flagV))
				b.flags = pendingFlags{dirty: true, mask: pending.mask, bits: pending.bits}
				var frame cpu.HostCallFrame
				for i := range frame.Stack {
					frame.Stack[i], frame.Parameters[i] = 0x98760000+uint32(i), 0xabcd0000+uint32(i)
				}
				frame.StackWords, frame.ParameterWords = 17, 19
				want := frame
				want.Registers = b.regs
				// Literal constants, not resolveFlags or the candidate's merge expression.
				want.Registers[cpu.RegisterCPSR] = uint32(processorModeSystem) | thumb | pending.literalFlags
				want.StackWords, want.ParameterWords = 0, 0
				for i := range b.hostCallScratch {
					b.hostCallScratch[i] = 0xc3
				}
				scratch := b.hostCallScratch
				before, beforeMode := b.ExecutionStatistics(), b.mode
				check(t, b.CaptureHostCallFrame(&frame, cpu.HostCallFrameRequest{ParameterAddress: 0xffffffff}))
				if frame != want || b.regs != want.Registers || b.flags != (pendingFlags{}) ||
					b.mode != beforeMode || b.hostCallScratch != scratch {
					t.Fatal("empty capture lost unmasked CPSR bits or retained pending flags")
				}
				before.HostFrameCaptures++
				if b.ExecutionStatistics() != before {
					t.Fatal("partial-flags capture changed another execution counter")
				}
				check(t, b.Close())
				comparisons++
			}
		}
	}
	if comparisons != 8 {
		t.Fatal("partial pending-flags matrix incomplete")
	}
	t.Logf("partial_pending_capture_comparisons=%d", comparisons)
}
