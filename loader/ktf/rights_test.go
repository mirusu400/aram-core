package ktf

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const dcfTestContentID = "00WIPI00000000000001020304"

func encryptOMADCFBody(t *testing.T, key, plaintext []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCTR(block, make([]byte, aes.BlockSize)).XORKeyStream(ciphertext, plaintext)
	return ciphertext
}

func useRightsKeys(t *testing.T, keys RightsKeys) {
	t.Helper()
	t.Setenv(RightsKeyEnv, "")
	SetRightsKeys(keys)
	t.Cleanup(func() { SetRightsKeys(nil) })
}

func makeProtectedArchive(t *testing.T, key []byte) []byte {
	t.Helper()
	jar := makeZIP(t, map[string][]byte{
		"client.bin64": {0x70, 0x47},
		"icon.png":     {1, 2, 3},
	})
	body := encryptOMADCFBody(t, key, jar)
	return makeZIP(t, map[string][]byte{
		"01020304.jar": makeOMADCF(t, 2, uint64(len(jar)), body),
		"__adf__":      []byte("PID:pid\nAID:01020304\nMClass:Main\n"),
	})
}

func TestInspectOpensAESCTRDCFWithItsRightsKey(t *testing.T) {
	key := []byte("0123456789abcdef")
	useRightsKeys(t, RightsKeys{dcfTestContentID: key})

	pkg, err := Inspect(makeProtectedArchive(t, key))
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ClientName != "client.bin64" ||
		!bytes.Equal(pkg.Resources["icon.png"], []byte{1, 2, 3}) {
		t.Fatalf("decrypted DCF package = %+v", pkg)
	}
}

func TestInspectOpensAESCTRDCFKeyedByAID(t *testing.T) {
	key := []byte("0123456789abcdef")
	useRightsKeys(t, RightsKeys{"01020304": key})

	if _, err := Inspect(makeProtectedArchive(t, key)); err != nil {
		t.Fatal(err)
	}
}

func TestInspectReportsAWrongRightsKey(t *testing.T) {
	useRightsKeys(t, RightsKeys{dcfTestContentID: []byte("fedcba9876543210")})

	_, err := Inspect(makeProtectedArchive(t, []byte("0123456789abcdef")))
	if !errors.Is(err, ErrProtectedContent) {
		t.Fatalf("wrong-key error = %v", err)
	}
	var protected *ProtectedContentError
	if !errors.As(err, &protected) || !protected.WrongKey {
		t.Fatalf("wrong-key error = %#v", protected)
	}
}

func TestInspectKeepsReportingProtectedContentWithoutAKey(t *testing.T) {
	useRightsKeys(t, RightsKeys{"0BADC0DE": []byte("0123456789abcdef")})

	_, err := Inspect(makeProtectedArchive(t, []byte("0123456789abcdef")))
	var protected *ProtectedContentError
	if !errors.As(err, &protected) || protected.WrongKey {
		t.Fatalf("unkeyed DCF error = %#v", protected)
	}
	if protected.ContentID != dcfTestContentID || protected.Algorithm != 2 {
		t.Fatalf("unkeyed DCF error = %#v", protected)
	}
}

func TestInspectAcceptsACounterBlockPrefixedObject(t *testing.T) {
	key := []byte("0123456789abcdef")
	useRightsKeys(t, RightsKeys{dcfTestContentID: key})

	jar := makeZIP(t, map[string][]byte{"client.bin64": {0x70, 0x47}})
	counter := []byte("counter-block!!!")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, aes.BlockSize+len(jar))
	copy(body, counter)
	cipher.NewCTR(block, counter).XORKeyStream(body[aes.BlockSize:], jar)

	archive := makeZIP(t, map[string][]byte{
		"01020304.jar": makeOMADCF(t, 2, uint64(len(jar)), body),
		"__adf__":      []byte("PID:pid\nAID:01020304\nMClass:Main\n"),
	})
	pkg, err := Inspect(archive)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ClientName != "client.bin64" {
		t.Fatalf("prefixed-counter DCF package = %+v", pkg)
	}
}

func TestParseRightsKeysReadsBothSpellings(t *testing.T) {
	keys, err := ParseRightsKeys([]byte(
		"# 2009 화이트데이\n" +
			"00WIPI000000000001040928 = 000102030405060708090a0b0c0d0e0f\n" +
			"\r\n" +
			"01041fe1\t101112131415161718191A1B1C1D1E1F # trailing note\n",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("parsed keys = %v", keys)
	}
	first, _ := hex.DecodeString("000102030405060708090a0b0c0d0e0f")
	if !bytes.Equal(keys["00WIPI000000000001040928"], first) {
		t.Fatalf("content-id key = %x", keys["00WIPI000000000001040928"])
	}
	second, _ := hex.DecodeString("101112131415161718191a1b1c1d1e1f")
	if !bytes.Equal(keys["01041FE1"], second) {
		t.Fatalf("AID key = %x", keys["01041FE1"])
	}
}

func TestParseRightsKeysRejectsMalformedLines(t *testing.T) {
	for name, input := range map[string]string{
		"no key":     "01040928\n",
		"odd length": "01040928 = 00010203\n",
		"not hex":    "01040928 = zzz102030405060708090a0b0c0d0e0f\n",
	} {
		if _, err := ParseRightsKeys([]byte(input)); !errors.Is(err, ErrMalformedRightsKeys) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
}

func TestRightsKeysLoadFromTheEnvironmentPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.txt")
	if err := os.WriteFile(
		path,
		[]byte("01020304 = 30313233343536373839616263646566\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv(RightsKeyEnv, path)
	SetRightsKeys(nil)
	t.Cleanup(func() { SetRightsKeys(nil) })

	if _, err := Inspect(makeProtectedArchive(t, []byte("0123456789abcdef"))); err != nil {
		t.Fatal(err)
	}
	if ids := RightsKeyIDs(); len(ids) != 1 || ids[0] != "01020304" {
		t.Fatalf("rights key ids = %v", ids)
	}
}

func TestRightsKeyAIDShorthand(t *testing.T) {
	for id, want := range map[string]string{
		"00WIPI000000000001040928":   "01040928",
		"00WIPI00000000000103BD90":   "0103BD90",
		"00WIPI00000000000001020304": "01020304",
		"01040928":                   "",
		"00WIPI0000000000000000":     "",
	} {
		if got := rightsKeyAID(id); got != want {
			t.Fatalf("rightsKeyAID(%q) = %q, want %q", id, got, want)
		}
	}
}
