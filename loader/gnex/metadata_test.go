package gnex

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func lpField(value string) []byte {
	result := make([]byte, 4+len(value))
	binary.LittleEndian.PutUint32(result, uint32(len(value)))
	copy(result[4:], value)
	return result
}

func TestDecodeMODDescriptorUsesLengthPrefixedIdentity(t *testing.T) {
	data := bytes.Repeat([]byte{0}, 40)
	for _, value := range []string{"2099-6-22T3:19:45", "application/x-gnex-sgs", "sgs", "36.9.0"} {
		data = append(data, lpField(value)...)
	}
	data = append(data, bytes.Repeat([]byte{0}, 300)...)
	got, err := DecodeMODDescriptor(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Timestamp != "2099-6-22T3:19:45" || got.MIMEType != "application/x-gnex-sgs" ||
		got.PayloadType != "sgs" || got.Version != "36.9.0" {
		t.Fatalf("descriptor = %+v", got)
	}
	for _, bad := range [][]byte{nil, data[:50], bytes.Replace(data, []byte("application/x-gnex-sgs"), []byte("application/x-other---"), 1)} {
		if _, err := DecodeMODDescriptor(bad); err == nil {
			t.Fatalf("accepted malformed MOD of length %d", len(bad))
		}
	}
}

func TestDecodeINFDescriptorPrefix(t *testing.T) {
	data := make([]byte, 80)
	data[0] = 1
	copy(data[1:17], []byte("0123456789abcdef"))
	data[17] = 2
	binary.LittleEndian.PutUint32(data[18:], 1)
	copy(data[22:], "3407041031")
	got, err := DecodeINFDescriptor(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 2 || got.Flags != 1 || got.ApplicationKey != "3407041031" ||
		string(got.Digest[:]) != "0123456789abcdef" {
		t.Fatalf("descriptor = %+v", got)
	}
	for _, mutate := range []func([]byte){
		func(data []byte) { data[0] = 0 },
		func(data []byte) { data[22] = 'x' },
		func(data []byte) { data[32] = 1 },
	} {
		bad := bytes.Clone(data)
		mutate(bad)
		if _, err := DecodeINFDescriptor(bad); err == nil {
			t.Fatal("accepted malformed INF")
		}
	}
}

func TestDecodeRESIconDetachesPayload(t *testing.T) {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data, resIconMagic)
	copy(data[4:], []byte{1, 2, 3, 4})
	icon, err := DecodeRESIcon(data)
	if err != nil {
		t.Fatal(err)
	}
	data[4] = 9
	if !bytes.Equal(icon.Payload, []byte{1, 2, 3, 4}) {
		t.Fatalf("payload = %v", icon.Payload)
	}
	if _, err := DecodeRESIcon(data[:4]); err == nil {
		t.Fatal("accepted empty RES")
	}
}

func TestDecodeWMRBundleBoundsAndCopies(t *testing.T) {
	data := make([]byte, 8)
	binary.LittleEndian.PutUint32(data, wmrBundleMagic)
	for _, resource := range []WMRResource{{Kind: 0, Data: []byte("BMdata")}, {Kind: 1, Data: []byte("ABMdata")}} {
		header := make([]byte, 8)
		binary.LittleEndian.PutUint32(header, resource.Kind)
		binary.LittleEndian.PutUint32(header[4:], uint32(len(resource.Data)))
		data = append(data, header...)
		data = append(data, resource.Data...)
	}
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)+2))
	bundle, err := DecodeWMRBundle(data)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.DeclaredSize != uint32(len(data)+2) || len(bundle.Resources) != 2 ||
		bundle.Resources[0].Kind != 0 || string(bundle.Resources[0].Data) != "BMdata" ||
		bundle.Resources[1].Kind != 1 || string(bundle.Resources[1].Data) != "ABMdata" {
		t.Fatalf("bundle = %+v", bundle)
	}
	data[16] = 'X'
	if string(bundle.Resources[0].Data) != "BMdata" {
		t.Fatal("decoded resource aliases input")
	}
	for _, bad := range [][]byte{data[:7], data[:len(data)-1]} {
		if _, err := DecodeWMRBundle(bad); err == nil {
			t.Fatalf("accepted malformed WMR of length %d", len(bad))
		}
	}
}
