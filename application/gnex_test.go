package application

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
)

// syntheticGNEXSGS builds a minimal .SGS payload with a valid header (see
// loader/gnex.ParseHeader): format version 1, the constant byte at offset 5,
// then the cp949 encoding of "북벌" ("Bukbeol") - a title borrowed from the
// reference corpus rather than re-derived here, terminated by 0x00 0x00.
func syntheticGNEXSGS() []byte {
	var buf bytes.Buffer
	buf.WriteByte(1)                          // format version
	buf.Write([]byte{0xff, 0x0c, 0xff, 0xff}) // unmodeled
	buf.WriteByte(0x01)                       // constant
	buf.Write([]byte{0x00, 0x85})             // title checksum (unmodeled algorithm)
	buf.Write([]byte{0x00, 0x00})             // unmodeled
	buf.Write([]byte{0xba, 0xcf, 0xb9, 0xfa}) // cp949 "북벌"
	buf.Write([]byte{0x00, 0x00})             // title terminator
	buf.Write([]byte{0x01, 0x02, 0x03, 0x04}) // undecoded GVM body placeholder
	return buf.Bytes()
}

// Mirrors TestFactoryNamesAndroidPackagesInsteadOfBlamingTheContainer: a GNEX
// title's .SGS payload has no ABHS/EADS records, so without this check it
// would surface as "no valid ABHS or EADS records" instead of naming the
// format ARAM recognizes but does not yet execute.
func TestFactoryNamesGNEXPackagesInsteadOfBlamingTheContainer(t *testing.T) {
	archive := testZIP(t, map[string][]byte{
		"3407041031.inf": []byte("older GNEX descriptor shape, not decoded"),
		"3407041031.sgs": syntheticGNEXSGS(),
	})
	_, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name:     "북벌.zip",
		ReaderAt: bytes.NewReader(archive),
		Size:     int64(len(archive)),
	})
	if !errors.Is(err, ErrUnsupportedSource) {
		t.Fatalf("GNEX package error = %v", err)
	}
	if !strings.Contains(err.Error(), "GNEX") || !strings.Contains(err.Error(), "북벌") {
		t.Fatalf("GNEX package error does not name the format and title: %v", err)
	}
	if errors.Is(err, loader.ErrNoContainerRecords) {
		t.Fatalf("GNEX package was reported as a broken container: %v", err)
	}
}
