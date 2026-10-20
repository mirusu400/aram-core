package gnex32

import (
	"bytes"
	"testing"

	"github.com/mirusu400/aram-core/loader/gnex"
)

func gnex32TestDeviceProfile() DeviceProfile {
	return DeviceProfile{
		ID:                  "gnex32/test",
		ScreenWidth:         120,
		ScreenHeight:        149,
		Identity:            Identity{MIN: "0111234567", UserID: "98B4970F"},
		Platform:            "WIPI 1.2",
		KeyToneEnabled:      true,
		MMIBacklightEnabled: true,
	}
}

func TestDeviceProfileSuppliesScreenIdentityAndHandsetDefaults(t *testing.T) {
	profile := DeviceProfile{
		ID:                  "gnex32/test/custom",
		ScreenWidth:         77,
		ScreenHeight:        99,
		Identity:            Identity{MIN: "0100000000", UserID: "CUSTOM"},
		Platform:            "WIPI 2.0",
		KeyToneEnabled:      false,
		MMIBacklightEnabled: false,
	}
	symbols := make([]gnex.GNEX32Symbol, 6)
	for index := range symbols {
		symbols[index] = gnex.GNEX32Symbol{Flags: 1, Words: 1, Data: make([]byte, 4)}
	}
	runtime, err := NewRuntime(gnex.GNEX32Image{
		Header: gnex.Header{BodyOffset: 160}, Entry: 160, Code: []byte{0x86, 0},
		Symbols: symbols,
		Media:   []gnex.GNEX32Media{{Flags: 1}, {Flags: 1}},
	}, profile)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()

	if got := runtime.display.working.Bounds(); got.Dx() != 77 || got.Dy() != 99 {
		t.Fatalf("display bounds = %v", got)
	}
	if got := gnex32TestSymbolWords(t, runtime, 4, 1)[0]; got != 77 {
		t.Fatalf("swWidth = %d, want 77", got)
	}
	if got := gnex32TestSymbolWords(t, runtime, 5, 1)[0]; got != 99 {
		t.Fatalf("swHeight = %d, want 99", got)
	}
	if runtime.keyToneOn || runtime.BacklightEnabled() {
		t.Fatalf("handset defaults = key tone %v, backlight %v", runtime.keyToneOn, runtime.BacklightEnabled())
	}

	result, err := runtime.service(1, []uint32{0})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 1 || len(result.Push) != 0 ||
		!bytes.Equal(readStringMedia(t, runtime, 0), []byte("0100000000\x00")) {
		t.Fatalf("GetSysMin result = %+v, %q", result, readStringMedia(t, runtime, 0))
	}
	result, err = runtime.service(2, []uint32{1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Pop != 1 || len(result.Push) != 1 || result.Push[0] != 0 ||
		!bytes.Equal(readStringMedia(t, runtime, 1), []byte("CUSTOM\x00")) {
		t.Fatalf("GetSysUserID result = %+v, %q", result, readStringMedia(t, runtime, 1))
	}
}

func TestDeviceProfileValidation(t *testing.T) {
	valid := gnex32TestDeviceProfile()
	for name, mutate := range map[string]func(*DeviceProfile){
		"empty ID":       func(profile *DeviceProfile) { profile.ID = "" },
		"zero width":     func(profile *DeviceProfile) { profile.ScreenWidth = 0 },
		"large height":   func(profile *DeviceProfile) { profile.ScreenHeight = 4097 },
		"empty MIN":      func(profile *DeviceProfile) { profile.Identity.MIN = "" },
		"NUL user ID":    func(profile *DeviceProfile) { profile.Identity.UserID = "bad\x00id" },
		"empty platform": func(profile *DeviceProfile) { profile.Platform = "" },
	} {
		t.Run(name, func(t *testing.T) {
			profile := valid
			mutate(&profile)
			if err := profile.Validate(); err == nil {
				t.Fatal("invalid profile passed validation")
			}
		})
	}
}
