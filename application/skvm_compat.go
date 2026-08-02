package application

import (
	"image"

	machinecore "github.com/mirusu400/aram-core/core"
	skloader "github.com/mirusu400/aram-core/loader/skvm"
	shared "github.com/mirusu400/aram-core/runtime"
	skengine "github.com/mirusu400/aram-core/skvm"
)

const dragonKnightEXSKTSHA256 = "fa1fc7826e4f2dbd10a4793177d9aed3282e5b9812d47863edc2f64761850cc2"

// applySKVMTitleCompatibility adds only compatibility behavior that is tied
// to an exact shipped package and its expected metadata. The Dragon Knight EX
// build targets an SKT handset where Canvas.getHeight() excluded a 16-pixel
// system strip while drawing still covered the complete 120x160 framebuffer.
func applySKVMTitleCompatibility(
	config *shared.Config,
	source machinecore.Source,
	pkg skloader.Package,
	framebuffer image.Point,
) {
	if config == nil ||
		source.SHA256 != dragonKnightEXSKTSHA256 ||
		pkg.Descriptor.MainClass != "PNJDKEx" ||
		pkg.Descriptor.ProgramName != "0053597505" ||
		framebuffer != image.Pt(120, 160) {
		return
	}
	config.Device.Quirks = append(config.Device.Quirks, shared.DeviceQuirk{
		Name:    skengine.CanvasHeightInset16Quirk,
		Enabled: true,
	})
}
