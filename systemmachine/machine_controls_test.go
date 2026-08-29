package systemmachine

import (
	"errors"
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
	if err != nil {
		t.Fatal(err)
	}
	machine := &Machine{
		primaryClock: clock,
		primaryKeys: map[string]system.QualcommPrimaryClockKeyProfile{
			"end": {ID: "end", InputLine: 4, ActiveLow: true},
		},
	}
	if err := machine.SetKey("end", true); err != nil {
		t.Fatal(err)
	}
	if status := clock.InputStatus(); status != 0 {
		t.Fatalf("pressed active-low END input status = %#x", status)
	}
	if err := machine.SetKey("end", false); err != nil {
		t.Fatal(err)
	}
	if status := clock.InputStatus(); status != 1<<4 {
		t.Fatalf("released active-low END input status = %#x", status)
	}
	if err := machine.SetKey("unknown", true); !errors.Is(err, ErrUnsupportedControl) {
		t.Fatalf("unknown primary-clock control error = %v", err)
	}
}
