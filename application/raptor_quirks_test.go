package application

import (
	"image"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	raptorloader "github.com/mirusu400/aram-core/loader/raptor"
)

const hybrid2RaptorSHA256 = "320a5360a0f314d096a9ad3f219114b47d2e4e5a36a30f07c36841f797fbf20a"

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
