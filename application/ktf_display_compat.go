package application

import (
	"crypto/sha256"

	"github.com/mirusu400/aram-core/loader/ktf"
)

type ktfDisplayOverride struct {
	aid, mainClass                string
	clientHash                    [sha256.Size]byte
	declaredWidth, declaredHeight int
	width, height                 int
}

var ktfDisplayOverrides = []ktfDisplayOverride{
	{
		aid:            "01035ACD",
		mainClass:      "Clet",
		clientHash:     mustKTFSHA256("1ac810f1af96676e337817e8ddec400508924047ae652a6d34fbd3b2c94ffe96"),
		declaredWidth:  176,
		declaredHeight: 220,
		width:          240,
		height:         320,
	},
}

// effectiveKTFDisplaySize applies narrowly identified handset metadata
// corrections. Most descriptors are authoritative, but a small number of
// shipped packages carry a display size for a different build while their
// client uses a larger fixed coordinate system. Every correction is guarded
// by the declared metadata and the exact client digest so unrelated titles
// keep their descriptor-selected framebuffer.
func effectiveKTFDisplaySize(pkg ktf.Package) (int, int) {
	return resolveKTFDisplaySize(
		pkg.Descriptor,
		sha256.Sum256(pkg.Client),
	)
}

func resolveKTFDisplaySize(
	descriptor ktf.Descriptor,
	clientHash [sha256.Size]byte,
) (int, int) {
	for _, override := range ktfDisplayOverrides {
		if descriptor.AID == override.aid &&
			descriptor.MainClass == override.mainClass &&
			descriptor.DisplayWidth == override.declaredWidth &&
			descriptor.DisplayHeight == override.declaredHeight &&
			clientHash == override.clientHash {
			return override.width, override.height
		}
	}
	return descriptor.DisplayWidth, descriptor.DisplayHeight
}
