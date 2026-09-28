package gvm_test

import (
	"slices"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestBranchGreaterThanSignedImmediate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value byte
		want  []uint16
	}{
		{"greater", 5, []uint16{}},
		{"equal", 4, []uint16{1}},
		{"less", 3, []uint16{1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := gvm.New([]byte{0x05, tc.value, 0x3b, 4, 0, 8, 0x05, 1, 0xff})
			if err := vm.Run(4); err != nil {
				t.Fatal(err)
			}
			if !vm.Halted() || !slices.Equal(vm.Stack(), tc.want) {
				t.Fatalf("halted=%v stack=%v, want %v", vm.Halted(), vm.Stack(), tc.want)
			}
		})
	}
}
