package system

import (
	"errors"
	"testing"
)

func testQualcommGPIOKeypadProfile() QualcommGPIOKeypadProfile {
	return QualcommGPIOKeypadProfile{
		Columns: []uint8{0, 1, 3},
		Rows: []QualcommGPIOKeypadRowProfile{
			{OutputOffset: 0x08, OutputMask: 0x00000400},
			{OutputOffset: 0x08, OutputMask: 0x00000800},
			{OutputOffset: 0x10, OutputMask: 0x00200000},
		},
		Keys: []QualcommGPIOKeyProfile{
			{ID: "digit-1", Row: 1, Column: 1},
			{ID: "power", Row: 2, Column: 2},
		},
	}
}

func TestQualcommGPIOKeypadFollowsFirmwareRowSelection(t *testing.T) {
	keypad, err := NewQualcommGPIOKeypad(testQualcommGPIOKeypadProfile())
	if err != nil {
		t.Fatal(err)
	}
	if got := keypad.InputStatus(0x1f); got != 0x1f {
		t.Fatalf("idle keypad status = %#x", got)
	}
	if err := keypad.SetKey("digit-1", true); err != nil {
		t.Fatal(err)
	}

	// The firmware's pre-scan drives every row high. A pressed key therefore
	// pulls its column input low before the row-by-row scan begins.
	keypad.ObserveGPIOWrite(0x08, 0x00000c00)
	keypad.ObserveGPIOWrite(0x10, 0x00200000)
	if got := keypad.InputStatus(0x1f); got != 0x1d {
		t.Fatalf("pre-scan keypad status = %#x, want column 1 low", got)
	}

	// Row 0 selected: the row-1 key must disappear.
	keypad.ObserveGPIOWrite(0x08, 0x00000400)
	keypad.ObserveGPIOWrite(0x10, 0)
	if got := keypad.InputStatus(0x1f); got != 0x1f {
		t.Fatalf("unselected row keypad status = %#x", got)
	}

	// Row 1 selected: the same host key now drives column 1 low.
	keypad.ObserveGPIOWrite(0x08, 0x00000800)
	if got := keypad.InputStatus(0x1f); got != 0x1d {
		t.Fatalf("selected row keypad status = %#x", got)
	}
	if err := keypad.SetKey("digit-1", false); err != nil {
		t.Fatal(err)
	}
	if got := keypad.InputStatus(0x1f); got != 0x1f {
		t.Fatalf("released keypad status = %#x", got)
	}
	if err := keypad.SetKey("missing", true); !errors.Is(err, ErrQualcommGPIOKeypad) {
		t.Fatalf("unknown key error = %v", err)
	}
}

func TestQualcommGPIOKeypadIntegratesWithGPIOWritesAndPrimaryInputs(t *testing.T) {
	keypad, err := NewQualcommGPIOKeypad(testQualcommGPIOKeypadProfile())
	if err != nil {
		t.Fatal(err)
	}
	primary, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
		Status: 0x1f, InputMask: 0x1f,
	})
	if err != nil {
		t.Fatal(err)
	}
	interrupt := NewQualcommInterruptController(nil)
	if err := primary.AttachGPIOKeypad(keypad); err != nil {
		t.Fatal(err)
	}
	if err := interrupt.AttachGPIOWriteObserver(keypad); err != nil {
		t.Fatal(err)
	}
	if err := keypad.SetMatrixKey(2, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := interrupt.Write(qualcommGPIOInterruptClear4Offset, Width32, 0x00200000); err != nil {
		t.Fatal(err)
	}
	value, err := primary.Read(qualcommPrimaryGPIOInputOffset, Width32)
	if err != nil || value != 0x17 {
		t.Fatalf("wired primary input status = %#x error %v", value, err)
	}

	state, err := primary.SaveState()
	if err != nil {
		t.Fatal(err)
	}
	restoredKeypad, _ := NewQualcommGPIOKeypad(testQualcommGPIOKeypadProfile())
	restoredPrimary, _ := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
		Status: 0x1f, InputMask: 0x1f,
	})
	if err := restoredPrimary.AttachGPIOKeypad(restoredKeypad); err != nil {
		t.Fatal(err)
	}
	if err := restoredPrimary.LoadState(state); err != nil {
		t.Fatal(err)
	}
	if got := restoredPrimary.InputStatus(); got != 0x17 {
		t.Fatalf("restored keypad input status = %#x", got)
	}
	if err := restoredPrimary.Reset(); err != nil {
		t.Fatal(err)
	}
	if got := restoredPrimary.InputStatus(); got != 0x1f {
		t.Fatalf("reset keypad input status = %#x", got)
	}
}

func TestQualcommGPIOKeypadRejectsInvalidProfilesAndState(t *testing.T) {
	for _, profile := range []QualcommGPIOKeypadProfile{
		{},
		{Columns: []uint8{0, 0}, Rows: []QualcommGPIOKeypadRowProfile{{OutputOffset: 8, OutputMask: 1}}},
		{Columns: []uint8{32}, Rows: []QualcommGPIOKeypadRowProfile{{OutputOffset: 8, OutputMask: 1}}},
		{Columns: []uint8{0}, Rows: []QualcommGPIOKeypadRowProfile{{OutputOffset: 3, OutputMask: 1}}},
		{Columns: []uint8{0}, Rows: []QualcommGPIOKeypadRowProfile{{OutputOffset: 8}}},
		{
			Columns: []uint8{0},
			Rows:    []QualcommGPIOKeypadRowProfile{{OutputOffset: 8, OutputMask: 1}},
			Keys:    []QualcommGPIOKeyProfile{{ID: "bad-row", Row: 1, Column: 0}},
		},
		{
			Columns: []uint8{0},
			Rows:    []QualcommGPIOKeypadRowProfile{{OutputOffset: 8, OutputMask: 1}},
			Keys: []QualcommGPIOKeyProfile{
				{ID: "same", Row: 0, Column: 0},
				{ID: "same", Row: 0, Column: 0},
			},
		},
	} {
		if _, err := NewQualcommGPIOKeypad(profile); err == nil {
			t.Fatalf("accepted invalid keypad profile %#v", profile)
		}
	}

	keypad, _ := NewQualcommGPIOKeypad(testQualcommGPIOKeypadProfile())
	state, _ := keypad.SaveState()
	if err := keypad.LoadState(state[:len(state)-1]); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("truncated keypad state error = %v", err)
	}
	mismatchProfile := testQualcommGPIOKeypadProfile()
	mismatchProfile.Rows[0].OutputMask = 0x4000
	mismatch, _ := NewQualcommGPIOKeypad(mismatchProfile)
	if err := mismatch.LoadState(state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched keypad state error = %v", err)
	}
}

func TestQualcommGPIOKeypadStateIgnoresHostAliasesAndSafelyMigratesLegacyFingerprint(t *testing.T) {
	profile := testQualcommGPIOKeypadProfile()
	keypad, err := NewQualcommGPIOKeypad(profile)
	if err != nil {
		t.Fatal(err)
	}
	state, err := keypad.SaveState()
	if err != nil {
		t.Fatal(err)
	}

	aliasedProfile := profile
	aliasedProfile.Keys = append(
		append([]QualcommGPIOKeyProfile(nil), profile.Keys...),
		QualcommGPIOKeyProfile{ID: "alias", Row: 1, Column: 0},
	)
	aliased, err := NewQualcommGPIOKeypad(aliasedProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := aliased.LoadState(state); err != nil {
		t.Fatalf("host alias changed electrical state contract: %v", err)
	}

	legacyState := append([]byte(nil), state...)
	legacyState[8] ^= 0xff
	if err := aliased.LoadState(legacyState); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("strict load accepted legacy fingerprint: %v", err)
	}
	if err := aliased.LoadStateSubset(legacyState); err != nil {
		t.Fatalf("subset load rejected released legacy keypad state: %v", err)
	}

	heldLegacyState := append([]byte(nil), legacyState...)
	heldLegacyState[len(heldLegacyState)-1] = 1
	if err := aliased.LoadStateSubset(heldLegacyState); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("subset load accepted held key under legacy fingerprint: %v", err)
	}
}

func TestBoardProfileAttachesProfileSelectedKeypad(t *testing.T) {
	keypadProfile := testQualcommGPIOKeypadProfile()
	profile := BoardProfile{
		ID: "test.board", PlatformID: "test.platform", FirmwareBuildID: "test.firmware",
		PrimaryClockStatus: 0x1f, PrimaryClockInputMask: 0x1f,
		Keypad: &keypadProfile,
	}
	primary, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
		Status: 0x1f, InputMask: 0x1f,
	})
	if err != nil {
		t.Fatal(err)
	}
	interrupt := NewQualcommInterruptController(nil)
	keypad, err := profile.AttachKeypad(primary, nil, interrupt)
	if err != nil {
		t.Fatal(err)
	}
	if keypad == nil {
		t.Fatal("profile did not attach its keypad")
	}
	if err := keypad.SetKey("digit-1", true); err != nil {
		t.Fatal(err)
	}
	if err := interrupt.Write(qualcommGPIOInterruptClear0Offset, Width32, 0x00000800); err != nil {
		t.Fatal(err)
	}
	if got := primary.InputStatus(); got != 0x1d {
		t.Fatalf("profile-wired keypad input status = %#x", got)
	}

	withoutKeypad := profile
	withoutKeypad.Keypad = nil
	keypad, err = withoutKeypad.AttachKeypad(nil, nil, nil)
	if err != nil || keypad != nil {
		t.Fatalf("profile without keypad returned keypad %p error %v", keypad, err)
	}

	invalid := profile
	invalid.PrimaryClockInputMask = 1
	if err := invalid.Validate(); err == nil {
		t.Fatal("accepted keypad columns outside primary-clock input mask")
	}
}

func TestSCHW830ProfileMapsKnownKeypadControls(t *testing.T) {
	profile := SCHW830DL21BoardProfile()
	if profile.Keypad == nil {
		t.Fatal("SCH-W830 profile has no keypad")
	}
	primary, err := NewQualcommPrimaryClockControl(QualcommPrimaryClockConfig{
		Status: profile.PrimaryClockStatus, InputMask: profile.PrimaryClockInputMask,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := NewQualcommSecondaryClockControlWithWritableOffsets(profile.SecondaryClockWritableOffsets)
	if err != nil {
		t.Fatal(err)
	}
	interrupt := NewQualcommInterruptController(nil)
	keypad, err := profile.AttachKeypad(primary, secondary, interrupt)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"soft-left", "soft-right",
		"digit-0", "digit-1", "digit-2", "digit-3", "digit-4",
		"digit-5", "digit-6", "digit-7", "digit-8", "digit-9", "star", "pound",
	} {
		if err := keypad.SetKey(id, true); err != nil {
			t.Fatalf("profiled key %q: %v", id, err)
		}
		if err := keypad.SetKey(id, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := keypad.SetKey("digit-1", true); err != nil {
		t.Fatal(err)
	}
	if err := secondary.Write(0x0400, Width32, 0x00002000); err != nil {
		t.Fatal(err)
	}
	if got := primary.InputStatus(); got != 0x1d {
		t.Fatalf("SCH-W830 digit-1 selected input = %#x", got)
	}
	if err := keypad.SetKey("digit-1", false); err != nil {
		t.Fatal(err)
	}
	if err := keypad.SetKey("soft-left", true); err != nil {
		t.Fatal(err)
	}
	if err := interrupt.Write(qualcommGPIOInterruptClear4Offset, Width32, 0x00200000); err != nil {
		t.Fatal(err)
	}
	if got := primary.InputStatus(); got != 0x1e {
		t.Fatalf("SCH-W830 left soft key selected input = %#x", got)
	}
	if err := keypad.SetKey("soft-left", false); err != nil {
		t.Fatal(err)
	}
	if err := keypad.SetKey("soft-right", true); err != nil {
		t.Fatal(err)
	}
	if got := primary.InputStatus(); got != 0x1d {
		t.Fatalf("SCH-W830 right soft key selected input = %#x", got)
	}
}
