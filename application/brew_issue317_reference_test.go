package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	machinecore "github.com/mirusu400/aram-core/core"
)

// The reported logo is an extended opening sequence, not a permanent stall.
// The exact game reaches its illustrated title and then accepts the menu key.
func TestBREWIssue317ReachesTitleAndMenu(t *testing.T) {
	const digest = "515977ac9e3f76f8a534ae633195fa42affdd2dbea075d42fd2830c101084daa"
	path, data := findAuthorizedPackage(t, digest)
	factory := NewFactory()
	factory.AllowUntrustedBREW = true
	ctx := context.Background()
	machine, err := factory.Create(ctx, machinecore.Source{
		Name: filepath.Base(path), Path: path,
		ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer machine.Close()
	if err := machine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	step := func(count int) {
		t.Helper()
		for frame := 0; frame < count; frame++ {
			if err := machine.StepFrame(ctx); err != nil {
				t.Fatalf("frame %d/%d: %v", frame, count, err)
			}
		}
	}
	frameHash := func() [32]byte {
		t.Helper()
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, machine.Framebuffer()); err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(encoded.Bytes())
	}
	step(300)
	logo := frameHash()
	step(2700)
	title := frameHash()
	if title == logo {
		t.Fatal("opening logo never advanced to the title")
	}
	r, g, b, _ := machine.Framebuffer().At(60, 40).RGBA()
	if r > 0xeeee && g > 0xeeee && b > 0xeeee {
		t.Fatal("title artwork is absent where the logo had a white background")
	}
	for _, event := range []machinecore.InputEvent{
		{Control: "select", Pressed: true},
		{Control: "select", Pressed: false, At: time.Millisecond},
	} {
		if err := machine.QueueInput(event); err != nil {
			t.Fatal(err)
		}
	}
	step(300)
	if menu := frameHash(); menu == title || menu == logo {
		t.Fatal("select did not advance from the title to the menu")
	}
}
