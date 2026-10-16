package application

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/minigame"
	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
)

func TestMagicholeExtractedEADSUsesSameRuntime(t *testing.T) {
	reference := os.Getenv("ARAM_REFERENCE_REPO")
	if reference == "" {
		t.Skip("ARAM_REFERENCE_REPO is not set")
	}
	data, err := os.ReadFile(filepath.Join(reference, "SCH-W380_DL21",
		"SCH-W830_DL21_29360128_DL21.dat"))
	check(t, err)
	container, err := loader.InspectContainer(data)
	check(t, err)
	if len(container.Images) != 1 {
		t.Fatalf("reference image count = %d, want 1", len(container.Images))
	}
	image := container.Images[0]
	standalone := data[image.RecordOffset:image.RecordEnd()]
	var original *Machine
	for _, test := range []struct {
		name string
		data []byte
	}{{"container.dat", data}, {"extracted.dat", standalone}} {
		created, err := NewFactory().Create(context.Background(), machinecore.Source{
			Name: test.name, ReaderAt: bytes.NewReader(test.data), Size: int64(len(test.data)),
		})
		check(t, err)
		t.Cleanup(func() { _ = created.Close() })
		machine := created.(*Machine)
		if machine.ImageInfo().ProfileID != minigame.ProfileID {
			t.Fatalf("%s profile = %q, want %q", test.name, machine.ImageInfo().ProfileID, minigame.ProfileID)
		}
		check(t, created.Start(context.Background()))
		stats, ok := machine.EADSFrameStats()
		if !ok || stats.PresentCount != 2 {
			t.Fatalf("%s EADS stats = %+v, available=%t", test.name, stats, ok)
		}
		if original == nil {
			original = machine
			continue
		}
		originalStats, _ := original.EADSFrameStats()
		if !slices.Equal(stats.Events, originalStats.Events) || stats.TickMS != originalStats.TickMS {
			t.Fatalf("extracted lifecycle differs from the original: %+v / %+v", stats, originalStats)
		}
		if machine.ImageInfo().ImageSHA256 != original.ImageInfo().ImageSHA256 {
			t.Fatal("extracting an unchanged EADS record changed the loaded image identity")
		}
		if machine.source.SHA256 == original.source.SHA256 {
			t.Fatal("source integrity identity was replaced with the image identity")
		}
		for y := 0; y < machine.frame.Bounds().Dy(); y++ {
			for x := 0; x < machine.frame.Bounds().Dx(); x++ {
				if machine.frame.RGBAAt(x, y) != original.frame.RGBAAt(x, y) {
					t.Fatalf("extracted frame differs at (%d,%d)", x, y)
				}
			}
		}
	}
}
