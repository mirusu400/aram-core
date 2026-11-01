package skvmhost

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/guest"
	"github.com/mirusu400/aram-core/profile"
)

// TestSKVMFrontendControlKeyCode walks the whole seam an input event takes:
// the frontend control name, the shared WIPI key code, and the SKT keypad code
// the MIDlet compares against. 대해적시대 backs out of a screen on 8 and
// 강호동맞고2 accepts 129 there too, so a control that stops at the WIPI
// spelling is silently dead for those titles.
func TestSKVMFrontendControlKeyCode(t *testing.T) {
	tests := []struct {
		control string
		want    int32
	}{
		{control: "up", want: 141},
		{control: "down", want: 146},
		{control: "left", want: 142},
		{control: "right", want: 145},
		{control: "ok", want: 148},
		{control: "back", want: 8},
		{control: "clear", want: 8},
		{control: "soft-left", want: 131},
		{control: "soft-right", want: 129},
		{control: "num7", want: '7'},
		{control: "star", want: '*'},
		{control: "hash", want: '#'},
		{control: "send", want: 190},
	}
	for _, test := range tests {
		t.Run(test.control, func(t *testing.T) {
			key, ok := guest.InputKeyCode(test.control)
			if !ok {
				t.Fatalf("control %q has no WIPI key code", test.control)
			}
			got, delivered := skvmKeyCode(key)
			if !delivered || got != test.want {
				t.Fatalf(
					"control %q = %d (delivered %t), want %d",
					test.control,
					got,
					delivered,
					test.want,
				)
			}
		})
	}
}

// TestSKVMFrontendControlWithoutSKTCode keeps the controls an SKT handset has
// no MIDlet key code for away from the title. 코인마스터 indexes a 200-entry
// key table with the code it receives, so a negative WIPI spelling such as
// END's -11 or MENU's -8 threw ArrayIndexOutOfBoundsException straight out of
// keyPressed.
func TestSKVMFrontendControlWithoutSKTCode(t *testing.T) {
	for _, control := range []string{"end", "menu"} {
		t.Run(control, func(t *testing.T) {
			key, ok := guest.InputKeyCode(control)
			if !ok {
				t.Fatalf("control %q has no WIPI key code", control)
			}
			if code, delivered := skvmKeyCode(key); delivered {
				t.Fatalf("control %q delivered SKT key code %d", control, code)
			}
		})
	}
	for _, key := range []profile.KeyCode{
		profile.KeyPower, profile.KeySideUp, profile.KeySideDown,
		profile.KeySideSel, profile.KeyFlipDown, profile.KeyFlipUp,
	} {
		if code, delivered := skvmKeyCode(key); delivered {
			t.Fatalf("WIPI key %d delivered SKT key code %d", key, code)
		}
	}
}

func TestSKVMKeyCode(t *testing.T) {
	tests := []struct {
		name string
		key  profile.KeyCode
		want int32
	}{
		{name: "up", key: profile.KeyUp, want: 141},
		{name: "left", key: profile.KeyLeft, want: 142},
		{name: "right", key: profile.KeyRight, want: 145},
		{name: "down", key: profile.KeyDown, want: 146},
		{name: "select", key: profile.KeySelect, want: 148},
		{name: "digit", key: profile.Key1, want: '1'},
		{name: "star", key: profile.KeyAsterisk, want: '*'},
		{name: "hash", key: profile.KeyPound, want: '#'},
		{name: "left soft key", key: profile.KeySoft1, want: 131},
		{name: "right soft key", key: profile.KeySoft2, want: 129},
		{name: "clear", key: profile.KeyClear, want: 8},
		{name: "send", key: profile.KeySend, want: 190},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, delivered := skvmKeyCode(test.key)
			if !delivered || got != test.want {
				t.Fatalf("skvmKeyCode(%d) = %d (delivered %t), want %d", test.key, got, delivered, test.want)
			}
		})
	}
}

func TestLGTFrontendControlKeyCode(t *testing.T) {
	tests := []struct {
		control string
		want    int32
	}{
		{"up", -1}, {"down", -2}, {"left", -3}, {"right", -4},
		{"select", -5}, {"soft-left", -6}, {"soft-right", -7},
		{"clear", -8}, {"num7", '7'}, {"star", '*'}, {"hash", '#'},
	}
	for _, test := range tests {
		t.Run(test.control, func(t *testing.T) {
			key, ok := guest.InputKeyCode(test.control)
			if !ok {
				t.Fatalf("control %q has no key code", test.control)
			}
			if got := lgtKeyCode(key); got != test.want {
				t.Fatalf("control %q = %d, want %d", test.control, got, test.want)
			}
		})
	}
}

func TestInferSKVMFramebufferSize(t *testing.T) {
	fallback := image.Pt(240, 320)
	tests := []struct {
		name      string
		resources map[string][]byte
		want      image.Point
	}{
		{
			name:      "120 pixel handset background",
			resources: map[string][]byte{"background.png": syntheticSKVMPNG(t, 120, 146)},
			want:      image.Pt(120, 160),
		},
		{
			name: "larger background wins",
			resources: map[string][]byte{
				"small.png":  syntheticSKVMPNG(t, 120, 160),
				"large1.png": syntheticSKVMPNG(t, 176, 202),
				"large2.png": syntheticSKVMPNG(t, 176, 202),
			},
			want: image.Pt(176, 208),
		},
		{
			name:      "no screen-sized image",
			resources: map[string][]byte{"icon.png": syntheticSKVMPNG(t, 23, 23)},
			want:      fallback,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := inferSKVMFramebufferSize(fallback, test.resources); got != test.want {
				t.Fatalf("inferSKVMFramebufferSize() = %v, want %v", got, test.want)
			}
		})
	}
}

func syntheticSKVMPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := png.Encode(
		&output,
		image.NewRGBA(image.Rect(0, 0, width, height)),
	); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
