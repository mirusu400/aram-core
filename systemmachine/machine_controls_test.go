package systemmachine

import (
	"errors"
	"slices"
	"testing"

	"github.com/mirusu400/aram-core/system"
)

func TestBoardControlsIncludePrimaryClockKeys(t *testing.T) {
	board := system.SCHW830DL21BoardProfile()
	controls := make(map[string]bool)
	for _, id := range boardControls(board) {
		controls[id] = true
	}
	for _, id := range []string{"send", "back", "end", "volume-up", "volume-down"} {
		if !controls[id] {
			t.Fatalf("SCH-W830 controls have no %q", id)
		}
	}
	keys := boardPrimaryClockKeys(board)
	if key, ok := keys["end"]; !ok || key.InputLine != 4 || !key.ActiveLow {
		t.Fatalf("SCH-W830 END key = %+v, present=%t", key, ok)
	}
}

func TestMachineSetKeyDrivesActiveLowPrimaryClockInput(t *testing.T) {
	clock, err := system.NewQualcommPrimaryClockControl(system.QualcommPrimaryClockConfig{
		Status: 1 << 4, InputMask: 1 << 4,
	})
	check(t, err)
	machine := &Machine{
		primaryClock: clock,
		primaryKeys: map[string]system.QualcommPrimaryClockKeyProfile{
			"end": {ID: "end", InputLine: 4, ActiveLow: true},
		},
	}
	check(t, machine.SetKey("end", true))
	if status := clock.InputStatus(); status != 0 {
		t.Fatalf("pressed active-low END input status = %#x", status)
	}
	check(t, machine.SetKey("end", false))
	if status := clock.InputStatus(); status != 1<<4 {
		t.Fatalf("released active-low END input status = %#x", status)
	}
	if err := machine.SetKey("unknown", true); !errors.Is(err, ErrUnsupportedControl) {
		t.Fatalf("unknown primary-clock control error = %v", err)
	}
}

func TestMachineSetKeyDrivesActiveLowGPIOInputKey(t *testing.T) {
	board := system.SPHW4200DC17BoardProfile()
	controls := boardControls(board)
	if !slices.Equal(controls, []string{"volume-up", "volume-down", "end", "back", "send", "hold"}) {
		t.Fatalf("SPH-W4200 controls = %v", controls)
	}
	inputs, err := system.NewQualcommInterruptControllerWithConfig(system.QualcommInterruptControllerConfig{
		GPIOInputs:     board.BootControlGPIOInputs,
		GPIOInputLines: board.BootControlGPIOInputLines(),
	}, nil)
	check(t, err)
	machine := &Machine{gpioInputs: inputs, gpioInputKeys: boardGPIOInputKeys(board)}
	read := func() uint32 {
		t.Helper()
		value, err := inputs.Read(0x3c, system.Width32)
		check(t, err)
		return value
	}
	if value := read(); value&(1<<30) == 0 {
		t.Fatalf("idle HOLD input word = %#x", value)
	}
	check(t, machine.SetKey("hold", true))
	if value := read(); value&(1<<30) != 0 {
		t.Fatalf("pressed HOLD input word = %#x", value)
	}
	check(t, machine.SetKey("hold", false))
	if value := read(); value&(1<<30) == 0 {
		t.Fatalf("released HOLD input word = %#x", value)
	}
}
