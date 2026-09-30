package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

func TestMediaVirtualSuffixRequiresExplicitBoundedPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		enabled       bool
		want          uint16
		wantErr       error
	}{
		{"enabled", "A\x00", true, '|', nil},
		{"disabled", "A\x00", false, 0, gvm.ErrInvalidElement},
		{"not-terminated", "AB", true, 0, gvm.ErrInvalidElement},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, err := gvm.NewWithAddressSpaceAndServices(
				[]byte{0x05, 0, 0x05, 2, 0x81}, 0, gvm.AddressSpace{},
				&gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte(tc.payload), VirtualSuffix: '|', HasVirtualSuffix: tc.enabled}}},
			)
			if err != nil {
				t.Fatal(err)
			}
			err = vm.Run(3)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Run = %v", err)
				}
				return
			}
			if !errors.Is(err, gvm.ErrBudget) || len(vm.Stack()) != 1 || vm.Stack()[0] != tc.want {
				t.Fatalf("Run=%v stack=%v", err, vm.Stack())
			}
		})
	}
}
