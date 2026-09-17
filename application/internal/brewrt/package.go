// Package brewrt implements the deliberately narrow executable contract for
// one preserved BREW title. It does not claim a general BREW ABI.
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
	ModuleSHA256  = "5fbb0a3d36da30c592fc01cbe4e4cd5ece4cf3e41421d965cd5a67cd70badd7a"
	ModulePath    = "32536/kkrh.mod"
	MIFPath       = "32536.mif"

	ClassID = uint32(0x0103d22a)
	// FirstUnsupportedClassID is the first shell service requested after the
	// module factory hands control to the applet constructor. The runtime stops
	// before this boundary because its vtable contract is not yet known.
	FirstUnsupportedClassID = uint32(0x01001001)

	mifClassIDOffset = 0x20b4
	mifSplashOffset  = 0x74
)

// Package contains only data authenticated by both the archive and MOD hashes.
type Package struct {
	Module []byte
	Splash *image.RGBA
}

// Match returns matched=false without parsing for every archive except the
// single researched title. This keeps generic BREW recognition unsupported.
func Match(data []byte) (pkg Package, matched bool, err error) {
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != ArchiveSHA256 {
		return Package{}, false, nil
	}

	inspected, err := brew.Inspect(data)
	if err != nil {
		return Package{}, true, fmt.Errorf("inspect authenticated BREW archive: %w", err)
	}
	module, ok := inspected.Files[ModulePath]
	if !ok {
		return Package{}, true, fmt.Errorf("authenticated BREW archive is missing %q", ModulePath)
	}
	moduleDigest := sha256.Sum256(module)
	if hex.EncodeToString(moduleDigest[:]) != ModuleSHA256 {
		return Package{}, true, fmt.Errorf("authenticated BREW MOD SHA-256 mismatch")
	}

	mif, ok := inspected.Files[MIFPath]
	if !ok {
		return Package{}, true, fmt.Errorf("authenticated BREW archive is missing %q", MIFPath)
	}
	if len(mif) < mifClassIDOffset+4 || binary.LittleEndian.Uint32(mif[mifClassIDOffset:]) != ClassID {
		return Package{}, true, fmt.Errorf("authenticated BREW MIF class contract mismatch")
	}
	splash, err := decodeSplash(mif)
	if err != nil {
		return Package{}, true, err
	}
	return Package{
		Module: append([]byte(nil), module...),
		Splash: splash,
	}, true, nil
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
