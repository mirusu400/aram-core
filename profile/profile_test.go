package profile

import (
	"strings"
	"testing"
)

func TestStackResolveAppliesLayersInSpecificityOrder(t *testing.T) {
	standardProperties := map[string]string{
		"TIMEZONE": "GMT+00:00",
		"shared":   "standard",
	}
	stack := Stack{
		Standard: StandardLayer{
			Layer: Layer{
				ID:         "wipi-1.2.1",
				Properties: standardProperties,
				Quirks:     map[string]bool{"legacy-timer": true},
				Keys:       KeyMap{VirtualUp: KeyUp},
			},
			Version: Version121,
		},
		Carrier: &CarrierLayer{
			Layer: Layer{
				ID:         "ktf",
				Properties: map[string]string{"shared": "carrier"},
			},
			Carrier: CarrierKTF,
		},
		Manufacturer: &ManufacturerLayer{
			Layer: Layer{
				ID:     "samsung",
				Quirks: map[string]bool{"samsung-oem": true},
			},
			Manufacturer: "Samsung",
		},
		Device: &DeviceLayer{
			Layer: Layer{
				ID:         "sch-w830",
				Properties: map[string]string{"PHONEMODEL": "SCH-W830"},
				Quirks:     map[string]bool{"legacy-timer": false},
				Keys:       KeyMap{VirtualUp: Key2},
			},
			Model: "SCH-W830",
			Screen: Screen{
				Width:        240,
				Height:       320,
				Orientation:  OrientationPortrait,
				BitsPerPixel: 16,
				Depth:        16,
				BytesPerLine: 480,
				ColorType:    ColorDirect,
				RedMask:      0xf800,
				GreenMask:    0x07e0,
				BlueMask:     0x001f,
			},
		},
		Title: &TitleLayer{
			Layer: Layer{
				ID:     "synthetic-title",
				Quirks: map[string]bool{"title-fix": true},
			},
			SHA256: strings.Repeat("ab", 32),
		},
	}

	resolved, err := stack.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ID != "synthetic-title" ||
		resolved.Version != Version121 ||
		resolved.Carrier != CarrierKTF ||
		resolved.Manufacturer != "Samsung" ||
		resolved.Model != "SCH-W830" {
		t.Fatalf("resolved identity = %+v", resolved)
	}
	if resolved.Properties["shared"] != "carrier" ||
		resolved.Properties["PHONEMODEL"] != "SCH-W830" {
		t.Fatalf("resolved properties = %+v", resolved.Properties)
	}
	if resolved.Quirks["legacy-timer"] ||
		!resolved.Quirks["samsung-oem"] ||
		!resolved.Quirks["title-fix"] {
		t.Fatalf("resolved quirks = %+v", resolved.Quirks)
	}
	if resolved.Keys[VirtualUp] != Key2 {
		t.Fatalf("resolved key map = %+v", resolved.Keys)
	}
	wantLayers := []string{"wipi-1.2.1", "ktf", "samsung", "sch-w830", "synthetic-title"}
	if strings.Join(resolved.Layers, ",") != strings.Join(wantLayers, ",") {
		t.Fatalf("Layers = %v, want %v", resolved.Layers, wantLayers)
	}

	standardProperties["shared"] = "mutated"
	if resolved.Properties["shared"] != "carrier" {
		t.Fatal("resolved profile aliases an input property map")
	}
}

func TestStackResolveRejectsInvalidTitleHash(t *testing.T) {
	stack := validStack()
	stack.Title = &TitleLayer{
		Layer:  Layer{ID: "title"},
		SHA256: "not-a-hash",
	}
	if _, err := stack.Resolve(); err == nil {
		t.Fatal("Resolve accepted an invalid title hash")
	}
}

func TestStackResolveRejectsDuplicatePhysicalKey(t *testing.T) {
	stack := validStack()
	stack.Standard.Keys = KeyMap{
		VirtualUp:   Key2,
		VirtualFire: Key2,
	}
	if _, err := stack.Resolve(); err == nil {
		t.Fatal("Resolve accepted duplicate physical key mappings")
	}
}

func TestScreenValidation(t *testing.T) {
	screen := Screen{
		Width:        240,
		Height:       320,
		BitsPerPixel: 16,
		Depth:        16,
		BytesPerLine: 100,
		ColorType:    ColorDirect,
	}
	if err := screen.Validate(); err == nil {
		t.Fatal("Screen.Validate accepted a short scanline")
	}
	screen.BytesPerLine = 480
	screen.RedMask = 0xf800
	screen.GreenMask = 0x07e0
	screen.BlueMask = 0x001f
	if err := screen.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestWIPIKeyValues(t *testing.T) {
	if Key0 != '0' || KeyAsterisk != '*' || KeyPound != '#' ||
		KeyUp != -1 || KeyFlipUp != -18 {
		t.Fatal("physical key constants do not match WIPI HAL values")
	}
	if VirtualUp != 1 || VirtualFire != 8 ||
		VirtualSideClear != 99 {
		t.Fatal("virtual key constants do not match WIPI HAL values")
	}
}

func validStack() Stack {
	return Stack{
		Standard: StandardLayer{
			Layer:   Layer{ID: "wipi-1.2.1"},
			Version: Version121,
		},
		Device: &DeviceLayer{
			Layer:  Layer{ID: "device"},
			Model:  "Synthetic",
			Screen: Screen{Width: 240, Height: 320},
		},
	}
}
