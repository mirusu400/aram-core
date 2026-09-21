package application

import (
	"image"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	raptorloader "github.com/mirusu400/aram-core/loader/raptor"
)

const hybrid2RaptorSHA256 = "320a5360a0f314d096a9ad3f219114b47d2e4e5a36a30f07c36841f797fbf20a"
const nom3RaptorSHA256 = "b475b63996844c2b4108224ec6ddb15f31ba8ac336dffcbebc4985d29009e930"
const battleMonsterModRaptorSHA256 = "b15f57f7aa597159a8495c04de2b987c2bdf72d9e5ae067c714360e2fec973f5"
const maplePirateRaptorSHA256 = "7f2c396bced5abba51e93cd1509eb06102ba9fec33d8ec96f79258c2ee3b039c"

func TestHybrid2RaptorPrimaryFramebufferHeightRequiresExactPackage(t *testing.T) {
	source := machinecore.Source{SHA256: hybrid2RaptorSHA256}
	pkg := raptorloader.Package{Descriptor: raptorloader.Descriptor{
		AID:       "0002E18D",
		MainClass: "Clet",
	}}
	size := image.Pt(240, 320)

	if options := raptorRuntimeOptions(source, pkg, size); options.PrimaryFramebufferHeight != 320 {
		t.Fatalf("primary framebuffer height = %d, want 320", options.PrimaryFramebufferHeight)
	}

	for name, mutate := range map[string]func(
		*machinecore.Source,
		*raptorloader.Package,
		*image.Point,
	){
		"digest": func(source *machinecore.Source, _ *raptorloader.Package, _ *image.Point) {
			source.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		},
		"aid": func(_ *machinecore.Source, pkg *raptorloader.Package, _ *image.Point) {
			pkg.Descriptor.AID = "00000000"
		},
		"main class": func(_ *machinecore.Source, pkg *raptorloader.Package, _ *image.Point) {
			pkg.Descriptor.MainClass = "OtherClet"
		},
		"framebuffer": func(_ *machinecore.Source, _ *raptorloader.Package, size *image.Point) {
			*size = image.Pt(240, 296)
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedSource, changedPackage, changedSize := source, pkg, size
			mutate(&changedSource, &changedPackage, &changedSize)
			if options := raptorRuntimeOptions(changedSource, changedPackage, changedSize); options.PrimaryFramebufferHeight != 0 {
				t.Fatalf("lookalike primary framebuffer height = %d, want default", options.PrimaryFramebufferHeight)
			}
		})
	}
}

func TestNom3RaptorResourceBytesHelperRequiresExactPackage(t *testing.T) {
	source := machinecore.Source{SHA256: nom3RaptorSHA256}
	pkg := raptorloader.Package{Descriptor: raptorloader.Descriptor{
		AID:       "00015E3D",
		MainClass: "Clet",
	}}
	if got := raptorRuntimeOptions(source, pkg, image.Pt(240, 320)).ResourceBytesHelper; got != 0x00032338 {
		t.Fatalf("resource helper = 0x%08x, want 0x00032338", got)
	}
	for name, mutate := range map[string]func(*machinecore.Source, *raptorloader.Package){
		"digest": func(source *machinecore.Source, _ *raptorloader.Package) {
			source.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		},
		"aid": func(_ *machinecore.Source, pkg *raptorloader.Package) {
			pkg.Descriptor.AID = "00000000"
		},
		"main class": func(_ *machinecore.Source, pkg *raptorloader.Package) {
			pkg.Descriptor.MainClass = "OtherClet"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedSource, changedPackage := source, pkg
			mutate(&changedSource, &changedPackage)
			if got := raptorRuntimeOptions(changedSource, changedPackage, image.Pt(240, 320)).ResourceBytesHelper; got != 0 {
				t.Fatalf("lookalike resource helper = 0x%08x, want default", got)
			}
		})
	}
}

func TestBattleMonsterModRaptorImagePatchRequiresExactPackage(t *testing.T) {
	source := machinecore.Source{SHA256: battleMonsterModRaptorSHA256}
	pkg := raptorloader.Package{Descriptor: raptorloader.Descriptor{
		AID:       "00025C2B",
		MainClass: "Jp",
	}}
	wantAddress := uint32(0x000cfc98)
	wantExpected := uint32(0xe92dd810)
	wantReplacement := uint32(0x000d8640)

	patches := raptorRuntimeOptions(source, pkg, image.Pt(240, 320)).ImagePatches
	if len(patches) != 1 || patches[0].Address != wantAddress ||
		patches[0].Expected != wantExpected || patches[0].Replacement != wantReplacement {
		t.Fatalf("image patches = %+v, want one repair at 0x%08x", patches, wantAddress)
	}

	for name, mutate := range map[string]func(*machinecore.Source, *raptorloader.Package){
		"digest": func(source *machinecore.Source, _ *raptorloader.Package) {
			source.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		},
		"aid": func(_ *machinecore.Source, pkg *raptorloader.Package) {
			pkg.Descriptor.AID = "00000000"
		},
		"main class": func(_ *machinecore.Source, pkg *raptorloader.Package) {
			pkg.Descriptor.MainClass = "OtherClet"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedSource, changedPackage := source, pkg
			mutate(&changedSource, &changedPackage)
			if patches := raptorRuntimeOptions(changedSource, changedPackage, image.Pt(240, 320)).ImagePatches; len(patches) != 0 {
				t.Fatalf("lookalike image patches = %+v, want none", patches)
			}
		})
	}
}

func TestMaplePirateRaptorAudioCompatibilityRequiresExactPackage(t *testing.T) {
	source := machinecore.Source{SHA256: maplePirateRaptorSHA256}
	pkg := raptorloader.Package{Descriptor: raptorloader.Descriptor{
		AID:       "0002A4F0",
		MainClass: "Clet",
	}}
	if options := raptorRuntimeOptions(source, pkg, image.Pt(240, 320)); !options.PreserveStoppedLoops {
		t.Fatal("stopped-loop preservation was not selected")
	}

	for name, mutate := range map[string]func(*machinecore.Source, *raptorloader.Package){
		"digest": func(source *machinecore.Source, _ *raptorloader.Package) {
			source.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
		},
		"aid": func(_ *machinecore.Source, pkg *raptorloader.Package) {
			pkg.Descriptor.AID = "00000000"
		},
		"main class": func(_ *machinecore.Source, pkg *raptorloader.Package) {
			pkg.Descriptor.MainClass = "OtherClet"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedSource, changedPackage := source, pkg
			mutate(&changedSource, &changedPackage)
			if options := raptorRuntimeOptions(
				changedSource,
				changedPackage,
				image.Pt(240, 320),
			); options.PreserveStoppedLoops {
				t.Fatal("lookalike package selected stopped-loop preservation")
			}
		})
	}
}
