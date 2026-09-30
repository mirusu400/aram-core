package brewrt

import "testing"

func TestPreferPackedAECHARRequiresExactPackage(t *testing.T) {
	for _, digest := range []string{
		issue319ArchiveSHA256,
		jinJanggiArchiveSHA256,
		theSINArchiveSHA256,
	} {
		if !preferPackedAECHARForDigest(digest) {
			t.Fatalf("packed AECHAR policy was not selected for %s", digest)
		}
	}

	lookalike := theSINArchiveSHA256[:len(theSINArchiveSHA256)-1] + "b"
	if preferPackedAECHARForDigest(lookalike) {
		t.Fatal("lookalike package selected packed AECHAR decoding")
	}
}
