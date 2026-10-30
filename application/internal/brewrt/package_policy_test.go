package brewrt

import "testing"

func TestPreferPackedAECHARRequiresExactPackage(t *testing.T) {
	for _, digest := range []string{
		issue319ArchiveSHA256,
		jinJanggiArchiveSHA256,
		theSINArchiveSHA256,
		unlimitedWarArchiveSHA256,
		narniaArchiveSHA256,
		ragnarokMagicianArchiveSHA256,
	} {
		if !preferPackedAECHARForDigest(digest) {
			t.Fatalf("packed AECHAR policy was not selected for %s", digest)
		}
	}

	lookalike := theSINArchiveSHA256[:len(theSINArchiveSHA256)-1] + "b"
	if preferPackedAECHARForDigest(lookalike) {
		t.Fatal("lookalike package selected packed AECHAR decoding")
	}
	if !preferPackedResourceAECHARForDigest(unlimitedWarArchiveSHA256) {
		t.Fatal("Unlimited War did not select packed resource strings")
	}
	if preferPackedResourceAECHARForDigest(unlimitedWarArchiveSHA256[:63]+"0") ||
		preferPackedResourceAECHARForDigest(theSINArchiveSHA256) {
		t.Fatal("unrelated package selected packed resource strings")
	}
}

func TestLegacyKeyOnPressRequiresExactPackage(t *testing.T) {
	for _, digest := range []string{devilOfMindArchiveSHA256, minibookArchiveSHA256} {
		if !legacyKeyOnPressForDigest(digest) {
			t.Fatalf("press-edge legacy key policy was not selected for %s", digest)
		}
	}
	if legacyKeyOnPressForDigest(devilOfMindArchiveSHA256[:63] + "0") {
		t.Fatal("lookalike package selected press-edge legacy keys")
	}
}
