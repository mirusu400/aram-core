package gvm_test

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type spritePaletteSink struct {
	resource, palette []byte
	x, y              int16
}

func (s *spritePaletteSink) DrawGVMSpriteWithPalette(resource, palette []byte, x, y int16) error {
	s.resource, s.palette, s.x, s.y = resource, palette, x, y
	return nil
}

func TestSpritePaletteServiceReadsGuestSource(t *testing.T) {
	sink := new(spritePaletteSink)
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{0x05, 1, 0x05, 2, 0x05, 0, 0x05, 0, 0x71, 0xff}, 0,
		gvm.AddressSpace{RAM: []byte{3, 4, 5, 6}},
		&gvm.ServiceConfig{Media: []gvm.MediaResource{{Data: []byte{7}}}, SpritePalette: sink},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(6); err != nil {
		t.Fatal(err)
	}
	if sink.x != 1 || sink.y != 2 || !bytes.Equal(sink.resource, []byte{7}) ||
		!bytes.Equal(sink.palette, []byte{3, 4, 5, 6}) || len(vm.Stack()) != 0 {
		t.Fatalf("draw x=%d y=%d resource=%x palette=%x stack=%x", sink.x, sink.y, sink.resource, sink.palette, vm.Stack())
	}
	word, err := vm.ReadWord(0)
	if err != nil || word != 0x0403 {
		t.Fatalf("palette source was modified: word=%04x err=%v", word, err)
	}
}
