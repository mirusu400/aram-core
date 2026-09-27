//go:build wasm

package interpreter

import (
	"testing"

	"github.com/mirusu400/aram-core/cpu"
)

func TestPrivateDataCacheRejectsNonPositiveScalarWidths(t *testing.T) {
	b := New()
	t.Cleanup(func() { _ = b.Close() })
	check(t, b.Map(0x1000, 16, cpu.PermissionRead|cpu.PermissionWrite))
	b.cacheData(&b.regions[0], 0x1000, cpu.PermissionRead)
	for _, width := range []int{-4, -1, 0} {
		if _, _, _, hit := b.privateDataHit(0x1000, width, cpu.PermissionRead); hit {
			t.Fatalf("non-positive scalar width %d produced a hit", width)
		}
	}
}
