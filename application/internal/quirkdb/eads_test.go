package quirkdb

import "testing"

func TestEADSRuntimeIdentityRequiresExactContentAndName(t *testing.T) {
	key := EADSTitleRuntime{
		ImageName: "Synthetic", DatSHA256: "source-digest", RecordSHA256: "record-digest",
	}
	for _, test := range []struct {
		name, imageName, source, record string
		want                            bool
	}{
		{"original", "Synthetic", "source-digest", "record-digest", true},
		{"extracted", "Synthetic", "different-source", "record-digest", true},
		{"lookalike", "Synthetic", "different-source", "changed-record", false},
		{"wrong-name", "Other", "source-digest", "record-digest", false},
		{"empty-identities", "Synthetic", "", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := key.Matches(test.imageName, test.source, test.record); got != test.want {
				t.Fatalf("Matches = %t, want %t", got, test.want)
			}
		})
	}
	if (EADSTitleRuntime{ImageName: "Synthetic"}).Matches("Synthetic", "", "") {
		t.Fatal("an unconfigured digest matched an empty identity")
	}
}
