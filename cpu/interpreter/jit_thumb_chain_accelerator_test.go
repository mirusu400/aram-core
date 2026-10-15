package interpreter

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Enter through two ordinary blocks so the generated full-block runner reaches
// an actual successor lookup before encountering the accelerator. Calling Run
// directly at the accelerated target would not exercise the chain exclusion.
func thumbChainAcceleratorEntry(t *testing.T, b *Backend) {
	t.Helper()
	for _, address := range []uint32{0x8000, 0x8100} {
		check(t, b.Map(address, 0x20, cpu.PermissionRead|cpu.PermissionWrite|cpu.PermissionExecute))
	}
	check(t, b.WriteMemory(0x8000, thumbTestCode(0x2209, 0x4760))) // MOVS r2,#9; BX r12
	check(t, b.WriteMemory(0x8100, thumbTestCode(0x3201, 0x4758))) // ADDS r2,#1; BX r11
	check(t, b.WriteRegister(cpu.RegisterR12, 0x8101))
	check(t, b.WriteRegister(cpu.RegisterR11, 0x1001))
	// Environment PC tracing must not silently disable the application runner.
	b.pcHits, b.pcHistory, b.pcCaptureLimit = nil, nil, 0
}

func TestThumbApplicationChainPreservesAcceleratorBoundaries(t *testing.T) {
	for _, family := range []string{"counted", "halfword-fill", "load-mask"} {
		for _, variant := range []string{"recognized", "ordinary-control", "declined"} {
			t.Run(fmt.Sprintf("%s/%s", family, variant), func(t *testing.T) {
				var precise, jit *Backend
				var wantInstructions, wantIterations, wantAccelerated, wantFused uint64
				declined := variant == "declined"
				switch family {
				case "counted":
					counter := uint32(12)
					if declined {
						counter = 1
					}
					program := countedLoopTestPrograms()[0]
					precise = configureApplicationCountedLoop(t, New, program, counter)
					jit = configureApplicationCountedLoop(t, newLoopAccelerationBackend, program, counter)
					wantInstructions = 4 + uint64(counter)*2 + 2
					if !declined {
						wantIterations, wantAccelerated = uint64(counter-1), uint64(counter-1)*2
					}
				case "halfword-fill":
					destination := uint32(0x5000)
					if declined {
						destination++ // The accelerator rejects odd STRH addresses.
					}
					precise = newThumbHalfwordFillTestBackend(t, New(), destination)
					jit = newThumbHalfwordFillTestBackend(t, NewJIT(), destination)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1000+thumbHalfwordFillLoopInstructions*2, thumbTestCode(0xbe00)))
					}
					wantInstructions = 4 + 4*uint64(thumbHalfwordFillLoopInstructions) + 1
					if !declined {
						wantIterations, wantAccelerated = 4, 4*uint64(thumbHalfwordFillLoopInstructions)
					}
				case "load-mask":
					registers := [6]uint16{5, 1, 3, 1, 3, 3}
					precise = loadMaskBranchBackend(t, New(), 0, registers, 7, 0x3000, true)
					jit = loadMaskBranchBackend(t, NewJIT(), 0, registers, 7, 0x3000, true)
					for _, b := range []*Backend{precise, jit} {
						check(t, b.WriteMemory(0x1010, thumbTestCode(0xbe00))) // Taken EQ successor.
					}
					wantInstructions = 4 + 5 + 1
					if !declined {
						wantFused = 1
					}
				}
				for _, b := range []*Backend{precise, jit} {
					thumbChainAcceleratorEntry(t, b)
					if family == "load-mask" {
						if declined {
							b.clearDataCaches() // A cold read cannot take the fused path.
						} else {
							_, err := b.read32(0x3000, cpu.PermissionRead)
							check(t, err)
						}
					}
				}
				from, middle := jit.jitBlockAt(0x8000), jit.jitBlockAt(0x8100)
				if from == nil || middle == nil || len(from.thumb) != 2 || len(middle.thumb) != 2 ||
					from.countedLoop != nil || from.extraLoop != nil || middle.countedLoop != nil || middle.extraLoop != nil {
					t.Fatal("fixture did not create two ordinary entry blocks")
				}
				if got := jit.thumbApplicationSuccessor(from, middle.start); got != middle {
					t.Fatal("ordinary entry successor was not linkable")
				}
				target := jit.jitBlockAt(0x1000)
				if target == nil {
					t.Fatal("accelerator target did not translate")
				}
				switch family {
				case "counted":
					if target.countedLoop == nil || target.extraLoop != nil {
						t.Fatal("counted-loop signature was not recognized")
					}
				case "halfword-fill":
					if target.extraLoop == nil || target.extraLoop.kind != jitHalfwordFillLoop {
						t.Fatal("halfword-fill signature was not recognized")
					}
				case "load-mask":
					if target.extraLoop == nil || target.extraLoop.kind != jitLoadMaskBranch {
						t.Fatal("load-mask signature was not recognized")
					}
				}
				if variant == "ordinary-control" {
					// Test-only control: the same decoded instructions, without the
					// optional host accelerator, must remain ordinary-chain eligible.
					target.countedLoop, target.extraLoop = nil, nil
					wantIterations, wantAccelerated, wantFused = 0, 0, 0
					if got := jit.thumbApplicationSuccessor(middle, target.start); got != target {
						t.Fatal("ordinary control target was not linkable")
					}
				} else {
					if got := jit.thumbApplicationSuccessor(middle, target.start); got != nil {
						t.Fatal("application successor bypassed a recognized accelerator")
					}
					for _, successor := range middle.successors {
						if successor == target {
							t.Fatal("refused accelerator target was cached as an ordinary successor")
						}
					}
				}
				wantResult := precise.Run(context.Background(), from.start, cpu.ModeThumb, 64)
				gotResult := jit.Run(context.Background(), from.start, cpu.ModeThumb, 64)
				if gotResult.Reason != wantResult.Reason || gotResult.Instructions != wantResult.Instructions ||
					gotResult.PC != wantResult.PC || !sameError(gotResult.Err, wantResult.Err) ||
					gotResult.Reason != cpu.StopBreakpoint || gotResult.Err != nil || gotResult.Instructions != wantInstructions {
					t.Fatalf("chain result=%+v, precise=%+v, want breakpoint after %d instructions", gotResult, wantResult, wantInstructions)
				}
				if jit.regs[cpu.RegisterPC] != precise.regs[cpu.RegisterPC] || jit.mode != precise.mode {
					t.Fatal("architectural PC/mode differed after the chain")
				}
				wantContext, err := precise.SaveContext()
				check(t, err)
				gotContext, err := jit.SaveContext()
				check(t, err)
				if !bytes.Equal(gotContext, wantContext) {
					t.Fatal("complete architectural context differed from precise execution")
				}
				for index, region := range precise.regions {
					if region.address != jit.regions[index].address || region.permissions != jit.regions[index].permissions ||
						!bytes.Equal(region.data, jit.regions[index].data) {
						t.Fatalf("guest mapping/memory at %08x differs", region.address)
					}
				}
				if family == "counted" && jit.regs[cpu.RegisterR0] != 0 ||
					family == "halfword-fill" && jit.regs[cpu.RegisterR4] != ^uint32(0) ||
					jit.regs[cpu.RegisterR2] != 10 {
					t.Fatal("chain or accelerator did not complete the intended fixture")
				}
				stats := jit.ExecutionStatistics()
				if stats.AcceleratedLoopIterations != wantIterations || stats.AcceleratedLoopInstructions != wantAccelerated {
					t.Fatalf("accelerator attribution = %d/%d, want %d/%d", stats.AcceleratedLoopIterations, stats.AcceleratedLoopInstructions, wantIterations, wantAccelerated)
				}
				if collectThumbMicro {
					micro := stats.ThumbMicro
					if micro == nil || micro.Blocks[from.start] != 1 || micro.Blocks[middle.start] != 1 || micro.FusedLoadMaskBlocks != wantFused {
						t.Fatalf("entry/fused attribution differs: %+v, want fused=%d", micro, wantFused)
					}
					var recorded uint64
					for _, count := range micro.Classes {
						recorded += count
					}
					if recorded+wantAccelerated+5*wantFused != wantInstructions {
						t.Fatalf("recorded=%d + accelerated=%d + fused=%d does not account for %d retired instructions", recorded, wantAccelerated, 5*wantFused, wantInstructions)
					}
				} else if stats.ThumbMicro != nil {
					t.Fatal("ordinary build collected micro-profile state")
				}
			})
		}
	}
}
