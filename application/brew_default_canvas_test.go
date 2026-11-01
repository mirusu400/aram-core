package application

import (
	"bytes"
	"context"
	"image"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// KTF BREW packages do not record their handset, and the dump names its small
// builds "(작화)". Unmarked packages are the 176x220-class builds: a
// whole-dump audit found 201 of 209 drawing 176-pixel screens, and a
// 120x160 canvas cut their menus and HUDs off (issues #565, #567).
func TestBREWDefaultCanvasFollowsHandsetClassInName(t *testing.T) {
	data := genericBREWArchiveForTest(t)
	for _, test := range []struct {
		name string
		want image.Rectangle
	}{
		{"[KTF BREW] 신시티.zip", image.Rect(0, 0, 176, 220)},
		{"[KTF BREW] 드래곤판타지(작화).zip", image.Rect(0, 0, 120, 160)},
		{"generic.zip", image.Rect(0, 0, 176, 220)},
	} {
		factory := NewFactory()
		factory.AllowUntrustedBREW = true
		created, matched, err := factory.createBREWMachine(context.Background(), machinecore.Source{
			Name: test.name, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
		})
		if err != nil || !matched {
			t.Fatalf("%s: matched=%v err=%v", test.name, matched, err)
		}
		if got := created.Framebuffer().Bounds(); got != test.want {
			t.Fatalf("%s canvas = %v, want %v", test.name, got, test.want)
		}
		created.Close()
	}
}
