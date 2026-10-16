//go:build aram_profile_ops

package interpreter

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

// Literal attempted-class counts, not inferred from the production fast path.
func TestCachedStore41ProfileFaultAndWholeBlock(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(fmt.Sprint(fault), func(t *testing.T) {
			address := uint32(0x3000)
			if fault {
				address = 0x300f
			}
			precise, jit := cachedStore41Pair(t, 0x8008, address, true, false)
			want := &cpu.ThumbMicroStatistics{BlockCalls: 1, Blocks: map[uint32]uint64{0x1000: 1}}
			want.Classes[thumbAddImmediate] = 1
			want.Classes[thumbHalfwordTransfer] = 1 // The failed attempt is counted.
			if !fault {
				want.Classes[thumbAddImmediate] = 2
				want.Classes[thumbCompareImmediate] = 1
				want.Classes[thumbConditionalBranch] = 1
			}
			assertThumbScalarStoreParity(t, precise, jit, 5)
			if got := jit.ExecutionStatistics().ThumbMicro; !reflect.DeepEqual(got, want) {
				t.Fatalf("literal store/fault profile differs: got=%+v want=%+v", got, want)
			}
		})
	}
}
