package gvm_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type dataWriter struct {
	payload []byte
	err     error
}

func (w *dataWriter) WriteGVMData(payload []byte) error {
	if w.err != nil {
		return w.err
	}
	w.payload = payload
	return nil
}

func TestDataWriteCopiesTaggedSourceAndPops(t *testing.T) {
	writer := new(dataWriter)
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 1, 0x05, 2, 0x99, 0xff}, 0,
		gvm.AddressSpace{RAM: []byte{0, 0, 1, 2, 3, 4}},
		&gvm.ServiceConfig{DataWrite: writer},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(3); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	if !bytes.Equal(writer.payload, []byte{1, 2, 3, 4}) || len(vm.Stack()) != 0 || vm.PC() != 5 {
		t.Fatalf("write payload=%x stack=%x pc=%d", writer.payload, vm.Stack(), vm.PC())
	}
}

func TestDataWriteFailurePreservesVMState(t *testing.T) {
	boom := errors.New("write failed")
	for _, writer := range []gvm.DataWriteSink{nil, &dataWriter{err: boom}} {
		vm, err := gvm.NewWithAddressSpaceAndServices(
			[]byte{0x05, 0, 0x05, 1, 0x99}, 0,
			gvm.AddressSpace{RAM: []byte{9, 8}},
			&gvm.ServiceConfig{DataWrite: writer},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := vm.Run(2); !errors.Is(err, gvm.ErrBudget) {
			t.Fatal(err)
		}
		before := vm.Stack()
		if err := vm.Step(); err == nil || !bytes.Equal(wordsToBytes(vm.Stack()), wordsToBytes(before)) {
			t.Fatalf("failed write: err=%v stack=%x", err, vm.Stack())
		}
	}
}
