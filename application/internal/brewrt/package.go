// Package brewrt implements a bounded portable subset of the BREW runtime.
package brewrt

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"

	"github.com/mirusu400/aram-core/loader/brew"
	"golang.org/x/image/bmp"
)

const (
	ArchiveSHA256 = "99be6eb56702533eb0ef6f916e3f8a3b7978cb84fbee2de878856ad7a0db1649"
	ArchiveSize   = int64(739636)
	ModuleSHA256  = "5fbb0a3d36da30c592fc01cbe4e4cd5ece4cf3e41421d965cd5a67cd70badd7a"
	ModulePath    = "32536/kkrh.mod"
	MIFPath       = "32536.mif"

	ClassID               = uint32(0x0103d22a)
	DisplayClassID        = uint32(0x01001001)
	HeapClassID           = uint32(0x01001002)
	FileMgrClassID        = uint32(0x01001003)
	OptionalDeviceClassID = uint32(0x018000fe)
	KTFServiceClassID     = uint32(0x018000fc)
	SoundPlayerClassID    = uint32(0x01002000)
	GraphicsClassID       = uint32(0x01002001)
	Sound10ClassID        = uint32(0x0100100b)
	TAPIClassID           = uint32(0x01001007)
	Net11ClassID          = uint32(0x01001105)
	TextCtl10ClassID      = uint32(0x01003009)
	IconViewCtl10ClassID  = uint32(0x01003003)
	SoftKeyCtl10ClassID   = uint32(0x01003001)
	// FirstUnsupportedClassID is retained for callers that recorded the original
	// bootstrap milestone before the display contract was implemented.
	FirstUnsupportedClassID = DisplayClassID

	mifClassIDOffset        = 0x20b4
	mifSplashOffset         = 0x74
	maxExecutableModuleSize = 8 << 20
)

// Package contains the inspected executable module and immutable archive data.
type Package struct {
	Module        []byte
	ClassIDs      []uint32
	Splash        *image.RGBA
	Files         map[string][]byte
	Authenticated bool
}

// Match validates a bounded BREW container, selects its sole executable module,
// and derives its application ClassID from bounded MIF metadata. Generic carrier
// signatures are presence-checked only; Package.Authenticated is reserved for
// the exact hash-qualified reference package.
func Match(data []byte) (pkg Package, matched bool, err error) {
	inspected, err := brew.Inspect(data)
	if err != nil {
		return Package{}, false, nil
	}
	if len(inspected.Modules) != 1 || len(inspected.MIFs) != 1 {
		return Package{}, false, nil
	}
	moduleMetadata := inspected.Modules[0]
	moduleName := moduleMetadata.Name
	module := inspected.Files[moduleName]
	if !moduleMetadata.SignaturePresent || len(module) < 8 || len(module) > maxExecutableModuleSize || !knownModuleVeneer(module) {
		return Package{}, false, nil
	}
	classID, ok := mifApplicationClassID(inspected.MIFs[0], inspected.Files[inspected.MIFs[0].Name])
	if !ok {
		return Package{}, false, nil
	}

	var splash *image.RGBA
	authenticated := false
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) == ArchiveSHA256 {
		moduleDigest := sha256.Sum256(module)
		if moduleName != ModulePath || hex.EncodeToString(moduleDigest[:]) != ModuleSHA256 {
			return Package{}, true, fmt.Errorf("authenticated BREW reference module contract mismatch")
		}
		authenticated = true
		mif, ok := inspected.Files[MIFPath]
		if !ok || len(mif) < mifClassIDOffset+4 || binary.LittleEndian.Uint32(mif[mifClassIDOffset:]) != ClassID {
			return Package{}, true, fmt.Errorf("authenticated BREW reference MIF class contract mismatch")
		}
		splash, err = decodeSplash(mif)
		if err != nil {
			return Package{}, true, err
		}
	}
	files := make(map[string][]byte, len(inspected.Files))
	for name, contents := range inspected.Files {
		files[name] = append([]byte(nil), contents...)
	}
	return Package{
		Module:        append([]byte(nil), module...),
		ClassIDs:      []uint32{classID},
		Splash:        splash,
		Files:         files,
		Authenticated: authenticated,
	}, true, nil
}

func knownModuleVeneer(module []byte) bool {
	first := binary.LittleEndian.Uint32(module[:4])
	return first == 0xe92d400e || first == 0xe92d400c || first == 0xe1a0c002
}

func mifApplicationClassID(metadata brew.Metadata, data []byte) (uint32, bool) {
	if metadata.IndexCount == 0 {
		return 0, false
	}
	indexStart := uint64(metadata.IndexOffset)
	count := uint64(metadata.IndexCount)
	if indexStart+(count+1)*4 > uint64(len(data)) {
		return 0, false
	}
	previous := uint32(0)
	for index := uint64(0); index <= count; index++ {
		offset := binary.LittleEndian.Uint32(data[indexStart+index*4:])
		if index == 0 && offset != metadata.DataOffset || index != 0 && offset < previous {
			return 0, false
		}
		previous = offset
	}
	recordStart := binary.LittleEndian.Uint32(data[indexStart+(count-1)*4:])
	recordEnd := binary.LittleEndian.Uint32(data[indexStart+count*4:])
	dataEnd := uint64(metadata.DataOffset) + uint64(metadata.DataSize)
	if recordStart < metadata.DataOffset || recordEnd < recordStart+4 || uint64(recordEnd) > dataEnd || uint64(recordEnd) > uint64(len(data)) {
		return 0, false
	}
	classID := binary.LittleEndian.Uint32(data[recordStart : recordStart+4])
	if classID < 0x01010000 || classID > 0x010fffff {
		return 0, false
	}
	return classID, true
}

func decodeSplash(mif []byte) (*image.RGBA, error) {
	if len(mif) < mifSplashOffset+6 || !bytes.Equal(mif[mifSplashOffset:mifSplashOffset+2], []byte("BM")) {
		return nil, fmt.Errorf("authenticated BREW MIF splash contract mismatch")
	}
	size := int(binary.LittleEndian.Uint32(mif[mifSplashOffset+2:]))
	if size < 14 || mifSplashOffset+size > len(mif) {
		return nil, fmt.Errorf("authenticated BREW MIF splash span is invalid")
	}
	decoded, err := bmp.Decode(bytes.NewReader(mif[mifSplashOffset : mifSplashOffset+size]))
	if err != nil {
		return nil, fmt.Errorf("decode authenticated BREW MIF splash: %w", err)
	}
	if decoded.Bounds().Dx() != 120 || decoded.Bounds().Dy() != 61 {
		return nil, fmt.Errorf("authenticated BREW MIF splash is %dx%d, want 120x61", decoded.Bounds().Dx(), decoded.Bounds().Dy())
	}
	rgba := image.NewRGBA(image.Rect(0, 0, decoded.Bounds().Dx(), decoded.Bounds().Dy()))
	draw.Draw(rgba, rgba.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	return rgba, nil
}
