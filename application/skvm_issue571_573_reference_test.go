package application

import (
	"bytes"
	"context"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/skvmhost"
	machinecore "github.com/mirusu400/aram-core/core"
	skengine "github.com/mirusu400/aram-core/skvm"
)

// openSKVMReferenceTitle creates the SKVM machine for one exact corpus package
// below ARAM_TEST_DATA, or skips when the private corpus is not present.
func openSKVMReferenceTitle(t *testing.T, digest string, elements ...string) *skvmhost.Machine {
	t.Helper()
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	name := filepath.Join(append([]string{root, "corpus", "f-app", "SKT"}, elements...)...)
	data, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		t.Skipf("reference title is not present: %s", filepath.Join(elements...))
	}
	check(t, err)
	created, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     filepath.Base(name),
		SHA256:   digest,
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	check(t, err)
	machine, ok := created.(*skvmhost.Machine)
	if !ok {
		t.Fatalf("machine is %T, want *skvmhost.Machine", created)
	}
	t.Cleanup(func() { _ = machine.Close() })
	if !machine.Services().Device.Quirk(skengine.CanvasHeightInset16Quirk) {
		t.Fatal("Canvas.getHeight() reports the full display to a title that adds the 16-row system strip back")
	}
	return machine
}

func stepSKVMReferenceTitle(t *testing.T, machine *skvmhost.Machine, frames int, taps map[int]bool) image.Image {
	t.Helper()
	check(t, machine.Start(context.Background()))
	for frame := range frames {
		if taps[frame] {
			check(t, machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: true}))
		}
		if taps[frame-2] {
			check(t, machine.QueueInput(machinecore.InputEvent{Control: "select", Pressed: false}))
		}
		check(t, machine.StepFrame(context.Background()))
	}
	return machine.Framebuffer()
}

func solidSKVMColumn(frame image.Image, x int) bool {
	bounds := frame.Bounds()
	first := frame.At(x, bounds.Min.Y)
	for y := bounds.Min.Y + 1; y < bounds.Max.Y; y++ {
		if frame.At(x, y) != first {
			return false
		}
	}
	return true
}

// TestReferenceSKVMIssue571KnightMakerCanvas covers aram-core #571. 나이트메이커
// keeps its art in LBMP blobs, so no package asset names a handset and it
// fell back to 240x320, where it frames its 176-column picture in the middle
// of the display (its own `getWidth() > 176` branch) between empty bands. Its
// opening keeps every screen inside that centred column, which the canvas
// probe reads as a 176x220 handset. Its `getHeight() + 16` layout then needs
// the 16-row inset for the copyright line at the bottom of the title screen.
// The two corpus archives hold byte-identical application members.
func TestReferenceSKVMIssue571KnightMakerCanvas(t *testing.T) {
	for _, title := range []struct {
		digest   string
		elements []string
	}{
		{"cf5249e75df3140f8e6ce14a7ef068a9d57b71c88eb8b70cb7a59ab8302281ea", []string{"타이쿤_시뮬레이션", "나이트메이커.zip"}},
		{"965b45de09ac8d062cfbe6cfb174a9b1f5dd0b81d39a76312ca59ce35627d749", []string{"RPG_전략", "나이트메이커.zip"}},
	} {
		t.Run(title.digest[:12], func(t *testing.T) {
			machine := openSKVMReferenceTitle(t, title.digest, title.elements...)
			frame := stepSKVMReferenceTitle(t, machine, 150, nil)
			if got := frame.Bounds().Size(); got != image.Pt(176, 220) {
				t.Fatalf("canvas = %v, want (176,220)", got)
			}
			// The title picture spans the whole display rather than a column.
			if solidSKVMColumn(frame, 0) || solidSKVMColumn(frame, 175) {
				t.Fatal("title screen leaves an empty band at a display edge")
			}
			// The copyright line is drawn in white in the last rows.
			white := 0
			for y := 206; y < 220; y++ {
				for x := range 176 {
					r, g, b, _ := frame.At(x, y).RGBA()
					if r>>8 >= 0xf0 && g>>8 >= 0xf0 && b>>8 >= 0xf0 {
						white++
					}
				}
			}
			if white < 40 {
				t.Fatalf("copyright line below the title picture is missing (%d white pixels)", white)
			}
		})
	}
}

// TestReferenceSKVMIssue573NoriTycoonBottomHUD covers aram-core #573. 노리타이쿤
// computes its layout height as `getHeight() + 16`, so with Canvas.getHeight()
// reporting the whole 320-row display its bottom status bar was laid out for
// 336 rows and its lower half fell off the screen. With the handset's 304-row
// client height the bar closes on the last display row.
func TestReferenceSKVMIssue573NoriTycoonBottomHUD(t *testing.T) {
	machine := openSKVMReferenceTitle(
		t,
		"a0436eb1ddbb0501ac3fe43a55c073b75b8344ffa2c1f297beeb2d204de44cae",
		"타이쿤_시뮬레이션", "skt 노리타이쿤.zip",
	)
	frame := stepSKVMReferenceTitle(t, machine, 600, map[int]bool{200: true, 320: true, 420: true, 520: true})
	if got := frame.Bounds().Size(); got != image.Pt(240, 320) {
		t.Fatalf("canvas = %v, want (240,320)", got)
	}
	// The status bar's top and bottom border rows are each one colour across
	// nearly the whole width; map tiles in those rows would not be.
	for _, y := range []int{309, 319} {
		counts := make(map[[3]uint32]int)
		largest := 0
		for x := range 240 {
			r, g, b, _ := frame.At(x, y).RGBA()
			key := [3]uint32{r, g, b}
			counts[key]++
			largest = max(largest, counts[key])
		}
		if largest < 200 {
			t.Fatalf("row %d is not the status bar border (largest colour run %d/240)", y, largest)
		}
	}
}
