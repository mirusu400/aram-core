package ktf

import (
	"crypto/sha256"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/quirkdb"
	"github.com/mirusu400/aram-core/loader/ktf"
)

func TestKTFDisplayOverrideRequiresExactPackageIdentity(t *testing.T) {
	override := quirkdb.DisplayOverrides[0]
	descriptor := ktf.Descriptor{
		AID:           override.Key.AID,
		MainClass:     override.Key.MainClass,
		DisplayWidth:  override.DeclaredWidth,
		DisplayHeight: override.DeclaredHeight,
	}
	if width, height := resolveKTFDisplaySize(descriptor, override.Key.ClientSHA256); width != override.Width || height != override.Height {
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
		"aid":        {descriptor: differentAID, hash: override.Key.ClientSHA256},
		"main class": {descriptor: differentMainClass, hash: override.Key.ClientSHA256},
		"dimensions": {descriptor: differentDimensions, hash: override.Key.ClientSHA256},
	} {
		t.Run(name, func(t *testing.T) {
			width, height := resolveKTFDisplaySize(changed.descriptor, changed.hash)
			if width != changed.descriptor.DisplayWidth || height != changed.descriptor.DisplayHeight {
				t.Fatalf("lookalike display = %dx%d, want declared %dx%d", width, height, changed.descriptor.DisplayWidth, changed.descriptor.DisplayHeight)
			}
		})
	}
}
