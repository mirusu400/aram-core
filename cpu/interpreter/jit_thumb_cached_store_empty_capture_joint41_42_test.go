package interpreter

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Joint regression oracle for cached stores and empty host-call captures.
// All words/addresses are public synthetic fixtures; no private input is used.
var joint41And42Stores = []struct {
	name  string
	raw   uint16
	width int
}{
	{"register-word", 0x5088, 4},
	{"register-half", 0x5288, 2},
	{"register-byte", 0x5488, 1},
	{"immediate-word", 0x6008, 4},
	{"immediate-byte", 0x7008, 1},
	{"immediate-half", 0x8008, 2},
	{"sp-word", 0x9000, 4},
}

type joint41And42Expected struct {
	reason          cpu.StopReason
	retired         uint64
	pc, cpsr        uint32
	r3, r5          uint32
	blockWords      int
	fullApplication bool
	code, data      []byte
}

func joint41And42ScalarCode(raw uint16) []byte {
	code := make([]byte, 0x100)
	copy(code, thumbTestCode(0x3401, raw, 0x3501, 0x2e00, 0xd1fa, 0xbe00))
	return code
}

func joint41And42Capture(t *testing.T, b *Backend, literalCPSR uint32) cpu.HostCallFrame {
	t.Helper()
	var frame cpu.HostCallFrame
	for i := range frame.Registers {
		frame.Registers[i] = 0xa1560000 + uint32(i)
	}
	for i := range frame.Stack {
		frame.Stack[i] = 0x98760000 + uint32(i)
		frame.Parameters[i] = 0xabcd0000 + uint32(i)
	}
	frame.StackWords, frame.ParameterWords = 17, 19
	want := frame
	want.Registers = b.regs
	// Independent full CPSR literals, not resolveFlags or a candidate flag merge.
	want.Registers[cpu.RegisterCPSR] = literalCPSR
	want.StackWords, want.ParameterWords = 0, 0

	beforeCP15, beforeMode := b.cp15, b.mode
	beforeMapGen, beforeJITGen := b.mappingGen, b.jitGen
	beforeTable, beforeScratch := b.mmuTLBTable, b.hostCallScratch
	before := b.ExecutionStatistics()
	if before.HostFrameCaptures != 0 {
		t.Fatal("joint fixture captured a host frame before the public call")
	}

	// No SaveContext/CPSR getter between the actual Run and this public bulk call.
	check(t, cpu.CaptureHostCallFrame(b, &frame, cpu.HostCallFrameRequest{
		ParameterAddress: 0xffffffff,
	}))
	if frame != want || b.regs != want.Registers {
		t.Fatal("joint empty capture changed its literal frame, words or registers")
	}
	if b.cp15 != beforeCP15 || b.mode != beforeMode ||
		b.mappingGen != beforeMapGen || b.jitGen != beforeJITGen ||
		b.mmuTLBTable != beforeTable || b.hostCallScratch != beforeScratch {
		t.Fatal("joint empty capture changed execution/mapping state or scratch")
	}
	before.HostFrameCaptures++
	// Profile builds clone ThumbMicro and its maps on EVERY statistics query.
	// Compare contents, never the freshly cloned pointer identities.
	if after := b.ExecutionStatistics(); !reflect.DeepEqual(after, before) {
		t.Fatal("joint empty capture changed a counter other than HostFrameCaptures+1")
	}
	return frame
}

func joint41And42Exercise(t *testing.T, precise, jit *Backend, budget uint64, want joint41And42Expected) {
	t.Helper()
	if jit.jitBlocks == nil || jit.nativeBlocks != nil || jit.systemBus != nil ||
		jit.physicalAccess || jit.tlb != nil || jit.tracing() || len(jit.executionTraps) != 0 {
		t.Fatal("joint fixture did not establish ordinary unobserved private Go JIT admission")
	}
	block := jit.jitBlockAt(0x1000)
	if block == nil || len(block.thumb) != want.blockWords ||
		block.compareBranchTail == nil || block.extraLoop != nil || block.countedLoop != nil {
		t.Fatal("joint fixture did not establish its complete ordinary application block")
	}
	if want.fullApplication && budget < uint64(len(block.thumb)) {
		t.Fatal("positive joint case has insufficient full-application budget")
	}

	backends := []*Backend{precise, jit}
	var first [2]cpu.Result
	var frames [2]cpu.HostCallFrame
	for i, b := range backends {
		for offset := range b.hostCallScratch {
			b.hostCallScratch[offset] = 0xc3 ^ byte(offset)
		}
		first[i] = b.Run(context.Background(), 0x1000, cpu.ModeThumb, budget)
		result := first[i]
		if result.Reason != want.reason || result.Instructions != want.retired ||
			result.PC != want.pc || (result.Err != nil) != (want.reason == cpu.StopFault) {
			t.Fatal("joint Run changed its literal reason, retirement, PC or fault presence")
		}
		frames[i] = joint41And42Capture(t, b, want.cpsr)
		if frames[i].Registers[cpu.RegisterR3] != want.r3 ||
			frames[i].Registers[cpu.RegisterR4] != 0x80000000 ||
			frames[i].Registers[cpu.RegisterR5] != want.r5 {
			t.Fatal("joint Run/capture changed literal prefix, suffix or SMC registers")
		}
	}
	if first[0].Reason != first[1].Reason || first[0].Instructions != first[1].Instructions ||
		first[0].PC != first[1].PC || !sameError(first[0].Err, first[1].Err) || frames[0] != frames[1] {
		t.Fatal("public precise Run plus empty capture differs from public Go JIT")
	}

	// Capture the VERY NEXT instruction before any SaveContext/register/memory getter.
	// If capture left incorrect pending flags, this real recordPC path exposes them.
	var resumed [2]cpu.Result
	var observations [2][]PCRegisterCapture
	for i, b := range backends {
		check(t, b.SetPCRegisterCapture(want.pc, 1))
		resumed[i] = b.Run(context.Background(), want.pc, cpu.ModeThumb, 1)
		observations[i] = b.PCRegisterCaptures()
		if resumed[i].Reason != cpu.StopBudget || resumed[i].Instructions != 1 ||
			resumed[i].PC != want.pc+2 || resumed[i].Err != nil ||
			len(observations[i]) != 1 || observations[i][0].Address != want.pc ||
			observations[i][0].Registers != frames[i].Registers ||
			observations[i][0].Registers[cpu.RegisterCPSR] != want.cpsr {
			t.Fatal("joint next-instruction capture changed literal flags, snapshot or one-step budget")
		}
	}
	if resumed[0].Reason != resumed[1].Reason || resumed[0].Instructions != resumed[1].Instructions ||
		resumed[0].PC != resumed[1].PC || !sameError(resumed[0].Err, resumed[1].Err) ||
		!reflect.DeepEqual(observations[0], observations[1]) {
		t.Fatal("joint real next-instruction captures/results differ")
	}

	// The next instruction is only ADDS or LSL#0, never another store.
	// Public getters are deliberately delayed until both raw captures are complete.
	for _, b := range backends {
		code := make([]byte, len(want.code))
		check(t, b.ReadMemory(0x1000, code))
		if !bytes.Equal(code, want.code) {
			t.Fatal("joint store/SMC changed unexpected code bytes")
		}
		if want.data != nil {
			data := make([]byte, len(want.data))
			check(t, b.ReadMemory(0x3000, data))
			if !bytes.Equal(data, want.data) {
				t.Fatal("joint store/fault did not preserve literal mapped bytes")
			}
		}
		if b.ExecutionStatistics().HostFrameCaptures != 1 {
			t.Fatal("joint sequence performed more or less than one host capture")
		}
	}
	oldContext, err := precise.SaveContext()
	check(t, err)
	newContext, err := jit.SaveContext()
	check(t, err)
	if !bytes.Equal(oldContext, newContext) {
		t.Fatal("joint post-resume public architectural contexts differ")
	}
}

func TestCachedStoreEmptyCaptureJoint41_42Scalar(t *testing.T) {
	comparisons := 0
	for _, op := range joint41And42Stores {
		t.Run(op.name+"/warm", func(t *testing.T) {
			precise, jit := cachedStore41Pair(t, op.raw, 0x3000, true, false)
			data := make([]byte, 16)
			for lane := 0; lane < op.width; lane++ {
				data[lane] = byte(uint32(0x89abcdef) >> (8 * lane))
			}
			joint41And42Exercise(t, precise, jit, 5, joint41And42Expected{
				reason: cpu.StopBudget, retired: 5, pc: 0x1000, cpsr: 0x2000003f,
				r5: 1, blockWords: 5, fullApplication: true,
				code: joint41And42ScalarCode(op.raw), data: data,
			})
			comparisons++
		})
	}
	t.Run("immediate-word/cold", func(t *testing.T) {
		precise, jit := cachedStore41Pair(t, 0x6008, 0x3000, false, false)
		data := make([]byte, 16)
		copy(data, []byte{0xef, 0xcd, 0xab, 0x89})
		joint41And42Exercise(t, precise, jit, 5, joint41And42Expected{
			reason: cpu.StopBudget, retired: 5, pc: 0x1000, cpsr: 0x2000003f,
			r5: 1, blockWords: 5, fullApplication: true,
			code: joint41And42ScalarCode(0x6008), data: data,
		})
		comparisons++
	})
	if comparisons != 8 {
		t.Fatal("joint scalar named-case set is incomplete")
	}
	t.Logf("joint41_42_scalar_cases=%d", comparisons)
}

func TestCachedStoreEmptyCaptureJoint41_42Boundaries(t *testing.T) {
	comparisons := 0
	for _, op := range joint41And42Stores {
		faultName := "unmapped"
		address := uint32(0x3010)
		if op.width == 2 {
			faultName, address = "cross-region", 0x300f
		}
		t.Run(op.name+"/"+faultName, func(t *testing.T) {
			// Establish the real valid write cache first, then move only the
			// relevant address register before Run. Warm entry range proof MUST miss.
			precise, jit := cachedStore41Pair(t, op.raw, 0x3000, true, false)
			for _, b := range []*Backend{precise, jit} {
				register := cpu.RegisterR1
				if op.raw == 0x9000 {
					register = cpu.RegisterSP
				}
				check(t, b.WriteRegister(register, address))
			}
			data := make([]byte, 16)
			if op.width == 2 {
				// Scalar write16 commits the first byte, then faults at the next region.
				data[15] = 0xef
			}
			joint41And42Exercise(t, precise, jit, 5, joint41And42Expected{
				reason: cpu.StopFault, retired: 1, pc: 0x1004, cpsr: 0x9000003f,
				blockWords: 5, fullApplication: true,
				code: joint41And42ScalarCode(op.raw), data: data,
			})
			comparisons++
		})
	}
	t.Run("warm-smc/old-compare-tail", func(t *testing.T) {
		precise, jit := New(), NewJIT()
		for _, b := range []*Backend{precise, jit} {
			t.Cleanup(func() { _ = b.Close() })
			check(t, b.Map(0x1000, 0x100, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
			check(t, b.WriteMemory(0x1000, thumbTestCode(
				0x3401, 0x8008, 0x2a00, 0xd102, 0x2301, 0xbe00, 0x46c0, 0x2302, 0xbe00,
			)))
			check(t, b.WriteRegister(cpu.RegisterCPSR, uint32(processorModeSystem)|flagC|flagV))
			check(t, b.WriteRegister(cpu.RegisterR0, 0x2a01))
			check(t, b.WriteRegister(cpu.RegisterR1, 0x1004))
			check(t, b.WriteRegister(cpu.RegisterR2, 0))
			check(t, b.WriteRegister(cpu.RegisterR4, 0x7fffffff))
			check(t, b.WriteRegister(cpu.RegisterSP, 0xffffffff))
			mapped, _, err := b.findRegion(0x1004, cpu.PermissionWrite)
			check(t, err)
			b.cacheData(mapped, 0x1004, cpu.PermissionWrite)
		}
		stale := jit.jitBlockAt(0x1000)
		if stale == nil || len(stale.thumb) != 4 || stale.compareBranchTail == nil {
			t.Fatal("joint SMC fixture did not establish the old complete fused tail")
		}
		generation := jit.jitGen
		code := make([]byte, 0x100)
		copy(code, thumbTestCode(
			0x3401, 0x8008, 0x2a00, 0xd102, 0x2301, 0xbe00, 0x46c0, 0x2302, 0xbe00,
		))
		code[4], code[5] = 0x01, 0x2a
		// ADDS / STRH / new CMP#1 / BNE / MOV r3,#2 / BKPT = six retired.
		// Last MOV clears N/Z; CMP(0,1) left C/V clear: literal final CPSR=0x3f.
		joint41And42Exercise(t, precise, jit, 257, joint41And42Expected{
			reason: cpu.StopBreakpoint, retired: 6, pc: 0x1012, cpsr: 0x0000003f,
			r3: 2, blockWords: 4, fullApplication: true, code: code,
		})
		if jit.jitGen == generation || jit.jitBlocks[0x1000] == stale {
			t.Fatal("joint real code store reused the old tail or failed to invalidate generation")
		}
		comparisons++
	})
	t.Run("partial-budget/guarded-only", func(t *testing.T) {
		precise, jit := cachedStore41Pair(t, 0x6008, 0x3000, true, false)
		data := make([]byte, 16)
		copy(data, []byte{0xef, 0xcd, 0xab, 0x89})
		// Budget2 is deliberately a guarded partial-block CONTROL, not positive
		// evidence for the complete application cached-store runner.
		joint41And42Exercise(t, precise, jit, 2, joint41And42Expected{
			reason: cpu.StopBudget, retired: 2, pc: 0x1004, cpsr: 0x9000003f,
			blockWords: 5, fullApplication: false,
			code: joint41And42ScalarCode(0x6008), data: data,
		})
		comparisons++
	})
	if comparisons != 9 {
		t.Fatal("joint boundary named-case set is incomplete")
	}
	t.Logf("joint41_42_boundary_cases=%d", comparisons)
}
