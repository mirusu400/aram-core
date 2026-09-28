package application

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

func TestJ2MEDarkWindVariantsUseArtworkCanvas(t *testing.T) {
	for _, test := range []struct {
		name, digest, menuHash, gameHash string
		canvas                           image.Point
	}{
		{
			name:     "small",
			digest:   "553faf5abdea80d63a069e30944b09227a1bad0677fb52ad8b9c4d6f06632580",
			canvas:   image.Pt(120, 160),
			menuHash: "420f5ac3a0952076b1b1d0bc51f5a44bea233e5859dc9000646eed0bba91369e",
			gameHash: "41f566f1a18d58ed89cb26a02ee7219e69cb50a79f6bc0f706572732d5b896a4",
		},
		{
			name:     "large",
			digest:   "c1716326874965bf0eec3f9226b0827235b1952559182c46ac7054fd5cd35f82",
			canvas:   image.Pt(176, 220),
			menuHash: "169ca1666241cba09c612dfc8eef2dabf3a79d55ff223fb66c79375f507b6b84",
			gameHash: "aa41c067ec2482f4bb37c1f2750ed3477e824bfecb08525f464295481c6a0f1d",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, data := findAuthorizedPackage(t, test.digest)
			ctx := context.Background()
			machine, err := NewFactory().Create(ctx, machinecore.Source{
				Name: filepath.Base(path), Path: path,
				ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			if err != nil {
				t.Fatal(err)
			}
			defer machine.Close()
			if got := machine.Framebuffer().Bounds().Size(); got != test.canvas {
				t.Fatalf("canvas = %v, want %v", got, test.canvas)
			}
			if err := machine.Start(ctx); err != nil {
				t.Fatal(err)
			}
			for frame := 0; frame < 1800; frame++ {
				if frame == 500 || frame == 1000 || frame == 1500 {
					for _, event := range []machinecore.InputEvent{
						{Control: "select", Pressed: true},
						{Control: "select", Pressed: false},
					} {
						if err := machine.QueueInput(event); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err := machine.StepFrame(ctx); err != nil {
					t.Fatalf("frame %d: %v", frame, err)
				}
				if frame == 1199 {
					if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != test.menuHash {
						t.Fatalf("menu hash = %s, want %s", got, test.menuHash)
					}
				}
			}
			if got := fmt.Sprintf("%x", brewFrameHash(machine.Framebuffer())); got != test.gameHash {
				t.Fatalf("game hash = %s, want %s", got, test.gameHash)
			}
		})
	}
}
