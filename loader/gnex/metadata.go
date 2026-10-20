package gnex

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

const (
	resIconMagic   = uint32(0xdeadface)
	wmrBundleMagic = uint32(0xfacedead)
)

// MODDescriptor is the stable, length-prefixed identity block shared by all
// observed newer GNEX installer descriptors. Fields after Version are carrier
// download-manager policy and are deliberately left in the raw manifest.
type MODDescriptor struct {
	Timestamp   string
	MIMEType    string
	PayloadType string
	Version     string
}

// DecodeMODDescriptor finds and validates the four adjacent length-prefixed
// strings that identify a newer GNEX installer record. Observed records place
// the block at different offsets, so decoding follows its self-describing
// lengths instead of choosing one handset-specific fixed layout.
func DecodeMODDescriptor(data []byte) (MODDescriptor, error) {
	for offset := 0; offset+4 <= len(data); offset++ {
		fields := make([]string, 0, 4)
		cursor := offset
		valid := true
		for range 4 {
			if cursor+4 > len(data) {
				valid = false
				break
			}
			size := binary.LittleEndian.Uint32(data[cursor:])
			cursor += 4
			if size == 0 || size > 255 || uint64(cursor)+uint64(size) > uint64(len(data)) {
				valid = false
				break
			}
			field := data[cursor : cursor+int(size)]
			for _, value := range field {
				if value < 0x20 || value > 0x7e {
					valid = false
					break
				}
			}
			if !valid {
				break
			}
			fields = append(fields, string(field))
			cursor += int(size)
		}
		if valid && fields[1] == "application/x-gnex-sgs" &&
			strings.EqualFold(fields[2], "sgs") {
			return MODDescriptor{
				Timestamp: fields[0], MIMEType: fields[1],
				PayloadType: fields[2], Version: fields[3],
			}, nil
		}
	}
	return MODDescriptor{}, fmt.Errorf("gnex: MOD descriptor identity block not found")
}

// INFDescriptor is the stable prefix of the older carrier installer record.
// No field in the remaining carrier policy tail is needed to execute SGS.
type INFDescriptor struct {
	Digest         [16]byte
	Revision       byte
	Flags          uint32
	ApplicationKey string
}

// DecodeINFDescriptor decodes the exact prefix shared by the older reference
// corpus: marker 1, a 16-byte digest, revision, flags, and a ten-digit key.
func DecodeINFDescriptor(data []byte) (INFDescriptor, error) {
	const keyOffset = 22
	const keySize = 10
	if len(data) < keyOffset+keySize+1 || data[0] != 1 || data[keyOffset+keySize] != 0 {
		return INFDescriptor{}, fmt.Errorf("gnex: invalid INF descriptor prefix")
	}
	key := data[keyOffset : keyOffset+keySize]
	for _, value := range key {
		if value < '0' || value > '9' {
			return INFDescriptor{}, fmt.Errorf("gnex: invalid INF application key")
		}
	}
	result := INFDescriptor{
		Revision: data[17], Flags: binary.LittleEndian.Uint32(data[18:22]),
		ApplicationKey: string(key),
	}
	copy(result.Digest[:], data[1:17])
	return result, nil
}

// RESIcon is an external legacy launcher-icon resource. The raster payload is
// not part of the SGS media table and is not consumed during game execution.
type RESIcon struct {
	Payload []byte
}

// DecodeRESIcon validates the observed DEADFACE container and detaches its
// legacy raster payload. Raster interpretation belongs to launcher UI code.
func DecodeRESIcon(data []byte) (RESIcon, error) {
	if len(data) <= 4 || binary.LittleEndian.Uint32(data) != resIconMagic {
		return RESIcon{}, fmt.Errorf("gnex: invalid RES icon")
	}
	return RESIcon{Payload: bytes.Clone(data[4:])}, nil
}

// WMRResource is one typed, length-delimited external resource. Kind zero is
// an ordinary BMP in the reference title; kind one is SinjiSoft ABM data.
type WMRResource struct {
	Kind uint32
	Data []byte
}

// WMRBundle is the observed FACEDEAD external resource bundle. DeclaredSize
// is retained because the reference producer includes its two-byte trailer in
// the value even though that trailer is absent from the archived member.
type WMRBundle struct {
	DeclaredSize uint32
	Resources    []WMRResource
}

// DecodeWMRBundle decodes bounded kind/size/data records until exact EOF.
func DecodeWMRBundle(data []byte) (WMRBundle, error) {
	if len(data) < 16 || binary.LittleEndian.Uint32(data) != wmrBundleMagic {
		return WMRBundle{}, fmt.Errorf("gnex: invalid WMR bundle")
	}
	declared := binary.LittleEndian.Uint32(data[4:])
	if declared != uint32(len(data)) && declared != uint32(len(data))+2 {
		return WMRBundle{}, fmt.Errorf("gnex: WMR declared size %d does not match %d", declared, len(data))
	}
	result := WMRBundle{DeclaredSize: declared}
	for offset := 8; offset < len(data); {
		if len(data)-offset < 8 {
			return WMRBundle{}, fmt.Errorf("gnex: truncated WMR resource header")
		}
		kind := binary.LittleEndian.Uint32(data[offset:])
		size := binary.LittleEndian.Uint32(data[offset+4:])
		offset += 8
		if uint64(size) > uint64(len(data)-offset) {
			return WMRBundle{}, fmt.Errorf("gnex: truncated WMR resource payload")
		}
		result.Resources = append(result.Resources, WMRResource{
			Kind: kind, Data: bytes.Clone(data[offset : offset+int(size)]),
		})
		offset += int(size)
	}
	if len(result.Resources) == 0 {
		return WMRBundle{}, fmt.Errorf("gnex: empty WMR bundle")
	}
	return result, nil
}
