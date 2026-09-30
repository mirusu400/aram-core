package gvm_test

import (
	"testing"

	"github.com/mirusu400/aram-core/gvm"
)

type pointRecorder struct {
	x, y, color int16
	calls       int
}

func (p *pointRecorder) DrawGVMPoint(x, y, color int16) error {
	p.x, p.y, p.color = x, y, color
	p.calls++
	return nil
}

func TestPointDrawUsesExplicitColorAndClips(t *testing.T) {
	recorder := new(pointRecorder)
	vm, err := gvm.NewWithAddressSpaceAndServices(
		[]byte{5, 2, 5, 3, 6, 0, 183, 0x58, 5, 2, 5, 3, 5, 4, 0x58, 5, 12, 5, 3, 5, 2, 0x58, 0xff},
		0, gvm.AddressSpace{}, &gvm.ServiceConfig{
			DeviceQuery: &gvm.DeviceQueryProfile{Width: 10, Height: 10}, PointDraw: recorder,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Run(13); err != nil || !vm.Halted() || recorder.calls != 1 ||
		recorder.x != 2 || recorder.y != 3 || recorder.color != 1 || len(vm.Stack()) != 0 {
		t.Fatalf("run=%v point=%+v stack=%x", err, recorder, vm.Stack())
	}
}
