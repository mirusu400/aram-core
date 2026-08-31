// Package quirkdb is the per-title compatibility database. Every entry is
// keyed by exact content digests (and, where available, package metadata),
// so a title that is not listed here can never match one. Generic runtime
// code consults this package through small lookup helpers; the mechanisms
// that act on a match stay in the runtimes, but the knowledge of *which*
// titles need them lives only here. New per-title behavior belongs in this
// database, not inline in runtime code.
package quirkdb

import (
	"crypto/sha256"
	"encoding/hex"
)

// TitleKey identifies one shipped KTF package build exactly.
type TitleKey struct {
	AID          string
	MainClass    string
	ClientSHA256 [sha256.Size]byte
}

func (k TitleKey) Matches(
	aid, mainClass string,
	clientSHA256 [sha256.Size]byte,
) bool {
	return aid == k.AID &&
		mainClass == k.MainClass &&
		clientSHA256 == k.ClientSHA256
}

// DisplayOverride corrects handset display metadata for a package whose
// descriptor carries a size from a different build while its client uses a
// larger fixed coordinate system.
type DisplayOverride struct {
	Key            TitleKey
	DeclaredWidth  int
	DeclaredHeight int
	Width          int
	Height         int
}

var DisplayOverrides = []DisplayOverride{
	{
		Key: TitleKey{
			AID:       "01035ACD",
			MainClass: "Clet",
			ClientSHA256: MustHash(
				"1ac810f1af96676e337817e8ddec400508924047ae652a6d34fbd3b2c94ffe96",
			),
		},
		DeclaredWidth:  176,
		DeclaredHeight: 220,
		Width:          240,
		Height:         320,
	},
}

// MenuForegroundOverlay describes a title that draws its menu labels before
// a full-menu overlay image and expects the labels to stay visible: the
// runtime defers recognized label draws and replays them above the overlay.
// Currently 다이어트 타이쿤 (Diet Tycoon).
type MenuForegroundOverlay struct {
	Key            TitleKey
	LabelHashes    [][sha256.Size]byte
	LabelMaxWidth  int
	LabelMaxHeight int
	OverlayHash    [sha256.Size]byte
	OverlayX       int
	OverlayY       int
	OverlayAnchor  uint32
	OverlayWidth   int
	OverlayHeight  int
}

var MenuForegroundOverlays = []MenuForegroundOverlay{
	{
		Key: TitleKey{
			AID:       "01034DCD",
			MainClass: "Diet",
			ClientSHA256: MustHash(
				"fb86d238b6ac2a3c38277ba0bc42670cfdb67ed64352d44d908864847cd36083",
			),
		},
		LabelHashes: [][sha256.Size]byte{
			MustHash("58f6aea343436ad866a77e94001fd0934a7bb18b70b113e9d36b637f054b95ad"),
			MustHash("8f9980866ceea846f1b583847625c81a4bcb985c27b47f15f9122641bf91c537"),
			MustHash("ba8004f816d161dff94759f694d016577bf9e4b734c36e21c52e6ab401679551"),
			MustHash("ca7ce962d1948d7324c65977e9a2f4ecd66d399a8c14e66b3984f307239983e0"),
			MustHash("5090a8aadebc9399d3420aad83eba1caba5b747d2c1508f493ef7bf8ff1087b7"),
			MustHash("953e2a4018d4f045680772fcbb3f85efadc82daf0c6de4b0b1f5b8553b1e4faa"),
			MustHash("aa5713be67f1b520e610d7ba1776a2a62652507781dd3c60050d62906c597298"),
		},
		LabelMaxWidth:  64,
		LabelMaxHeight: 16,
		OverlayHash: MustHash(
			"bcd3334f9a0ffe548975d8a38676e9d2104c798d5633a2efa8abc5921ddaba40",
		),
		OverlayX:      23,
		OverlayY:      145,
		OverlayAnchor: 0,
		OverlayWidth:  195,
		OverlayHeight: 107,
	},
}

// SKVMTitleKey identifies one shipped SKT MIDlet package build exactly. The
// digest is the lowercase hex SHA-256 of the whole distributed package, which
// is what the SKVM host already carries for the loaded input.
type SKVMTitleKey struct {
	PackageSHA256 string
	MainClass     string
	ProgramName   string
}

func (k SKVMTitleKey) Matches(packageSHA256, mainClass, programName string) bool {
	return packageSHA256 == k.PackageSHA256 &&
		mainClass == k.MainClass &&
		programName == k.ProgramName
}

// SKVMCanvas records the handset canvas one SKT MIDlet build was authored for.
// An SKT descriptor never declares a display size, and a title that packs its
// art into opaque resource blobs offers nothing to infer one from, so a build
// whose layout only closes up on a particular handset is recorded here rather
// than guessed.
type SKVMCanvas struct {
	Key SKVMTitleKey
	// InferredWidth and InferredHeight, when non-zero, additionally require
	// the geometry the host inferred from the package assets.
	InferredWidth  int
	InferredHeight int
	// Width and Height, when non-zero, replace the inferred geometry.
	Width  int
	Height int
	// CanvasHeightInset16 reports Canvas.getHeight() 16 pixels short of the
	// framebuffer, matching an SKT handset that reserved a system strip while
	// drawing still covered the complete display.
	CanvasHeightInset16 bool
}

var SKVMCanvases = []SKVMCanvas{
	{
		// 드래곤나이트EX (Dragon Knight EX) targets an SKT handset where
		// Canvas.getHeight() excluded a 16-pixel system strip while drawing
		// still covered the complete 120x160 framebuffer.
		Key: SKVMTitleKey{
			PackageSHA256: "fa1fc7826e4f2dbd10a4793177d9aed3282e5b9812d47863edc2f64761850cc2",
			MainClass:     "PNJDKEx",
			ProgramName:   "0053597505",
		},
		InferredWidth:       120,
		InferredHeight:      160,
		CanvasHeightInset16: true,
	},
	{
		// 고래사냥2 (Whale Hunting 2) keeps every image it ships inside an
		// opaque resource blob, so no package asset reveals the handset. Its
		// title screen draws a 128-pixel column flush against the right edge
		// (x = getWidth()-128) next to the 48-pixel column that completes the
		// picture at x = 0, and those two only meet on a 176-pixel-wide
		// canvas. The 205-pixel-tall picture then centers on a 220-pixel
		// display once Canvas.getHeight() reports the same 16-pixel system
		// strip inset the title already compensates for (aram-core #116).
		Key: SKVMTitleKey{
			PackageSHA256: "1367261bc3ee3b7f0afa102a52a7559204d94da60c543969773d47a09c051e79",
			MainClass:     "w",
			ProgramName:   "3523930101",
		},
		Width:               176,
		Height:              220,
		CanvasHeightInset16: true,
	},
}

// LookupSKVMCanvas answers with the recorded canvas for one shipped SKT MIDlet
// package, given the geometry the host inferred from its assets.
func LookupSKVMCanvas(
	packageSHA256, mainClass, programName string,
	inferredWidth, inferredHeight int,
) (SKVMCanvas, bool) {
	for _, entry := range SKVMCanvases {
		if !entry.Key.Matches(packageSHA256, mainClass, programName) {
			continue
		}
		if entry.InferredWidth != 0 && entry.InferredWidth != inferredWidth {
			continue
		}
		if entry.InferredHeight != 0 && entry.InferredHeight != inferredHeight {
			continue
		}
		return entry, true
	}
	return SKVMCanvas{}, false
}

// EADSTitleRuntime pins a dedicated EADS host runtime to one exact shipped
// binary. The only entry is the SKT SCH-W830 minigame pack (미니게임천국
// lineage), whose OEM native service ABI is modeled by internal/minigame.
type EADSTitleRuntime struct {
	ImageName string
	DatSHA256 string
}

var MinigameQVGAOEM = EADSTitleRuntime{
	ImageName: "MinigameQVGAOEM",
	DatSHA256: "955a39b3c09d6228224234dab18b3b38fe89da518c0b614a7cba47e6f9f96900",
}

func MustHash(value string) [sha256.Size]byte {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		panic("invalid built-in quirkdb hash")
	}
	var result [sha256.Size]byte
	copy(result[:], decoded)
	return result
}
