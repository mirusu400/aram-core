package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	machinecore "github.com/mirusu400/aram-core/core"
	"github.com/mirusu400/aram-core/loader"
)

func syntheticApplicationGNEX32SGS() []byte {
	const prefix, descriptorStart = 32, 0x100a0
	const symbolData = descriptorStart + 8
	const mediaDescriptor = descriptorStart + 12
	const mediaData = descriptorStart + 24
	data := make([]byte, mediaData+6)
	binary.LittleEndian.PutUint32(data, prefix)
	data[prefix], data[prefix+5] = 4, 4
	copy(data[prefix+10:], "Profile Test")
	binary.LittleEndian.PutUint32(data[prefix+0x1c:], descriptorStart-prefix-4)
	binary.LittleEndian.PutUint32(data[prefix+0x24:], descriptorStart-prefix-8)
	for offset, value := range map[int]int{
		0x48: descriptorStart,
		0x4c: symbolData,
		0x50: mediaDescriptor,
		0x54: mediaData,
	} {
		binary.LittleEndian.PutUint32(data[prefix+offset:], uint32(value-prefix))
	}
	binary.LittleEndian.PutUint32(data[prefix+0x78:], uint32(len(data)-prefix))
	data[descriptorStart-4] = 0xff
	binary.LittleEndian.PutUint16(data[descriptorStart:], 1)
	binary.LittleEndian.PutUint16(data[descriptorStart+2:], 1)
	copy(data[symbolData:], []byte{1, 2, 3, 4})
	binary.LittleEndian.PutUint32(data[mediaDescriptor:], 0x100)
	binary.LittleEndian.PutUint32(data[mediaDescriptor+4:], 6)
	copy(data[mediaData:], []byte{1, 2, 3, 4, 5, 6})
	return data
}

func TestFactoryReportsUnqualifiedGNEX32Capabilities(t *testing.T) {
	data := syntheticApplicationGNEX32SGS()
	machine, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: "other-v4.sgs", ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	if machine != nil {
		_ = machine.Close()
		t.Fatal("unqualified GNEX32 image unexpectedly created a machine")
	}
	var unsupported *UnsupportedPlatformError
	if !errors.As(err, &unsupported) {
		t.Fatalf("GNEX32 diagnostic is not typed: %v", err)
	}
	if unsupported.Kind != loader.KindGNEX || unsupported.ProfileID != GNEX32UnqualifiedProfileID {
		t.Fatalf("GNEX32 diagnostic identity = %+v", unsupported)
	}
	for _, phrase := range []string{
		"structurally valid GNEX32", "no qualified compatibility profile",
		"opcode/service/media capability preflight failed", "unknown opcode", "code offset 65528",
	} {
		if !strings.Contains(unsupported.Reason, phrase) {
			t.Fatalf("GNEX32 diagnostic lacks %q: %s", phrase, unsupported.Reason)
		}
	}
	if strings.Contains(unsupported.Reason, "execution is not implemented") {
		t.Fatalf("GNEX32 diagnostic retained stale implementation claim: %s", unsupported.Reason)
	}
}

func TestFactoryReportsPassingGNEX32CapabilityPreflightWithoutProfile(t *testing.T) {
	data := syntheticApplicationGNEX32SGS()
	const descriptorStart = 0x100a0
	data[descriptorStart-8] = 0xff
	_, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: "covered-v4.sgs", ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	var unsupported *UnsupportedPlatformError
	if !errors.As(err, &unsupported) || !strings.Contains(unsupported.Reason, "capability preflight passed") ||
		!strings.Contains(unsupported.Reason, "no qualified compatibility profile") {
		t.Fatalf("covered unqualified diagnostic = %v", err)
	}
}

func TestFactoryDoesNotQualifyStructurallyInvalidGNEX32(t *testing.T) {
	data := syntheticApplicationGNEX32SGS()
	const descriptorStart = 0x100a0
	binary.LittleEndian.PutUint16(data[descriptorStart+2:], 0xffff)
	_, err := NewFactory().Create(context.Background(), machinecore.Source{
		Name: "broken-v4.sgs", ReaderAt: bytes.NewReader(data), Size: int64(len(data)),
	})
	var unsupported *UnsupportedPlatformError
	if err == nil || errors.As(err, &unsupported) {
		t.Fatalf("invalid GNEX32 image received a compatibility identity: %v", err)
	}
}
