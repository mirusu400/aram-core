package system

import (
	"errors"
	"image/color"
	"reflect"
	"testing"
)

func TestDCSPanelControllerDecodesRGB565AddressWindow(t *testing.T) {
	controller, err := NewDCSPanelController(DCSPanelConfig{Width: 2, Height: 2})
	if err != nil {
		t.Fatal(err)
	}
	panel, err := NewParallelPanelInterfaceWithController(controller)
	if err != nil {
		t.Fatal(err)
	}
	writePanelCommand(t, panel, dcsSetPixelFormat, dcsPixelFormatRGB565)
	writePanelCommand(t, panel, dcsSetAddressMode, 0)
	writePanelCommand(t, panel, dcsSetColumnAddress, 0, 0, 0, 1)
	writePanelCommand(t, panel, dcsSetPageAddress, 0, 0, 0, 1)
	writePanelCommand(t, panel, dcsWriteMemoryStart, 0xf800, 0x07e0, 0x001f, 0xffff)
	if got := controller.FrameRGB565(); len(got) != 4 ||
		got[0] != 0xf800 || got[1] != 0x07e0 || got[2] != 0x001f || got[3] != 0xffff {
		t.Fatalf("RGB565 frame = %#v", got)
	}
	pixels, updates := controller.WriteCounts()
	if pixels != 4 || updates != 1 {
		t.Fatalf("panel write counts = %d/%d", pixels, updates)
	}
	frame := controller.FrameRGBA()
	if got := frame.RGBAAt(0, 0); got != (color.RGBA{R: 0xff, A: 0xff}) {
		t.Fatalf("RGBA red pixel = %+v", got)
	}
	if got := frame.RGBAAt(1, 1); got != (color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
		t.Fatalf("RGBA white pixel = %+v", got)
	}
}

func TestDCSPanelControllerAcceptsDBIOnlyRGB565Encoding(t *testing.T) {
	controller, err := NewDCSPanelController(DCSPanelConfig{Width: 1, Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	panel, err := NewParallelPanelInterfaceWithController(controller)
	if err != nil {
		t.Fatal(err)
	}
	// DCS 0x3A separates the DBI format in bits 2:0 from the DPI format in
	// bits 6:4. Parallel-only controllers may program just the DBI field.
	writePanelCommand(t, panel, dcsSetPixelFormat, dcsPixelFormatDBIRGB565)
	writePanelCommand(t, panel, dcsWriteMemoryStart, 0x1234)
	if got := controller.FrameRGB565(); len(got) != 1 || got[0] != 0x1234 {
		t.Fatalf("DBI-only RGB565 frame = %#v", got)
	}
}

func TestDCSPanelControllerAppliesAddressModeAndPartialWindow(t *testing.T) {
	controller, _ := NewDCSPanelController(DCSPanelConfig{Width: 3, Height: 2})
	panel, _ := NewParallelPanelInterfaceWithController(controller)
	writePanelCommand(t, panel, dcsSetPixelFormat, dcsPixelFormatRGB565)
	writePanelCommand(t, panel, dcsSetAddressMode, dcsAddressModeColumnReverse|dcsAddressModeBGR)
	writePanelCommand(t, panel, dcsSetColumnAddress, 0, 0, 0, 1)
	writePanelCommand(t, panel, dcsSetPageAddress, 0, 1, 0, 1)
	writePanelCommand(t, panel, dcsWriteMemoryStart, 0xf800, 0x07e0)
	frame := controller.FrameRGB565()
	if frame[1*3+2] != 0x001f || frame[1*3+1] != 0x07e0 {
		t.Fatalf("oriented/BGR frame = %#v", frame)
	}
	writePanelCommand(t, panel, dcsWriteMemoryContinue, 0x001f, 0xffff)
	frame = controller.FrameRGB565()
	if frame[1*3+2] != 0xf800 || frame[1*3+1] != 0xffff {
		t.Fatalf("continued/wrapped frame = %#v", frame)
	}
}

func TestDCSPanelControllerTreatsNativeAddressModeAsUprightRGB(t *testing.T) {
	const nativeMode = dcsAddressModeColumnReverse | dcsAddressModeBGR
	controller, _ := NewDCSPanelController(DCSPanelConfig{
		Width: 2, Height: 1, NativeAddressMode: nativeMode,
	})
	panel, _ := NewParallelPanelInterfaceWithController(controller)
	writePanelCommand(t, panel, dcsSetPixelFormat, dcsPixelFormatRGB565)
	writePanelCommand(t, panel, dcsSetAddressMode, nativeMode)
	writePanelCommand(t, panel, dcsSetColumnAddress, 0, 0, 0, 1)
	writePanelCommand(t, panel, dcsSetPageAddress, 0, 0, 0, 0)
	writePanelCommand(t, panel, dcsWriteMemoryStart, 0xf800, 0x001f)
	if got := controller.FrameRGB565(); len(got) != 2 || got[0] != 0xf800 || got[1] != 0x001f {
		t.Fatalf("native-orientation frame = %#v", got)
	}
}

func TestIndexedRGB565PanelDecodesRegisterWindowAndGRAM(t *testing.T) {
	config := DCSPanelConfig{
		Width: 3, Height: 2, Protocol: ParallelPanelProtocolIndexedRGB565,
	}
	controller, err := NewDCSPanelController(config)
	if err != nil {
		t.Fatal(err)
	}
	panel, err := NewParallelPanelInterfaceWithController(controller)
	if err != nil {
		t.Fatal(err)
	}
	writePanelCommand(t, panel, 0x03, 0x0201)
	writePanelCommand(t, panel, 0x04, 0)
	writePanelCommand(t, panel, 0x05, 1)
	writePanelCommand(t, panel, 0x20, 1)
	writePanelCommand(t, panel, 0x21, 0)
	writePanelCommand(t, panel, 0x22, 0xf800, 0x07e0, 0x001f, 0xffff)
	if got := controller.FrameRGB565(); len(got) != 6 ||
		got[1] != 0xf800 || got[2] != 0x07e0 || got[4] != 0x001f || got[5] != 0xffff {
		t.Fatalf("indexed RGB565 frame = %#v", got)
	}
	state, err := panel.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restoredController, _ := NewDCSPanelController(config)
	restored, _ := NewParallelPanelInterfaceWithController(restoredController)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got := restoredController.FrameRGB565(); !reflect.DeepEqual(got, controller.FrameRGB565()) {
		t.Fatalf("restored indexed RGB565 frame = %#v", got)
	}
	wrongController, _ := NewDCSPanelController(DCSPanelConfig{Width: 3, Height: 2})
	wrong, _ := NewParallelPanelInterfaceWithController(wrongController)
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("indexed state loaded into DCS controller: %v", err)
	}
}

func TestIndexedRGB565Window454647IgnoresVendorRegisterFour(t *testing.T) {
	panel, err := NewDCSPanelController(DCSPanelConfig{
		Width: 3, Height: 2, Protocol: ParallelPanelProtocolIndexedRGB565Window454647,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, write := range []struct {
		command, value uint16
	}{
		{0x04, 0x6627},
		{0x45, 0x0002},
		{0x46, 0},
		{0x47, 1},
		{0x20, 0},
		{0x21, 0},
		{0x22, 0x1234},
	} {
		if err := panel.WriteCommand(write.command); err != nil {
			t.Fatal(err)
		}
		if err := panel.WriteData(write.value); err != nil {
			t.Fatalf("command %#x data %#x: %v", write.command, write.value, err)
		}
	}
	if got := panel.FrameRGB565(); len(got) != 6 || got[0] != 0x1234 {
		t.Fatalf("window-454647 frame = %#v", got)
	}
}

func TestPackedRGB565Window424ADecodesCommandFIFOAndPixelFIFO(t *testing.T) {
	config := DCSPanelConfig{
		Width: 3, Height: 320, Protocol: ParallelPanelProtocolPackedRGB565Window424A,
	}
	controller, err := NewDCSPanelController(config)
	if err != nil {
		t.Fatal(err)
	}
	panel, err := NewParallelPanelInterfaceWithController(controller)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []uint16{
		0x0510,
		0x4501, 0x4602,
		0x4701, 0x483e,
		// Moving from page 238 to 319 temporarily combines high byte 1 with
		// the previous low byte 0xee. The low-byte write completes 0x013f.
		0x4900, 0x4aee, 0x4901, 0x4a3f,
		0x4201, 0x4301, 0x443e,
	} {
		if err := panel.Write(0, Width16, uint32(command)); err != nil {
			t.Fatalf("packed command %#x: %v", command, err)
		}
	}
	for _, pixel := range []uint16{0xf800, 0x07e0, 0x001f, 0xffff} {
		if err := panel.Write(ParallelPanelDataOffset, Width16, uint32(pixel)); err != nil {
			t.Fatalf("packed pixel %#x: %v", pixel, err)
		}
	}
	frame := controller.FrameRGB565()
	if frame[318*3+1] != 0xf800 || frame[318*3+2] != 0x07e0 ||
		frame[319*3+1] != 0x001f || frame[319*3+2] != 0xffff {
		t.Fatalf("packed RGB565 frame tail = %#v", frame[len(frame)-6:])
	}
	columnStart, columnEnd, pageStart, pageEnd := controller.AddressWindow()
	if window := [4]uint16{columnStart, columnEnd, pageStart, pageEnd}; window != [4]uint16{1, 2, 318, 319} {
		t.Fatalf("packed address window = %#v", window)
	}
	pixels, updates := controller.WriteCounts()
	if pixels != 4 || updates != 1 {
		t.Fatalf("packed panel write counts = %d/%d", pixels, updates)
	}

	state, err := panel.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restoredController, _ := NewDCSPanelController(config)
	restored, _ := NewParallelPanelInterfaceWithController(restoredController)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got := restoredController.FrameRGB565(); !reflect.DeepEqual(got, frame) {
		t.Fatal("packed RGB565 state did not restore its framebuffer")
	}
}

func TestDCSPanelControllerRejectsMalformedCommonCommands(t *testing.T) {
	controller, _ := NewDCSPanelController(DCSPanelConfig{Width: 2, Height: 2})
	panel, _ := NewParallelPanelInterfaceWithController(controller)
	if err := panel.Write(0, Width16, dcsWriteMemoryStart); err != nil {
		t.Fatal(err)
	}
	if err := panel.Write(ParallelPanelDataOffset, Width16, 0); !errors.Is(err, ErrDCSPanel) {
		t.Fatalf("memory write without RGB565 error = %v", err)
	}
	writePanelCommand(t, panel, dcsSetColumnAddress, 0, 0, 0)
	if err := panel.Write(ParallelPanelDataOffset, Width16, 2); !errors.Is(err, ErrDCSPanel) {
		t.Fatalf("out-of-range column error = %v", err)
	}
	if err := panel.Write(0, Width16, 0xf0); err != nil {
		t.Fatal(err)
	}
	if err := panel.Write(ParallelPanelDataOffset, Width16, 0xffff); err != nil {
		t.Fatalf("vendor command data error = %v", err)
	}
}

func TestDCSPanelControllerStateRoundTripThroughTransport(t *testing.T) {
	controller, _ := NewDCSPanelController(DCSPanelConfig{Width: 2, Height: 1})
	panel, _ := NewParallelPanelInterfaceWithController(controller)
	writePanelCommand(t, panel, dcsExitSleepMode)
	writePanelCommand(t, panel, dcsSetDisplayOn)
	writePanelCommand(t, panel, dcsSetPixelFormat, dcsPixelFormatRGB565)
	writePanelCommand(t, panel, dcsSetColumnAddress, 0, 0, 0, 1)
	writePanelCommand(t, panel, dcsSetPageAddress, 0, 0, 0, 0)
	writePanelCommand(t, panel, dcsWriteMemoryStart, 0x1234, 0xabcd)
	state, err := panel.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restoredController, _ := NewDCSPanelController(DCSPanelConfig{Width: 2, Height: 1})
	restored, _ := NewParallelPanelInterfaceWithController(restoredController)
	if err := restored.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got := restoredController.FrameRGB565(); len(got) != 2 || got[0] != 0x1234 || got[1] != 0xabcd {
		t.Fatalf("restored DCS frame = %#v", got)
	}
	sleepOut, displayOn := restoredController.PowerState()
	if !sleepOut || !displayOn {
		t.Fatalf("restored power state = %t/%t", sleepOut, displayOn)
	}
	wrongController, _ := NewDCSPanelController(DCSPanelConfig{Width: 1, Height: 2})
	wrong, _ := NewParallelPanelInterfaceWithController(wrongController)
	if err := wrong.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched DCS configuration state error = %v", err)
	}
	wrongModeController, _ := NewDCSPanelController(DCSPanelConfig{
		Width: 2, Height: 1, NativeAddressMode: dcsAddressModeColumnReverse,
	})
	wrongMode, _ := NewParallelPanelInterfaceWithController(wrongModeController)
	if err := wrongMode.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched DCS native-mode state error = %v", err)
	}
	plain := NewParallelPanelInterface()
	if err := plain.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("controller state loaded without controller: %v", err)
	}
}

func writePanelCommand(t *testing.T, panel *ParallelPanelInterface, command uint16, data ...uint16) {
	t.Helper()
	if err := panel.Write(0, Width16, uint32(command)); err != nil {
		t.Fatal(err)
	}
	for _, value := range data {
		if err := panel.Write(ParallelPanelDataOffset, Width16, uint32(value)); err != nil {
			t.Fatal(err)
		}
	}
}
