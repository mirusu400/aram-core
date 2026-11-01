package application

import (
	"fmt"
	"image"
	"testing"
)

// 미니게임 명작동화 sizes its menu from the screen height. On the 176x207
// handset canvas each item is one 14-pixel label; on a taller canvas the
// module chose its QVGA layout and blitted two labels per item, so the
// highlight always covered a pair of entries (issue #558).
func TestBREWMinibookMenuHighlightsOneLabel(t *testing.T) {
	const digest = "9d17a101d5c9e31e789698f31fb41ba5448b624eb63b37193be13b12fdbb46c0"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 1000)
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 176, 207); got != want {
		t.Fatalf("Minibook canvas = %v, want %v", got, want)
	}
	for range 2 {
		tapBREWReference(t, machine)
		stepBREWReference(t, machine, 1000)
	}
	first := minibookSelectedLabelRows(machine.Framebuffer())
	if len(first) != 14 {
		t.Fatalf("selected menu label spans %d rows %v, want one 14-row label", len(first), first)
	}
	tapBREWControl(t, machine, "down")
	stepBREWReference(t, machine, 120)
	next := minibookSelectedLabelRows(machine.Framebuffer())
	if len(next) != 14 || next[0] <= first[len(first)-1] {
		t.Fatalf("selection after down spans rows %v, want one 14-row label below rows %v", next, first)
	}
}

// minibookSelectedLabelRows returns the rows where the highlighted label's
// dark-red outline appears inside the menu column.
func minibookSelectedLabelRows(frame image.Image) []int {
	var rows []int
	bounds := frame.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := 40; x < 140; x++ {
			r, g, b, _ := frame.At(x, y).RGBA()
			if r>>8 == 181 && g>>8 == 0 && b>>8 == 0 {
				rows = append(rows, y)
				break
			}
		}
	}
	return rows
}

// 미니게임2005 (small-screen build) uses the same height classes. Its scenes
// are 120x146; the default 120x160 canvas selects the large-handset layout.
func TestBREWMinigame2005UsesItsHandsetCanvas(t *testing.T) {
	const digest = "0622f4c830832e04d310c3496680df8307060fa74f4f63a9ee9c09ae16300e5c"
	path, data := findAuthorizedPackage(t, digest)
	machine := newBREWReferenceMachine(t, path, data)
	stepBREWReference(t, machine, 200)
	if got, want := machine.Framebuffer().Bounds(), image.Rect(0, 0, 120, 146); got != want {
		t.Fatalf("미니게임2005 canvas = %v, want %v", got, want)
	}
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 200)
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 200)
	tapBREWControl(t, machine, "down")
	stepBREWReference(t, machine, 200)
	tapBREWReference(t, machine)
	stepBREWReference(t, machine, 200)
	// The settings screen: no leftover title footer, CLR caption at the bottom.
	if got, want := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())), "6896a48f516cbe801bcf1a1587e3cd0b26e369d3499603a4cab1d4564d93cd6b"; got != want {
		t.Fatalf("미니게임2005 settings frame hash=%s, want %s", got, want)
	}
}
