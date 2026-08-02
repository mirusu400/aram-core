package application

import (
	"crypto/sha256"
	"testing"

	"github.com/mirusu400/aram-core/loader/ktf"
)

func TestKTFDisplayOverrideRequiresExactPackageIdentity(t *testing.T) {
	override := ktfDisplayOverrides[0]
	descriptor := ktf.Descriptor{
		AID:           override.aid,
		MainClass:     override.mainClass,
		DisplayWidth:  override.declaredWidth,
		DisplayHeight: override.declaredHeight,
	}
	if width, height := resolveKTFDisplaySize(descriptor, override.clientHash); width != override.width || height != override.height {
		t.Fatalf("overridden display = %dx%d", width, height)
	}

	differentAID := descriptor
	differentAID.AID = "different"
	differentMainClass := descriptor
	differentMainClass.MainClass = "Different"
	differentDimensions := descriptor
	differentDimensions.DisplayWidth = 240
	differentDimensions.DisplayHeight = 320

	for name, changed := range map[string]struct {
		descriptor ktf.Descriptor
		hash       [sha256.Size]byte
	}{
		"client":     {descriptor: descriptor, hash: sha256.Sum256([]byte("different client"))},
		"aid":        {descriptor: differentAID, hash: override.clientHash},
		"main class": {descriptor: differentMainClass, hash: override.clientHash},
		"dimensions": {descriptor: differentDimensions, hash: override.clientHash},
	} {
		t.Run(name, func(t *testing.T) {
			width, height := resolveKTFDisplaySize(changed.descriptor, changed.hash)
			if width != changed.descriptor.DisplayWidth || height != changed.descriptor.DisplayHeight {
				t.Fatalf("lookalike display = %dx%d, want declared %dx%d", width, height, changed.descriptor.DisplayWidth, changed.descriptor.DisplayHeight)
			}
		})
	}
}
