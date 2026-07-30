package loader_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mirusu400/aram-core/loader"
	"github.com/mirusu400/aram-core/loader/abhs"
	"github.com/mirusu400/aram-core/loader/eads"
)

func TestMagicholeReferenceDAT(t *testing.T) {
	reference := os.Getenv("ARAM_REFERENCE_REPO")
	if reference == "" {
		t.Skip("ARAM_REFERENCE_REPO is not set")
	}
	path := filepath.Join(
		reference,
		"SCH-W380_DL21",
		"SCH-W830_DL21_29360128_DL21.dat",
	)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("reference DAT is unavailable: %v", err)
	}
	container, err := loader.InspectContainer(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(container.Modules) != 6 {
		t.Fatalf("container ABHS module count = %d, want 6", len(container.Modules))
	}
	if len(container.Images) != 1 {
		t.Fatalf("container EADS image count = %d, want 1", len(container.Images))
	}
	modules := abhs.Inspect(data)
	if len(modules) != 6 {
		t.Fatalf("ABHS module count = %d, want 6", len(modules))
	}
	for index, module := range modules {
		guestBase := uint32(0x10000000 + index*0x01000000)
		loaded, err := abhs.Load(data, module, guestBase)
		if err != nil {
			t.Fatalf("load ABHS module %d: %v", index, err)
		}
		if uint32(len(loaded.Image)) != module.Code.Size {
			t.Fatalf("loaded ABHS module %d size = %d, want %d",
				index, len(loaded.Image), module.Code.Size)
		}
	}
	images := eads.Inspect(data)
	if len(images) != 1 {
		t.Fatalf("EADS image count = %d, want 1", len(images))
	}
	if images[0].Name != "MinigameQVGAOEM" {
		t.Fatalf("EADS image name = %q", images[0].Name)
	}
	if _, err := eads.ExtractText(data, images[0]); err != nil {
		t.Fatalf("extract reference EADS text: %v", err)
	}
}
