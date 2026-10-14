package application

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
)

// Read the exact reported package in place; the BMP regressions use synthetic
// pixels and do not need this optional private corpus.
func TestIssue524WindsTaleTerrainTransparency(t *testing.T) {
	const digest = "75569fe4104ceb00e713bca92559f4fc04dea9b1622c1cbef44c8b9dd3b100f8"
	path, data := findAuthorizedPackage(t, digest)
	for _, backend := range []string{"native", "precise"} {
		t.Run(backend, func(t *testing.T) {
			factory := NewFactory()
			var err error
			factory.NewCPU, err = ResolveCPUBackend(backend)
			check(t, err)
			factory.RunBudget = DefaultHandsetRunBudget
			factory.FrameRunBudget = DefaultHandsetRunBudget
			factory.KTFRunBudget = DefaultKTFHandsetRunBudget
			created, err := factory.Create(context.Background(), machinecore.Source{
				Name: filepath.Base(path), Path: path, ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
			})
			check(t, err)
			machine := created.(*Machine)
			t.Cleanup(func() { _ = machine.Close() })
			check(t, machine.Start(context.Background()))
			for frame := 0; frame < 2100; frame++ {
				if frame >= 80 && (frame%80 == 0 || frame%80 == 2) {
					check(t, machine.QueueInput(machinecore.InputEvent{
						Control: "ok", Pressed: frame%80 == 0,
					}))
				}
				check(t, machine.StepFrame(context.Background()))
			}
			if machine.ktf.FirstJavaThrowName != "" {
				t.Fatalf("replay threw %s", machine.ktf.FirstJavaThrowName)
			}
			// Inspect the terrain above the HUD. A loading screen would also
			// lack magenta, so require the green foliage to have appeared.
			green, magenta := 0, 0
			for y := 0; y < 272; y++ {
				for x := 0; x < 240; x++ {
					pixel := machine.frame.RGBAAt(x, y)
					if pixel.G > pixel.R && pixel.G > pixel.B {
						green++
					}
					if pixel.R == 255 && pixel.G == 0 && pixel.B == 255 {
						magenta++
					}
				}
			}
			if green < 1000 {
				t.Fatalf("terrain did not appear: green pixels = %d", green)
			}
			if magenta != 0 {
				t.Fatalf("terrain contains %d opaque magenta key pixels", magenta)
			}
		})
	}
}
