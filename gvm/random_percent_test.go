package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
	gruntime "github.com/mirusu400/aram-core/runtime"
)

func TestRandomPercentReplacesThreshold(t *testing.T) {
	random := gruntime.NewRandom(1, 1)
	if err := random.SetLCG214013Seed("percent", 7); err != nil {
		t.Fatal(err)
	}
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 100, 0xa2, 0x05, 0, 0xa2, 0xff}, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{Random: random, RandomStream: "percent"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(5); err != nil || len(vm.Stack()) != 2 || vm.Stack()[0] != 1 || vm.Stack()[1] != 0 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
	state := random.Snapshot()
	if len(state.Streams) != 1 || state.Streams[0].Draws != 2 {
		t.Fatalf("random state = %+v", state)
	}
}

func TestRandomPercentSignedThreshold(t *testing.T) {
	random := gruntime.NewRandom(1, 1)
	if err := random.SetLCG214013Seed("percent", 1); err != nil {
		t.Fatal(err)
	}
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 0xff, 0xa2, 0xff}, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{Random: random, RandomStream: "percent"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); err != nil || len(vm.Stack()) != 1 || vm.Stack()[0] != 0 {
		t.Fatalf("run=%v stack=%x", err, vm.Stack())
	}
}

func TestRandomPercentRequiresExplicitService(t *testing.T) {
	legacy := gvm.New([]byte{0x05, 1, 0xa2})
	var unsupported *gvm.UnsupportedOpcodeError
	if err := legacy.Run(2); !errors.As(err, &unsupported) || unsupported.Opcode != 0xa2 {
		t.Fatalf("legacy error = %v", err)
	}
	vm, err := gvm.NewWithAddressSpaceAndServices([]byte{0x05, 1, 0xa2}, 0, gvm.AddressSpace{}, &gvm.ServiceConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(2); !errors.Is(err, gvm.ErrRandomUnavailable) {
		t.Fatalf("service error = %v", err)
	}
}
