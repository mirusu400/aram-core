package skvmhost

import (
	"bytes"
	"image"
	"image/png"
	"testing"

	"github.com/mirusu400/aram-core/profile"
)

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
		{name: "soft key", key: profile.KeySoft1, want: -6},
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
