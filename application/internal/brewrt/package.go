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
	ShellClassID          = uint32(0x01001000)
	DisplayClassID        = uint32(0x01001001)
	HeapClassID           = uint32(0x01001002)
	FileMgrClassID        = uint32(0x01001003)
	DBMgrClassID          = uint32(0x01001008)
	OptionalDeviceClassID = uint32(0x018000fe)
	KTFServiceClassID     = uint32(0x018000fc)
	SoundPlayerClassID    = uint32(0x01002000)
	GraphicsClassID       = uint32(0x01002001)
	Sound10ClassID        = uint32(0x0100100b)
	MemAStreamClassID     = uint32(0x0100100c)
	StaticClassID         = uint32(0x0100100a) // IStatic text control
	TAPIClassID           = uint32(0x01001007)
	Net11ClassID          = uint32(0x01001105)
	TextCtl10ClassID      = uint32(0x01003009)
	IconViewCtl10ClassID  = uint32(0x01003003)
	SoftKeyCtl10ClassID   = uint32(0x01003001)
	MenuCtl10ClassID      = uint32(0x01003000)
	DateCtl10ClassID      = uint32(0x01003005)
	ClockCtl10ClassID     = uint32(0x01003007)
	WinBMPClassID         = uint32(0x01004001)
	BitmapClassID         = uint32(0x01001021)
	// FirstUnsupportedClassID is retained for callers that recorded the original
	// bootstrap milestone before the display contract was implemented.
	FirstUnsupportedClassID = DisplayClassID

	mifClassIDOffset        = 0x20b4
	mifSplashOffset         = 0x74
	maxExecutableModuleSize = 8 << 20
)

const issue319ArchiveSHA256 = "2cfa3dff0779456f8482ccfbda740436101699186fdd9537b7a9108f8bfcedbf"
const unlimitedWarArchiveSHA256 = "185d88339b04381697ebfcb31bbc8b6a7eff22f4ad588b1726e0f2708227d545"
const issue321ArchiveSHA256 = "4fae0b6a37e501163f6eabbda2b400bdfd4682ae389217bc26a7dbee564cb360"
const jinJanggiArchiveSHA256 = "06369c8db97fb4e28165297ac2557c0de050dfb6986ea429d3411ffedfa891b9"
const tengaiArchiveSHA256 = "01f8fe8784138792822df9306127279f038813ea62c00385d256b77b61a025cf"
const theSINArchiveSHA256 = "8b33b7b3c20dabfe38cdfc905c482ef179827cf8b55b47c9057f60995bea96aa"
const minibookArchiveSHA256 = "9d17a101d5c9e31e789698f31fb41ba5448b624eb63b37193be13b12fdbb46c0"
const minigame2005ArchiveSHA256 = "0622f4c830832e04d310c3496680df8307060fa74f4f63a9ee9c09ae16300e5c"
const battleDrumXArchiveSHA256 = "a50f7decb608272daca545a1beca126286cba8eeb8b6e230f14448393b143ca2"
const topGun2ArchiveSHA256 = "43392c5a657ac36aca605a3cde727bd817b5672594be55b8470bacdb924468f1"
const milkTycoon2ArchiveSHA256 = "9f7366ca1902b91c359b271ee5721fdccff7e39f1ebda6849454e6f9572a3a4f"
const baseball2006ArchiveSHA256 = "79a2b320c46fba8650dc21e2a525f8226d5e2592f47f02b5de77061ba19eac8f"
const bigFishingArchiveSHA256 = "1e7753773e3afb794d704bc67f3e70bc58336bfd28cb762094da97e057f8914f"
const narniaArchiveSHA256 = "80c23d63c7c177b13ae59a69b8560e3395ac14b3f21348debc9d8bd1f3f041f9"
const devilOfMindArchiveSHA256 = "c02863ffe4b9d6e7caeb9e0ab55f5d863e3887f91fe3dc654908c652c14b8d88"
const ragnarokMagicianArchiveSHA256 = "3da1ea7b8e834ef11df146c8a0294577a34659a17b0ce83deefffe4359c65566"

// Package contains the inspected executable module and immutable archive data.
type Package struct {
	Module        []byte
	ClassIDs      []uint32
	Splash        *image.RGBA
	Files         map[string][]byte
	Authenticated bool
	// NativeCanvas is an exact package's handset display geometry. Zero keeps
	// the existing default for that package.
	NativeCanvas image.Point

	// PreferPackedAECHAR resolves ambiguous Hangul-looking EUC-KR pairs in
	// hash-qualified KTF titles without changing generic UTF-16 behavior.
	PreferPackedAECHAR bool
	// PreferPackedResourceAECHAR preserves OEM byte pairs in compressed string
	// resources for titles that read the low byte of each AECHAR directly.
	PreferPackedResourceAECHAR bool
	// WideOEMText preserves resource-backed AECHAR labels in an exact title's
	// menu footer, which passes those strings with IDF_TEXT_FORMAT_OEM.
	WideOEMText bool
	// LegacyKeyOnPress selects the pre-BREW-MP handset edge used by exact KTF
	// titles that act on EVT_KEY while a physical key is still down.
	LegacyKeyOnPress bool
}

// KTF shipped most BREW titles twice, for its 120x160-class handsets and for
// its 176x220-class handsets, and the package records neither. A package
// without an exact canvas policy is measured instead; see CanvasProbe.
var (
	SmallHandsetCanvas = image.Pt(int(framebufferWidth), int(framebufferHeight))
	LargeHandsetCanvas = image.Pt(176, 220)
)

// DisplaySize selects the handset canvas. Exact-title dimensions override
// the generic 120x160 canvas so native artwork is not clipped.
func (p Package) DisplaySize() image.Point {
	if p.NativeCanvas.X > 0 && p.NativeCanvas.Y > 0 {
		return p.NativeCanvas
	}
	if p.Authenticated {
		return image.Pt(120, 200)
	}
	return image.Pt(int(framebufferWidth), int(framebufferHeight))
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
	digestHex := hex.EncodeToString(digest[:])
	nativeCanvas := image.Point{}
	if digestHex == ArchiveSHA256 {
		// The title and menu compose against the common 176x220 KTF handset.
		// A 120-pixel surface cuts off the right half of the title treatment and
		// places the menu against the edge (issue #553).
		nativeCanvas = image.Pt(176, 220)
	}
	if digestHex == issue321ArchiveSHA256 {
		// The exact module BitBlts a 176x202 native frame from a 176-wide IDIB.
		nativeCanvas = image.Pt(176, 202)
	}
	if digestHex == jinJanggiArchiveSHA256 {
		// The title's menu/footer art is 176 pixels wide and its splash is
		// 180 pixels tall. The generic 120x160 canvas clips both and pushes
		// the game board against the screen edges.
		nativeCanvas = image.Pt(176, 220)
	}
	if digestHex == tengaiArchiveSHA256 {
		// The PAD's full-screen artwork is 176x204, above a 16-pixel
		// handset strip. The generic 120x160 canvas clips the title and menu.
		nativeCanvas = image.Pt(176, 220)
	}
	if digestHex == minigame2005ArchiveSHA256 {
		// The small-screen build of the same engine: its full-screen scenes
		// are 120x146, and a 120x160 canvas lands in the 176x207 layout
		// class, so the menu overruns the footer and screens leave the
		// title's version strip behind.
		nativeCanvas = image.Pt(120, 146)
	}
	if digestHex == minibookArchiveSHA256 {
		// The MBR's full-screen scenes are 176x207, the KTF handset's
		// application area. The module picks its layout from cyScreen:
		// up to 120, 150 or 210 are the handsets it was built for, and
		// anything taller is treated as a QVGA screen whose menus double
		// every sprite. A 220-pixel canvas therefore drew each menu item
		// two labels tall over 1x artwork, so every highlight covered two
		// entries (issue #558).
		nativeCanvas = image.Pt(176, 207)
	}
	if digestHex == battleDrumXArchiveSHA256 {
		// Multiple SC images in drumx.bar are 176x204, including the
		// menu background; the generic canvas cuts off its right side.
		nativeCanvas = image.Pt(176, 220)
	}
	if digestHex == topGun2ArchiveSHA256 {
		// The packaged HLB title and background images are 176 pixels wide,
		// with artwork as tall as 203 pixels.
		nativeCanvas = image.Pt(176, 220)
	}
	if digestHex == baseball2006ArchiveSHA256 {
		// 게임빌 2006프로야구 lays its menu and field out for 176x220; on the
		// generic 120x160 canvas the menu's check marks and OK bar fall off
		// the right edge, and its own blitter, which clips only a partly
		// visible sprite, copied a negative row length for one that lay
		// wholly past the narrower screen (aram-fuzz 160efa4658ded845).
		nativeCanvas = image.Pt(176, 220)
	}
	if digestHex == milkTycoon2ArchiveSHA256 {
		// The title's two full-screen BMPs are 176x202. The generic 120x160
		// canvas exposes only their upper-left corner (issue #522).
		nativeCanvas = image.Pt(176, 202)
	}
	if digestHex == ArchiveSHA256 {
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
		NativeCanvas:  nativeCanvas,

		PreferPackedAECHAR:         preferPackedAECHARForDigest(digestHex),
		PreferPackedResourceAECHAR: preferPackedResourceAECHARForDigest(digestHex),
		WideOEMText:                wideOEMTextForDigest(digestHex),
		LegacyKeyOnPress:           legacyKeyOnPressForDigest(digestHex),
	}, true, nil
}

func preferPackedAECHARForDigest(digest string) bool {
	switch digest {
	case issue319ArchiveSHA256, jinJanggiArchiveSHA256, theSINArchiveSHA256, unlimitedWarArchiveSHA256,
		narniaArchiveSHA256, ragnarokMagicianArchiveSHA256:
		return true
	default:
		return false
	}
}

func preferPackedResourceAECHARForDigest(digest string) bool {
	return digest == unlimitedWarArchiveSHA256
}

func wideOEMTextForDigest(digest string) bool {
	return digest == bigFishingArchiveSHA256
}

func legacyKeyOnPressForDigest(digest string) bool {
	switch digest {
	case devilOfMindArchiveSHA256, minibookArchiveSHA256:
		return true
	default:
		return false
	}
}

func knownModuleVeneer(module []byte) bool {
	if len(module) < 4 {
		return false
	}
	first := binary.LittleEndian.Uint32(module[:4])
	if first == 0xe92d400e || first == 0xe92d400c || first == 0xe1a0c002 {
		return true
	}
	if first&0xff000000 != 0xea000000 { // ARM B, excluding BL and conditional branches.
		return false
	}
	delta := int64(int32(first<<8) >> 6) // Sign-extend imm24, then scale by four.
	target := int64(8) + delta           // ARM branches use PC+8 as their base.
	return target >= 0 && target+4 <= int64(len(module))
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
	if classID == 0 || runtimeServiceClassID(classID) {
		return 0, false
	}
	return classID, true
}

func runtimeServiceClassID(classID uint32) bool {
	switch classID {
	case ShellClassID, DisplayClassID, HeapClassID, FileMgrClassID, OptionalDeviceClassID,
		KTFServiceClassID, SoundPlayerClassID, GraphicsClassID, Sound10ClassID,
		MemAStreamClassID, StaticClassID, TAPIClassID, Net11ClassID, TextCtl10ClassID,
		IconViewCtl10ClassID, SoftKeyCtl10ClassID, MenuCtl10ClassID,
		DateCtl10ClassID, ClockCtl10ClassID, WinBMPClassID, BitmapClassID:
		return true
	default:
		return false
	}
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
