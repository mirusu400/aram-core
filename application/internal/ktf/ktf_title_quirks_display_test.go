package ktf

import (
	"crypto/sha256"
	"testing"

	"github.com/mirusu400/aram-core/application/internal/quirkdb"
	"github.com/mirusu400/aram-core/loader/ktf"
)

func TestKTFDisplayOverrideRequiresExactPackageIdentity(t *testing.T) {
	for _, override := range quirkdb.DisplayOverrides {
		t.Run(override.Key.AID, func(t *testing.T) {
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
			differentDimensions.DisplayWidth++

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
		})
	}
}

func TestKTFPresentationLimitRequiresExactPackageIdentity(t *testing.T) {
	for _, entry := range quirkdb.KTFPresentationLimits {
		descriptor := ktf.Descriptor{
			AID:       entry.Key.AID,
			MainClass: entry.Key.MainClass,
		}
		if limit, ok := resolveKTFPresentationLimit(
			descriptor,
			entry.Key.ClientSHA256,
		); !ok || limit != entry.MaxPerQuantum {
			t.Fatalf("presentation limit = %d, %t", limit, ok)
		}

		lookalikes := []struct {
			descriptor ktf.Descriptor
			hash       [sha256.Size]byte
		}{
			{descriptor: ktf.Descriptor{AID: "different", MainClass: descriptor.MainClass}, hash: entry.Key.ClientSHA256},
			{descriptor: ktf.Descriptor{AID: descriptor.AID, MainClass: "Different"}, hash: entry.Key.ClientSHA256},
			{descriptor: descriptor, hash: sha256.Sum256([]byte("different client"))},
		}
		for _, lookalike := range lookalikes {
			if limit, ok := resolveKTFPresentationLimit(lookalike.descriptor, lookalike.hash); ok || limit != 0 {
				t.Fatalf("lookalike presentation limit = %d, %t", limit, ok)
			}
		}
	}
}

func TestKTFMainThreadLivenessRequiresExactPackageIdentity(t *testing.T) {
	key := quirkdb.KTFMainThreadLivenessOverrides[0]
	descriptor := ktf.Descriptor{AID: key.AID, MainClass: key.MainClass}
	if !resolveMainThreadLivenessCompatibility(descriptor, key.ClientSHA256) {
		t.Fatal("MapleStory Archer liveness compatibility did not match")
	}
	for name, changed := range map[string]struct {
		descriptor ktf.Descriptor
		hash       [sha256.Size]byte
	}{
		"aid":        {ktf.Descriptor{AID: "other", MainClass: key.MainClass}, key.ClientSHA256},
		"main class": {ktf.Descriptor{AID: key.AID, MainClass: "Other"}, key.ClientSHA256},
		"client":     {descriptor, sha256.Sum256([]byte("different client"))},
	} {
		t.Run(name, func(t *testing.T) {
			if resolveMainThreadLivenessCompatibility(changed.descriptor, changed.hash) {
				t.Fatal("liveness compatibility matched another package")
			}
		})
	}
}

func TestKTFDataInputStreamJavaABIRequiresExactPackageIdentity(t *testing.T) {
	key := quirkdb.KTFDataInputStreamJavaABIs[0]
	descriptor := ktf.Descriptor{AID: key.AID, MainClass: key.MainClass}
	if !resolveDataInputStreamJavaABI(descriptor, key.ClientSHA256) {
		t.Fatal("ED3 DataInputStream Java ABI did not match")
	}
	for name, changed := range map[string]struct {
		descriptor ktf.Descriptor
		hash       [sha256.Size]byte
	}{
		"aid":        {ktf.Descriptor{AID: "other", MainClass: key.MainClass}, key.ClientSHA256},
		"main class": {ktf.Descriptor{AID: key.AID, MainClass: "Other"}, key.ClientSHA256},
		"client":     {descriptor, sha256.Sum256([]byte("different client"))},
	} {
		t.Run(name, func(t *testing.T) {
			if resolveDataInputStreamJavaABI(changed.descriptor, changed.hash) {
				t.Fatal("DataInputStream Java ABI matched another package")
			}
		})
	}
}
