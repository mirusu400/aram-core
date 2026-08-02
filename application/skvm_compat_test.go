package application

import (
	"image"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
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
