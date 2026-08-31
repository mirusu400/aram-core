package skvmhost

import (
	"image"

	"github.com/mirusu400/aram-core/application/internal/quirkdb"
	machinecore "github.com/mirusu400/aram-core/core"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
)

func lookupSKVMTitleCanvas(
	source machinecore.Source,
	pkg skloader.Package,
	inferred image.Point,
) (quirkdb.SKVMCanvas, bool) {
	return quirkdb.LookupSKVMCanvas(
		source.SHA256,
		pkg.Descriptor.MainClass,
		pkg.Descriptor.ProgramName,
		inferred.X,
		inferred.Y,
	)
}

// skvmTitleCanvas answers with the handset canvas an exact shipped package was
// authored for, or the inferred geometry when the package is not recorded.
func skvmTitleCanvas(
	source machinecore.Source,
	pkg skloader.Package,
	inferred image.Point,
) image.Point {
	entry, ok := lookupSKVMTitleCanvas(source, pkg, inferred)
	if !ok || entry.Width <= 0 || entry.Height <= 0 {
		return inferred
	}
	return image.Pt(entry.Width, entry.Height)
}

// applySKVMTitleCompatibility adds only compatibility behavior that is tied to
// an exact shipped package and its expected metadata. It takes the geometry
// inferSKVMFramebufferSize chose rather than the canvas skvmTitleCanvas
// answered with, so an entry that replaces the canvas still matches itself.
func applySKVMTitleCompatibility(
	config *shared.Config,
	source machinecore.Source,
	pkg skloader.Package,
	inferred image.Point,
) {
	if config == nil {
		return
	}
	entry, ok := lookupSKVMTitleCanvas(source, pkg, inferred)
	if !ok || !entry.CanvasHeightInset16 {
		return
	}
	config.Device.Quirks = append(config.Device.Quirks, shared.DeviceQuirk{
		Name:    skengine.CanvasHeightInset16Quirk,
		Enabled: true,
	})
}
