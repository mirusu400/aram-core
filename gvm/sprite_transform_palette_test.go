package gvm_test

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type transformedPaletteSink struct {
	resource, palette []byte
	x, y              int16
	mirror            bool
}

func (s *transformedPaletteSink) DrawGVMTransformedSpriteWithPalette(resource, palette []byte, x, y int16, mirror bool) error {
	s.resource, s.palette, s.x, s.y, s.mirror = resource, palette, x, y, mirror
	return nil
}

func TestSpriteTransformPaletteUsesFiveOperands(t *testing.T) {
	sink := new(transformedPaletteSink)
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 0xfd, 0x05, 2, 0x05, 0, 0x05, 1, 0x05, 0, 0x72, 0xff}, 0,
		gvm.AddressSpace{RAM: []byte{3, 4, 5, 6}},
		&gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte{7}}}, SpriteTransformPalette: sink},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(7); err != nil {
		t.Fatal(err)
	}
	if sink.x != -3 || sink.y != 2 || !sink.mirror || !bytes.Equal(sink.resource, []byte{7}) ||
		!bytes.Equal(sink.palette, []byte{3, 4, 5, 6}) || len(vm.Stack()) != 0 {
		t.Fatalf("draw x=%d y=%d mirror=%t resource=%x palette=%x stack=%x", sink.x, sink.y, sink.mirror, sink.resource, sink.palette, vm.Stack())
	}
}
