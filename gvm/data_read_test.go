package gvm_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type dataReader struct {
	payload []byte
	err     error
}

func (r dataReader) ReadGVMData(size uint32) ([]byte, error) {
	if r.err != nil {
		return nil, r.err
	}
	return append([]byte(nil), r.payload...), nil
}

func dataReadVM(t *testing.T, service gvm.DataReadSink) *gvm.VM {
	t.Helper()
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 1, 0x05, 2, 0x98, 0xff}, 0,
		gvm.AddressSpace{RAM: make([]byte, 8)},
		&gvm.ServiceConfig{DataRead: service},
	)
	if err != nil {
		t.Fatal(err)
	}
	return vm
}

func TestDataReadCopiesExactPayloadAndPops(t *testing.T) {
	vm := dataReadVM(t, dataReader{payload: []byte{1, 2, 3, 4}})
	if err := vm.Run(3); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	for index, want := range []uint16{0x0201, 0x0403} {
		got, err := vm.ReadWord(uint16(index + 1))
		if err != nil || got != want {
			t.Fatalf("word %d = %04x, %v", index, got, err)
		}
	}
	if len(vm.Stack()) != 0 || vm.PC() != 5 {
		t.Fatalf("stack=%x pc=%d", vm.Stack(), vm.PC())
	}
}

func TestDataReadFailureIsAtomic(t *testing.T) {
	boom := errors.New("read failed")
	for _, service := range []gvm.DataReadSink{nil, dataReader{err: boom}, dataReader{payload: []byte{1}}} {
		vm := dataReadVM(t, service)
		if err := vm.Run(2); !errors.Is(err, gvm.ErrBudget) {
			t.Fatal(err)
		}
		before := vm.Stack()
		err := vm.Step()
		if err == nil || !bytes.Equal(wordsToBytes(vm.Stack()), wordsToBytes(before)) {
			t.Fatalf("service=%T err=%v stack=%x", service, err, vm.Stack())
		}
	}
}

func wordsToBytes(words []uint16) []byte {
	result := make([]byte, 0, len(words)*2)
	for _, word := range words {
		result = append(result, byte(word), byte(word>>8))
	}
	return result
}
