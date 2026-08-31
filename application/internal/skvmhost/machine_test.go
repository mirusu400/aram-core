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
	}
	for _, test := range tests {
		t.Run(test.control, func(t *testing.T) {
			key, ok := guest.InputKeyCode(test.control)
			if !ok {
				t.Fatalf("control %q has no WIPI key code", test.control)
			}
			if got := skvmKeyCode(key); got != test.want {
				t.Fatalf(
					"control %q = %d, want %d",
					test.control,
					got,
					test.want,
				)
			}
		})
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
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := skvmKeyCode(test.key); got != test.want {
				t.Fatalf("skvmKeyCode(%d) = %d, want %d", test.key, got, test.want)
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
