//go:build aram_profile_ops

package interpreter

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestThumbApplicationPCProfileCountsPureBudgetAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		for _, budget := range append(thumbPCOracleBudgets(65), 511, 512, 513, 514) {
			t.Run(fmt.Sprintf("cancel=%t/budget=%d", cancel, budget), func(t *testing.T) {
				precise, jit := newThumbPCOraclePair(t, 0x402, false)
				for _, b := range []*Backend{precise, jit} {
					words := make([]uint16, 512, 513)
					for i := range words {
						words[i] = 0x3001
					}
					check(t, b.WriteMemory(0x1000, thumbTestCode(append(words, 0xbe00)...)))
				}
				var preciseContext, jitContext context.Context = context.Background(), context.Background()
				if cancel {
					preciseContext, jitContext = &thumbPCSecondPollContext{}, &thumbPCSecondPollContext{}
				}
				wantResult := precise.Run(preciseContext, 0x1000, cpu.ModeThumb, budget)
				gotResult := jit.Run(jitContext, 0x1000, cpu.ModeThumb, budget)
				if gotResult.Reason != wantResult.Reason || gotResult.Instructions != wantResult.Instructions ||
					gotResult.PC != wantResult.PC || !sameError(gotResult.Err, wantResult.Err) {
					t.Fatalf("result=%+v want=%+v", gotResult, wantResult)
				}
				// Literal 256-instruction pure blocks, followed by one BKPT block.
				retired := min(budget, uint64(513))
				if cancel {
					retired = min(retired, uint64(256))
				}
				want := &cpu.ThumbMicroStatistics{Blocks: make(map[uint32]uint64)}
				want.BlockCalls = (retired + 255) / 256
				want.Classes[thumbAddImmediate] = min(retired, uint64(512))
				if retired == 513 {
					want.Classes[thumbBreakpoint] = 1
				}
				for block := uint64(0); block < want.BlockCalls; block++ {
					want.Blocks[0x1000+uint32(block)*512] = 1
				}
				if got := jit.ExecutionStatistics().ThumbMicro; !reflect.DeepEqual(got, want) {
					t.Fatalf("statistics=%+v want=%+v", got, want)
				}
			})
		}
	}
}
