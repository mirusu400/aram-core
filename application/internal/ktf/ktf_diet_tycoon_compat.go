package ktf

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/draw"

	"github.com/mirusu400/aram-core/loader/ktf"
)

type ktfJavaImageDraw struct {
	image  uint32
	x      int
	y      int
	anchor uint32
}

// ktfMenuForegroundCompat models a title-local handset compatibility rule.
// It records the current coordinates rather than fixing them in the runtime,
// so menu rotation and selection animations keep following the guest.
type ktfMenuForegroundCompat struct {
	labelHashes  map[[sha256.Size]byte]struct{}
	overlayHash  [sha256.Size]byte
	overlayImage uint32
	pending      []ktfJavaImageDraw
}

var (
	ktfDietTycoonClientHash = mustKTFSHA256(
		"fb86d238b6ac2a3c38277ba0bc42670cfdb67ed64352d44d908864847cd36083",
	)
	ktfDietTycoonMenuHashes = [][sha256.Size]byte{
		mustKTFSHA256("58f6aea343436ad866a77e94001fd0934a7bb18b70b113e9d36b637f054b95ad"),
		mustKTFSHA256("8f9980866ceea846f1b583847625c81a4bcb985c27b47f15f9122641bf91c537"),
		mustKTFSHA256("ba8004f816d161dff94759f694d016577bf9e4b734c36e21c52e6ab401679551"),
		mustKTFSHA256("ca7ce962d1948d7324c65977e9a2f4ecd66d399a8c14e66b3984f307239983e0"),
		mustKTFSHA256("5090a8aadebc9399d3420aad83eba1caba5b747d2c1508f493ef7bf8ff1087b7"),
		mustKTFSHA256("953e2a4018d4f045680772fcbb3f85efadc82daf0c6de4b0b1f5b8553b1e4faa"),
		mustKTFSHA256("aa5713be67f1b520e610d7ba1776a2a62652507781dd3c60050d62906c597298"),
	}
	ktfDietTycoonLowerLogoHash = mustKTFSHA256(
		"bcd3334f9a0ffe548975d8a38676e9d2104c798d5633a2efa8abc5921ddaba40",
	)
)

func mustKTFSHA256(value string) [sha256.Size]byte {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		panic("invalid built-in KTF image hash")
	}
	var result [sha256.Size]byte
	copy(result[:], decoded)
	return result
}

func newKTFMenuForegroundCompat(pkg ktf.Package) *ktfMenuForegroundCompat {
	if pkg.Descriptor.AID != "01034DCD" ||
		pkg.Descriptor.MainClass != "Diet" ||
		sha256.Sum256(pkg.Client) != ktfDietTycoonClientHash {
		return nil
	}
	labels := make(map[[sha256.Size]byte]struct{}, len(ktfDietTycoonMenuHashes))
	for _, digest := range ktfDietTycoonMenuHashes {
		labels[digest] = struct{}{}
	}
	return &ktfMenuForegroundCompat{
		labelHashes: labels,
		overlayHash: ktfDietTycoonLowerLogoHash,
	}
}

func (r *Runtime) drawKTFJavaImage(
	state *ktfGraphics,
	imageAddress uint32,
	source image.Image,
	x, y int,
	anchor uint32,
) {
	r.drawKTFJavaImageRaw(state, source, x, y, anchor)
	compat := r.menuForegroundCompat
	if compat == nil || state == nil || source == nil {
		return
	}
	bounds := source.Bounds()
	if bounds.Dx() <= 64 && bounds.Dy() <= 16 {
		digest := sha256.Sum256(ktfRGBABytes(source))
		if _, ok := compat.labelHashes[digest]; ok {
			compat.pending = append(compat.pending, ktfJavaImageDraw{
				image: imageAddress,
				x:     x, y: y,
				anchor: anchor,
			})
			return
		}
	}
	if imageAddress != compat.overlayImage {
		if x != 23 || y != 145 || anchor != 0 ||
			bounds.Dx() != 195 || bounds.Dy() != 107 ||
			sha256.Sum256(ktfRGBABytes(source)) != compat.overlayHash {
			return
		}
		compat.overlayImage = imageAddress
	}
	for _, pending := range compat.pending {
		pendingSource := r.images[pending.image]
		if pendingSource != nil {
			r.drawKTFJavaImageRaw(
				state,
				pendingSource,
				pending.x,
				pending.y,
				pending.anchor,
			)
		}
	}
	compat.pending = nil
}

func (r *Runtime) drawKTFJavaImageRaw(
	state *ktfGraphics,
	source image.Image,
	x, y int,
	anchor uint32,
) {
	if state == nil || source == nil {
		return
	}
	if anchor&8 != 0 {
		x -= source.Bounds().Dx()
	} else if anchor&1 != 0 {
		x -= source.Bounds().Dx() / 2
	}
	if anchor&32 != 0 {
		y -= source.Bounds().Dy()
	} else if anchor&2 != 0 {
		y -= source.Bounds().Dy() / 2
	}
	point := image.Pt(x+state.translate.X, y+state.translate.Y)
	targetRect := source.Bounds().Add(point.Sub(source.Bounds().Min))
	clippedRect := targetRect.Intersect(state.clip)
	sourcePoint := source.Bounds().Min.Add(
		clippedRect.Min.Sub(targetRect.Min),
	)
	draw.Draw(
		state.Target,
		clippedRect,
		source,
		sourcePoint,
		draw.Over,
	)
	state.PixelsDirty = true
}
