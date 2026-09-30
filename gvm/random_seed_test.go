package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
	gruntime "github.com/mirusu400/aram-core/runtime"
)

func TestRandomSeedControlsFollowingRange(t *testing.T) {
	seed := uint16(0x8001)
	random := gruntime.NewRandom(99, 2)
	if err := random.SetLCG214013Seed("game", 123); err != nil {
		t.Fatal(err)
	}
	program := []byte{0x06, 0x80, 0x01, 0xa0, 0x05, 0, 0x05, 100, 0xa1, 0xff}
	vm, err := gvm.NewWithAddressSpaceAndServices(program, 0, gvm.AddressSpace{},
		&gvm.ServiceConfig{Random: random, RandomStream: "game"})
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(5); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	wantState := uint32(int32(int16(seed)))*214013 + 2531011
	want := uint16(((wantState >> 16) & 0x7fff) % 100)
	if got := vm.Stack(); len(got) != 1 || got[0] != want {
		t.Fatalf("seeded range: got=%x want=%d", got, want)
	}
	stream := random.Snapshot().Streams[0]
	if stream.State[0] != uint64(wantState) || stream.Draws != 1 {
		t.Fatalf("seeded stream: %+v", stream)
	}
}

func TestRandomSeedNeedsConfiguredServiceAndOperand(t *testing.T) {
	program := []byte{0xa0}
	for _, services := range []*gvm.ServiceConfig{nil, {}} {
		var vm *gvm.VM
		var err error
		if services == nil {
			vm = gvm.New(program)
		} else {
			vm, err = gvm.NewWithAddressSpaceAndServices(program, 0, gvm.AddressSpace{}, services)
		}
		if err != nil {
			t.Fatal(err)
		}
		err = vm.Step()
		if services == nil {
			var unsupported *gvm.UnsupportedOpcodeError
			if !errors.As(err, &unsupported) || unsupported.Opcode != 0xa0 {
				t.Fatalf("legacy seed: %v", err)
			}
		} else if !errors.Is(err, gvm.ErrStackUnderflow) {
			t.Fatalf("missing operand: %v", err)
		}
	}
}
