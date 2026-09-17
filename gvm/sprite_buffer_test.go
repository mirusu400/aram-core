package gvm_test

import (
	"errors"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type spriteBufferSink struct {
	calls int
}

func (s *spriteBufferSink) DrawGVMSpriteBuffer(resource, buffer []byte, x, y int16) error {
	s.calls++
	buffer[0] = resource[0] + byte(x) + byte(y)
	return nil
}

func TestSpriteBufferService(t *testing.T) {
	sink := new(spriteBufferSink)
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 1, 0x05, 2, 0x05, 0, 0x05, 0, 0x71, 0xff}, 0,
		gvm.AddressSpace{RAM: make([]byte, 4)},
		&gvm.ServiceConfig{
			DeviceQuery: &gvm.DeviceQueryProfile{Width: 2, Height: 2},
			Media:       []gvm.MediaResource{{Data: []byte{3}}}, SpriteBuffer: sink,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(5); !errors.Is(err, gvm.ErrBudget) {
		t.Fatal(err)
	}
	word, err := vm.ReadWord(0)
	if err != nil || byte(word) != 6 || sink.calls != 1 || len(vm.Stack()) != 0 {
		t.Fatalf("word=%04x err=%v calls=%d stack=%x", word, err, sink.calls, vm.Stack())
	}
}
