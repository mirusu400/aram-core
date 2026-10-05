package system

import (
	"errors"
	"testing"
)

func testQualcommTSC2007Profile() QualcommTSC2007Profile {
	return QualcommTSC2007Profile{
		Width: 240, Height: 432,
		PenInputOffset: 0x0440, PenInputMask: 1,
		InterruptGroup: QualcommGPIOInterruptGroupProfile{
			ClearOffset: 0x0594, EnableOffset: 0x05a8,
			DetectOffset: 0x05bc, PolarityOffset: 0x05d0,
			StatusOffset:    0x05e4,
			InterruptSource: 5, UseVectoredController: true,
		},
		InterruptMask: 1,
	}
}

func TestQualcommTSC2007PublishesPenGPIOAndCalibratedSamples(t *testing.T) {
	touchscreen, err := NewQualcommTSC2007(testQualcommTSC2007Profile())
	check(t, err)
	secondary, err := NewQualcommSecondaryClockControlWithConfig(QualcommSecondaryClockConfig{
		ReadOnlyRegisters: []QualcommSecondaryClockReadOnlyRegister{{Offset: 0x0440, Value: 0x10}},
	})
	check(t, err)
	check(t, secondary.AttachGPIOReadObserver(touchscreen))
	if value, readErr := secondary.Read(0x0440, Width32); readErr != nil || value != 0x11 {
		t.Fatalf("released pen input = %#x error %v", value, readErr)
	}

	check(t, touchscreen.SetTouch(120, 216, true))
	if value, readErr := secondary.Read(0x0440, Width32); readErr != nil || value != 0x10 {
		t.Fatalf("pressed pen input = %#x error %v", value, readErr)
	}
	touchscreen.WriteCommand(0xc4)
	if sample := touchscreen.ReadSample(); sample < 2040 || sample > 2042 {
		t.Fatalf("center X sample = %d", sample)
	}
	touchscreen.WriteCommand(0xd0)
	if sample := touchscreen.ReadSample(); sample < 2040 || sample > 2042 {
		t.Fatalf("center Y sample = %d", sample)
	}
	check(t, touchscreen.SetTouch(120, 216, false))
}

func TestQualcommTSC2007RoutesAndSerializesPenInterrupt(t *testing.T) {
	profile := testQualcommTSC2007Profile()
	probe := &interruptLineProbe{}
	vic, err := NewQualcommVectoredInterruptController(QualcommVectoredInterruptConfig{
		SourceCount: 49, Bank0Sources: 25, ReverseSourceOrder: true,
	}, probe)
	check(t, err)
	check(t, vic.Write(qualcommVICEnable1Offset, Width32, ^uint32(0)))
	touchscreen, err := NewQualcommTSC2007(profile)
	check(t, err)
	check(t, touchscreen.AttachInterruptControllers(nil, vic))
	primary, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
		WritableOffsets: []uint32{0x0594, 0x05a8, 0x05bc, 0x05d0},
	})
	check(t, err)
	check(t, primary.AttachTouchscreen(touchscreen))
	check(t, primary.Write(0x05a8, Width32, 1))
	check(t, touchscreen.SetTouch(20, 30, true))
	if status, readErr := primary.Read(0x05e4, Width32); readErr != nil || status != 1 {
		t.Fatalf("pen GPIO pending = %#x error %v", status, readErr)
	}
	if banks := vic.PendingStatusBanks(); banks == [2]uint32{} {
		t.Fatal("pen GPIO did not pulse compact VIC source 5")
	}

	state, err := primary.SaveState()
	check(t, err)
	restoredTouchscreen, _ := NewQualcommTSC2007(profile)
	restoredPrimary, _ := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
		WritableOffsets: []uint32{0x0594, 0x05a8, 0x05bc, 0x05d0},
	})
	check(t, restoredPrimary.AttachTouchscreen(restoredTouchscreen))
	check(t, restoredPrimary.LoadState(state))
	if status, readErr := restoredPrimary.Read(0x05e4, Width32); readErr != nil || status != 1 {
		t.Fatalf("restored pen GPIO pending = %#x error %v", status, readErr)
	}
	check(t, restoredPrimary.Write(0x0594, Width32, 1))
	if status, _ := restoredPrimary.Read(0x05e4, Width32); status != 0 {
		t.Fatalf("cleared pen GPIO pending = %#x", status)
	}
	if err := restoredPrimary.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated touchscreen state error = %v", err)
	}
}

func TestQualcommTSC2007RejectsOutOfPanelTouch(t *testing.T) {
	touchscreen, err := NewQualcommTSC2007(testQualcommTSC2007Profile())
	check(t, err)
	for _, coordinate := range [][2]int{{-1, 0}, {0, -1}, {240, 0}, {0, 432}} {
		if err := touchscreen.SetTouch(coordinate[0], coordinate[1], true); !errors.Is(err, ErrQualcommTSC2007) {
			t.Fatalf("touch (%d,%d) error = %v", coordinate[0], coordinate[1], err)
		}
	}
}
