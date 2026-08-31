package skvmhost

import (
	"image"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/quirkdb"
	machinecore "github.com/mirusu400/aram-core/core"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
)

const (
	dragonKnightEXSKTSHA256 = "fa1fc7826e4f2dbd10a4793177d9aed3282e5b9812d47863edc2f64761850cc2"
	whaleHunting2SKTSHA256  = "1367261bc3ee3b7f0afa102a52a7559204d94da60c543969773d47a09c051e79"
)

func TestDragonKnightEXCompatibilityRequiresExactPackageIdentity(t *testing.T) {
	source := machinecore.Source{SHA256: dragonKnightEXSKTSHA256}
	pkg := skloader.Package{Descriptor: skloader.Descriptor{
		MainClass:   "PNJDKEx",
		ProgramName: "0053597505",
	}}
	size := image.Pt(120, 160)

	config := shared.DefaultConfig()
	applySKVMTitleCompatibility(&config, source, pkg, size)
	if len(config.Device.Quirks) != 1 ||
		config.Device.Quirks[0] != (shared.DeviceQuirk{
			Name:    skengine.CanvasHeightInset16Quirk,
			Enabled: true,
		}) {
		t.Fatalf("compatibility quirks = %+v", config.Device.Quirks)
	}
	if canvas := skvmTitleCanvas(source, pkg, size); canvas != size {
		t.Fatalf("canvas = %v, want the inferred %v", canvas, size)
	}

	for name, mutate := range map[string]func(
		*machinecore.Source,
		*skloader.Package,
		*image.Point,
	){
		"digest": func(source *machinecore.Source, _ *skloader.Package, _ *image.Point) {
			source.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		},
		"main class": func(_ *machinecore.Source, pkg *skloader.Package, _ *image.Point) {
			pkg.Descriptor.MainClass = "Different"
		},
		"program": func(_ *machinecore.Source, pkg *skloader.Package, _ *image.Point) {
			pkg.Descriptor.ProgramName = "different"
		},
		"framebuffer": func(_ *machinecore.Source, _ *skloader.Package, size *image.Point) {
			*size = image.Pt(128, 160)
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedSource, changedPackage, changedSize := source, pkg, size
			mutate(&changedSource, &changedPackage, &changedSize)
			changedConfig := shared.DefaultConfig()
			applySKVMTitleCompatibility(
				&changedConfig,
				changedSource,
				changedPackage,
				changedSize,
			)
			if len(changedConfig.Device.Quirks) != 0 {
				t.Fatalf("lookalike quirks = %+v", changedConfig.Device.Quirks)
			}
		})
	}
}

// TestWhaleHunting2CanvasReplacesTheInferredGeometry covers aram-core #116:
// the title lays its picture out against a 176x220 handset, so the 240x320
// fallback the package assets cannot improve on leaves a 64-pixel band of the
// title screen unpainted.
func TestWhaleHunting2CanvasReplacesTheInferredGeometry(t *testing.T) {
	source := machinecore.Source{SHA256: whaleHunting2SKTSHA256}
	pkg := skloader.Package{Descriptor: skloader.Descriptor{
		MainClass:   "w",
		ProgramName: "3523930101",
	}}
	inferred := image.Pt(240, 320)

	canvas := skvmTitleCanvas(source, pkg, inferred)
	if want := image.Pt(176, 220); canvas != want {
		t.Fatalf("canvas = %v, want %v", canvas, want)
	}
	config := shared.DefaultConfig()
	applySKVMTitleCompatibility(&config, source, pkg, inferred)
	if len(config.Device.Quirks) != 1 ||
		config.Device.Quirks[0] != (shared.DeviceQuirk{
			Name:    skengine.CanvasHeightInset16Quirk,
			Enabled: true,
		}) {
		t.Fatalf("compatibility quirks = %+v", config.Device.Quirks)
	}

	// The entry carries no inferred geometry of its own, so a host that later
	// learns the size from the package still reaches the same canvas.
	if canvas := skvmTitleCanvas(source, pkg, image.Pt(176, 208)); canvas !=
		image.Pt(176, 220) {
		t.Fatalf("canvas from another inference = %v", canvas)
	}

	lookalike := pkg
	lookalike.Descriptor.MainClass = "x"
	if canvas := skvmTitleCanvas(source, lookalike, inferred); canvas != inferred {
		t.Fatalf("lookalike canvas = %v, want the inferred %v", canvas, inferred)
	}
}

func TestSKVMCanvasesStayKeyedToExactPackages(t *testing.T) {
	for _, entry := range quirkdb.SKVMCanvases {
		if len(entry.Key.PackageSHA256) != 64 ||
			entry.Key.MainClass == "" ||
			entry.Key.ProgramName == "" {
			t.Fatalf("under-specified quirkdb entry %+v", entry.Key)
		}
		if (entry.Width == 0) != (entry.Height == 0) {
			t.Fatalf("half a canvas in quirkdb entry %+v", entry)
		}
	}
}
