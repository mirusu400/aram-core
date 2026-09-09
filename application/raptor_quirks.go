package application

import (
	"image"

	"github.com/mirusu400/aram-core/application/internal/quirkdb"
	raptorrt "github.com/mirusu400/aram-core/application/internal/raptor"
	machinecore "github.com/mirusu400/aram-core/core"
	raptorloader "github.com/mirusu400/aram-core/loader/raptor"
)

// raptorRuntimeOptions derives immutable runtime behavior from a fully
// verified source package. It deliberately does not inspect descriptor names.
func raptorRuntimeOptions(
	source machinecore.Source,
	pkg raptorloader.Package,
	framebufferSize image.Point,
) raptorrt.Options {
	geometry, ok := quirkdb.LookupRaptorFramebufferGeometry(
		source.SHA256,
		pkg.Descriptor.AID,
		pkg.Descriptor.MainClass,
		framebufferSize.X,
		framebufferSize.Y,
	)
	if !ok || geometry.PrimaryHeight <= 0 ||
		geometry.PrimaryHeight > framebufferSize.Y {
		return raptorrt.Options{}
	}
	return raptorrt.Options{
		PrimaryFramebufferHeight: geometry.PrimaryHeight,
	}
}
