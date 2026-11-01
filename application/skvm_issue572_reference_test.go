package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Issue #572: 더팜 (SKT) draws each main-menu label as a 2-bit LBMP on a
// filled button. The decoder rejected the depth, the VM answered a blank 1x1
// image, and every button came up empty. The highlighted button is located by
// its own fill colour, so the check does not depend on the canvas size.
func TestReferenceSKVMIssue572TheFarmMenuLabels(t *testing.T) {
	root := os.Getenv("ARAM_TEST_DATA")
	if root == "" {
		t.Skip("ARAM_TEST_DATA is not set")
	}
	const digest = "b42e4242866b6908bda35d93a703bdac7e1be6c69e42c58ded1ffbbc478e7b23"
	var data []byte
	for _, base := range []string{
		filepath.Join(root, "corpus", "f-app"),
		filepath.Join(root, "f-app"),
		root,
	} {
		read, err := os.ReadFile(filepath.Join(base, "SKT", "타이쿤_시뮬레이션", "skt 더팜1.zip"))
		if err == nil {
			data = read
			break
		}
	}
	if data == nil {
		t.Skip("issue #572 reference title is not present below ARAM_TEST_DATA")
	}
	sum := sha256.Sum256(data)
	if actual := hex.EncodeToString(sum[:]); actual != digest {
		t.Skipf("issue #572 reference archive SHA-256=%s, want %s", actual, digest)
	}
	machine, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "skt 더팜1.zip",
		SHA256:   digest,
		ReaderAt: bytes.NewReader(data),
		Size:     int64(len(data)),
	})
	check(t, err)
	t.Cleanup(func() { _ = machine.Close() })
	check(t, machine.Start(context.Background()))

	step := func(frames int) {
		t.Helper()
		for range frames {
			check(t, machine.StepFrame(context.Background()))
		}
	}
	press := func(control string) {
		t.Helper()
		check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: true}))
		step(2)
		check(t, machine.QueueInput(machinecore.InputEvent{Control: control, Pressed: false}))
	}
	// The title screen waits for OK; the menu then opens on "더팜으로".
	step(60)
	press("select")
	step(58)
	if ink := issue572HighlightedLabelInk(t, machine.Framebuffer()); ink < 20 {
		t.Fatalf("highlighted menu button carries %d label pixels, want its 2-bit label", ink)
	}
	press("down")
	step(30)
	if ink := issue572HighlightedLabelInk(t, machine.Framebuffer()); ink < 20 {
		t.Fatalf("next highlighted menu button carries %d label pixels, want its 2-bit label", ink)
	}
}

// issue572HighlightedLabelInk counts black pixels in the middle of the
// highlighted (0xffda00) menu button. The label is centred on the button; the
// pointer drawn over the button's right edge stays outside the counted band.
func issue572HighlightedLabelInk(t *testing.T, frame image.Image) int {
	t.Helper()
	highlight := color.NRGBA{R: 0xff, G: 0xda, B: 0x00, A: 0xff}
	black := color.NRGBA{A: 0xff}
	bounds := frame.Bounds()
	box := image.Rectangle{}
	found := false
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if color.NRGBAModel.Convert(frame.At(x, y)) != highlight {
				continue
			}
			cell := image.Rect(x, y, x+1, y+1)
			if !found {
				box, found = cell, true
			} else {
				box = box.Union(cell)
			}
		}
	}
	if !found || box.Dx() < 40 || box.Dy() < 8 {
		t.Fatalf("highlighted menu button not found (bounding box %v)", box)
	}
	centre := (box.Min.X + box.Max.X) / 2
	ink := 0
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := centre - 16; x < centre+16; x++ {
			if color.NRGBAModel.Convert(frame.At(x, y)) == black {
				ink++
			}
		}
	}
	return ink
}
