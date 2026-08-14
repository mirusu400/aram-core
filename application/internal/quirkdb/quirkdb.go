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
