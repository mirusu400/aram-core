package cheat

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const testCatalogJSON = `{
  "version": 2,
  "title": {
    "image_sha256": "abababababababababababababababababababababababababababababababab",
    "name": "Synthetic Title",
    "carrier": "lgt",
    "format": "raptor-wipi-c",
    "profile_id": "wipi-1.2.1/lgt/raptor"
  },
  "cheats": [
    {
      "id": "skip-auth",
      "name": "Skip server authentication",
      "description": "Branch past the network check.",
      "category": "bypass",
      "author": "aram",
      "reference": "https://github.com/mirusu400/aram-emu/issues/4",
      "restore_on_disable": true,
      "patches": [
        {
          "address": "0x00001000",
          "value": "00000000",
          "expected": "01020304",
          "note": "force the check to succeed"
        },
        {
          "address": "0x00001004",
          "value": "0000",
          "expected": "0506"
        }
      ]
    }
  ]
}`

func TestParseCatalogReadsHexAddressesAndBytes(t *testing.T) {
	t.Parallel()
	catalog, err := ParseCatalog([]byte(testCatalogJSON))
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Title.Name != "Synthetic Title" ||
		catalog.Title.ProfileID != "wipi-1.2.1/lgt/raptor" {
		t.Fatalf("catalog title = %+v", catalog.Title)
	}
	if len(catalog.Cheats) != 1 || len(catalog.Cheats[0].Patches) != 2 {
		t.Fatalf("catalog cheats = %+v", catalog.Cheats)
	}
	patch := catalog.Cheats[0].Patches[0]
	if patch.Address != 0x1000 ||
		!bytes.Equal(patch.Value, []byte{0, 0, 0, 0}) ||
		!bytes.Equal(patch.Expected, []byte{1, 2, 3, 4}) {
		t.Fatalf("first patch = %+v", patch)
	}

	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"address":"0x00001000"`) ||
		!strings.Contains(string(encoded), `"expected":"01020304"`) {
		t.Fatalf("re-encoded catalog = %s", encoded)
	}
	round, err := ParseCatalog(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if round.Cheats[0].Patches[1].Address != 0x1004 {
		t.Fatalf("round tripped patch = %+v", round.Cheats[0].Patches[1])
	}
}

func TestParseCatalogRejectsMalformedDocuments(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"unknown field":  `{"version":2,"surprise":true}`,
		"wrong version":  `{"version":99,"title":{"image_sha256":"` + strings.Repeat("ab", 32) + `"},"cheats":[]}`,
		"missing target": `{"version":2,"title":{},"cheats":[]}`,
		"no cheats": `{"version":2,"title":{"image_sha256":"` +
			strings.Repeat("ab", 32) + `"},"cheats":[]}`,
		"uppercase id": `{"version":2,"title":{"image_sha256":"` +
			strings.Repeat("ab", 32) + `"},"cheats":[{"id":"Skip","name":"n",` +
			`"patches":[{"address":"0x0","value":"00","expected":"01"}]}]}`,
		"expected length mismatch": `{"version":2,"title":{"image_sha256":"` +
			strings.Repeat("ab", 32) + `"},"cheats":[{"id":"skip","name":"n",` +
			`"patches":[{"address":"0x0","value":"0000","expected":"01"}]}]}`,
		"duplicate id": `{"version":2,"title":{"image_sha256":"` +
			strings.Repeat("ab", 32) + `"},"cheats":[` +
			`{"id":"skip","name":"n","patches":[{"address":"0x0","value":"00","expected":"01"}]},` +
			`{"id":"skip","name":"n","patches":[{"address":"0x4","value":"00","expected":"01"}]}]}`,
	}
	for name, document := range cases {
		if _, err := ParseCatalog([]byte(document)); err == nil {
			t.Fatalf("%s: parsed a malformed catalog", name)
		}
	}
}

func TestCatalogVersionMismatchIsIdentifiable(t *testing.T) {
	t.Parallel()
	document := `{"version":3,"title":{"image_sha256":"` + strings.Repeat("ab", 32) +
		`"},"cheats":[{"id":"skip","name":"n",` +
		`"patches":[{"address":"0x0","value":"00","expected":"01"}]}]}`
	_, err := ParseCatalog([]byte(document))
	if !errors.Is(err, ErrUnsupportedCatalogVersion) {
		t.Fatalf("catalog version error = %v", err)
	}
}

func TestCheatCodesCarryTitleIdentityAndStableIDs(t *testing.T) {
	t.Parallel()
	catalog, err := ParseCatalog([]byte(testCatalogJSON))
	if err != nil {
		t.Fatal(err)
	}
	codes, err := catalog.Cheats[0].Codes(catalog.Title.ImageSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 2 {
		t.Fatalf("codes = %+v", codes)
	}
	if codes[0].ID != "skip-auth#0" || codes[1].ID != "skip-auth#1" {
		t.Fatalf("code IDs = %q, %q", codes[0].ID, codes[1].ID)
	}
	if codes[0].TargetSHA256 != catalog.Title.ImageSHA256 ||
		codes[0].Address != 0x1000 ||
		!codes[0].RestoreOnDisable {
		t.Fatalf("first code = %+v", codes[0])
	}
	if !strings.Contains(codes[0].Description, "force the check to succeed") {
		t.Fatalf("first code description = %q", codes[0].Description)
	}
}
